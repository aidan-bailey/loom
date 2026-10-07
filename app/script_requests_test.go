package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// intentsOf lists the intents a script's result carries.
func intentsOf(done scriptDoneMsg) []script.Intent {
	var out []script.Intent
	for _, p := range done.pendingIntents {
		out = append(out, p.intent)
	}
	return out
}

// runCmds runs cmd and, recursively, every Cmd of a tea.BatchMsg it
// produces, as the runtime would, and returns their messages. The error
// bar's hide timer waits on m.ctx, so callers cancel it (cancelledCtx).
func runCmds(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("runCmds met a tea.Sequence, whose ordering it does not model")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		out = append(out, msg)
	}
	return out
}

// coreResults runs cmd (runCmds) and returns the model's job results among
// its messages, undelivered.
func coreResults(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	for _, msg := range runCmds(t, cmd) {
		if _, ok := msg.(coreResultMsg); ok {
			out = append(out, msg)
		}
	}
	return out
}

// pumpRequests feeds cmd's script and model messages back through Update
// until none is left: a script's dispatch and resumes (scriptDoneMsg,
// scriptResumeMsg) and the model's job results (coreResultMsg), so a Lua
// call that waits on the model runs to its end. It returns the
// scriptDoneMsgs it delivered, in order. The error bar's hide timer waits
// on m.ctx, which it cancels for its run, so the timer returns at once.
func pumpRequests(t *testing.T, m *home, cmd tea.Cmd) []scriptDoneMsg {
	t.Helper()
	prev := m.ctx
	m.ctx = cancelledCtx()
	defer func() { m.ctx = prev }()
	var done []scriptDoneMsg
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		require.Less(t, steps, 200, "request pump did not settle")
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("pumpRequests met a tea.Sequence, whose ordering it does not model")
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case scriptDoneMsg:
			done = append(done, msg)
			_, next := m.Update(msg)
			queue = append(queue, next)
		case scriptResumeMsg, coreResultMsg:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
	return done
}

// withScript loads src as m's only user script.
func withScript(t *testing.T, m *home, src string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lua.lua"), []byte(src), 0o644))
	m.scripts = nil
	initScriptsIn(m, dir, false)
}

// runKey dispatches key's script and pumps it to its end (pumpRequests),
// returning every scriptDoneMsg delivered.
func runKey(t *testing.T, m *home, key string) []scriptDoneMsg {
	t.Helper()
	cmd, ok := m.dispatchScript(key)
	require.True(t, ok, "fixture: %s is bound", key)
	return pumpRequests(t, m, cmd)
}

// lastErr is the error of the last script result, which a raise ends.
func lastErr(t *testing.T, done []scriptDoneMsg) error {
	t.Helper()
	require.NotEmpty(t, done)
	return done[len(done)-1].err
}

// storedRecords decodes what a recording storage last saved.
func storedRecords(t *testing.T, rec *recordingInstanceStorage) []session.InstanceData {
	t.Helper()
	var out []session.InstanceData
	require.NoError(t, json.Unmarshal(rec.lastData, &out))
	return out
}

// pausedRecordHome is ownerTestHome with a Paused record, a1, on a git
// repository, selected and seeded into its storage: one a kill can delete.
func pausedRecordHome(t *testing.T) (m *home, a1 *session.Instance, recA *recordingInstanceStorage) {
	t.Helper()
	isolateTmux(t)
	repo := t.TempDir()
	out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))
	m, recA, _ = ownerTestHome(t)
	a1, err = session.FromInstanceData(session.InstanceData{
		Title: "a1", Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: t.TempDir(), BranchName: "loom/a1", SessionName: "a1"},
	}, t.TempDir())
	require.NoError(t, err)
	m.ws.AddForTest(a1)
	selectIn(m, m.list, a1)
	seed, err := json.Marshal([]session.InstanceData{a1.ToInstanceData()})
	require.NoError(t, err)
	recA.lastData = seed
	return m, a1, recA
}

// TestScriptKill_RemovesTheRowAndTheRecord: a Lua inst:kill() used to kill
// the session from the script's goroutine and leave its row and its stored
// record behind. It is a request to the model now, like the D key's.
func TestScriptKill_RemovesTheRowAndTheRecord(t *testing.T) {
	m, a1, recA := pausedRecordHome(t)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():kill() end)`)
	id := idOf(m, a1)

	done := runKey(t, m, "Z")

	require.Len(t, done, 2, "the dispatch, then the Reply's resume")
	assert.Equal(t, []script.Intent{script.InstanceOpIntent{ID: id, Title: "a1", Op: "kill"}}, intentsOf(done[0]))
	assert.NoError(t, lastErr(t, done), "the kill succeeded: inst:kill() returned")
	assert.Nil(t, m.list.GetInstanceByTitle("a1"), "the row is gone")
	assert.GreaterOrEqual(t, recA.calls, 1)
	assert.NotContains(t, string(recA.lastData), `"a1"`, "and so is the stored record")
	assert.Empty(t, m.pending, "the Reply was handled")
}

// TestScriptPause_ShowsTheSpinnerAndSavesThePausedRecord: a Lua
// inst:pause() used to pause the session from the script's goroutine with
// no spinner and no save. It is a request to the model now, like s.
func TestScriptPause_ShowsTheSpinnerAndSavesThePausedRecord(t *testing.T) {
	isolateTmux(t)
	m, recA, _ := ownerTestHome(t)
	inst := startedInstanceWithProgram(t, "lua-paused", "claude", "idle")
	m.ws.AddForTest(inst)
	selectIn(m, m.list, inst)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():pause() end)`)

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	first, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	_, next := m.Update(first)
	assert.Equal(t, session.Loading, shownStatus(t, m, inst), "the row shows the spinner while the pause runs")

	done := pumpRequests(t, m, next)

	require.Len(t, done, 1, "the Reply resumed the script once")
	assert.NoError(t, done[0].err)
	assert.Equal(t, session.Paused, inst.GetStatus())
	records := storedRecords(t, recA)
	require.Len(t, records, 1)
	assert.Equal(t, "lua-paused", records[0].Title)
	assert.Equal(t, session.Paused, records[0].Status, "the record is saved as Paused")
}

// TestScriptPause_RefusesAPausedSession: Instance.Pause refused a Paused
// session, so a Lua inst:pause() of one raised. The model admits one, as s
// does; the host keeps Lua's refusal, with its old message, and asks the
// model nothing.
func TestScriptPause_RefusesAPausedSession(t *testing.T) {
	m, a1, recA := pausedRecordHome(t)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():pause() end)`)
	saves := recA.calls

	done := runKey(t, m, "Z")

	require.Len(t, done, 2)
	err := lastErr(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pause: instance is already paused")
	assert.Equal(t, session.Paused, a1.GetStatus())
	assert.Empty(t, m.pending, "no request was made")
	assert.Equal(t, saves, recA.calls, "and nothing was saved")
}

// TestScriptResume_RecoversARecoverableSession: before the model ran Lua's
// calls, inst:resume() on an orphan adopted it by accident (Instance.Resume
// reattached or relaunched it). The model's Resume refuses one, so the
// host recovers it, as r does (runResumeOrRecover).
func TestScriptResume_RecoversARecoverableSession(t *testing.T) {
	m := homeWithAppState(t)
	m.ctx = cancelledCtx()
	placeholder, err := session.FromInstanceData(session.InstanceData{
		Title: "orphan", Status: session.Recoverable, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(), BranchName: "u/orphan"},
	}, t.TempDir())
	require.NoError(t, err)
	m.ws.AddForTest(placeholder)
	selectIn(m, m.list, placeholder)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():resume() end)`)

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	first, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	_, next := m.Update(first)

	assert.Equal(t, session.Loading, placeholder.GetStatus(), "Recover moved it to Loading")
	results := coreResults(t, next)
	require.Len(t, results, 1)
	assert.IsType(t, core.RecoverResult{}, core.UntrackedForTest(results[0].(coreResultMsg).msg),
		"the request was a Recover, not a Resume")
	require.Len(t, m.pending, 1, "the script waits on the Recover's Reply")
}

// TestScriptResume_RefusedRaises: a resume the model refuses (here a
// workspace terminal, which Instance.Resume refused too) answers at once.
// Its Reply resumes the script exactly once, and inst:resume() raises
// "resume: …" at the script's line.
func TestScriptResume_RefusedRaises(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	term := &session.Instance{Title: "term", IsWorkspaceTerminal: true}
	m.ws.AddForTest(term)
	selectIn(m, m.list, term)
	withScript(t, m, "cs.bind(\"Z\", function(ctx)\n  ctx:selected():resume()\n  ctx:notify(\"after\")\nend)")

	done := runKey(t, m, "Z")

	require.Len(t, done, 2, "the dispatch, then one resume")
	err := lastErr(t, done)
	require.Error(t, err, "a workspace terminal cannot be resumed")
	assert.Contains(t, err.Error(), "resume: resume term: not allowed on a workspace terminal")
	assert.Contains(t, err.Error(), ":2:", "raised at the script's line")
	assert.Empty(t, done[1].notices, "nothing after the call ran")
	assert.Empty(t, m.pending)
	assert.Contains(t, m.errBox.String(), "resume term", "the script's error is shown")
}

// TestScriptReplied_ANoticeAloneResumesWithNothing: a kill or resume that
// succeeded with something to report (a stash it forgot or could not drop)
// used to raise in Lua, or reach the host's Notify. The model shows the
// notice now (a core.Notice event); its Reply resumes the call with
// nothing, and the TUI adds no notice of its own. A failure resumes it
// with "<op>: <err>", which the method raises.
func TestScriptReplied_ANoticeAloneResumesWithNothing(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	blank := m.errBox.String()
	resumed := func(r core.Reply) script.ResumeValue {
		t.Helper()
		msg, ok := m.scriptReplied(&pendingScript{intent: 7, trace: "tr", op: "kill"}, r)().(scriptResumeMsg)
		require.True(t, ok)
		assert.Equal(t, script.IntentID(7), msg.id)
		assert.Equal(t, "tr", msg.trace)
		return msg.value
	}

	assert.Equal(t, script.ResumeValue{}, resumed(core.Reply{ID: 1, Notice: errors.New("could not drop stash abc")}),
		"a notice alone resumes the call with nothing")
	assert.Equal(t, blank, m.errBox.String(), "the TUI adds no notice: the model shows it")
	assert.Equal(t, script.ResumeValue{Err: "kill: boom"}, resumed(core.Reply{ID: 1, Err: errors.New("boom")}))
	assert.Equal(t, script.ResumeValue{}, resumed(core.Reply{ID: 1}), "success resumes it with nothing")
	assert.Equal(t, blank, m.errBox.String())
}

// TestScriptKill_AJobFailureIsShownAndRaised: a kill whose job fails shows
// the model's notice when its result lands, as the D key's does; its Reply
// then raises "kill: <err>" in the script, whose error the TUI shows in
// turn (the error bar holds one message, so the second replaces the
// first, naming the script's file and line).
func TestScriptKill_AJobFailureIsShownAndRaised(t *testing.T) {
	m := homeWithAppState(t)
	m.ctx = cancelledCtx()
	m.errBox.SetSize(400, 1)
	// Never started: the kill's job can't get its worktree.
	inst, err := session.NewInstance(session.InstanceOptions{Title: "fresh", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	m.ws.AddForTest(inst)
	selectIn(m, m.list, inst)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():kill() end)`)

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	first, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	_, next := m.Update(first)
	results := coreResults(t, next)
	require.Len(t, results, 1)
	_, resume := m.Update(results[0])

	assert.Contains(t, m.errBox.String(), "cannot get git worktree", "the model's notice")
	assert.NotContains(t, m.errBox.String(), "kill:")
	done := pumpRequests(t, m, resume)
	require.Len(t, done, 1)
	require.Error(t, done[0].err)
	assert.Contains(t, done[0].err.Error(), "kill: cannot get git worktree")
	assert.Contains(t, m.errBox.String(), "kill: cannot get git worktree", "then the script's raise")
	assert.NotNil(t, m.list.GetInstanceByTitle("fresh"), "the row stays")
}

// TestScriptSendPrompt_HoldsInputUntilTheReply: a Lua inst:send_prompt()
// is a prompt send like the TUI's own (decision 12): while it lands, the
// TUI holds input to the session, and a send of its own is refused, with
// the same info line. The Reply releases the hold and resumes the script.
func TestScriptSendPrompt_HoldsInputUntilTheReply(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	require.NoError(t, inst.EnsureRunning())
	m.syncViews()
	id := idOf(m, inst)
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():send_prompt("hello") end)`)

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	first, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	assert.Equal(t, []script.Intent{script.InstanceOpIntent{ID: id, Title: inst.Title, Op: "send_prompt", Text: "hello"}}, intentsOf(first))
	_, next := m.Update(first)

	require.True(t, m.sending[id], "the Lua send holds input")
	_, _ = runInlineAttachAgent(m)
	assert.Equal(t, stateDefault, m.state, "inline attach is refused while it lands")
	assert.Contains(t, m.errBox.String(), "still sending the last prompt to "+inst.Title)

	done := pumpRequests(t, m, next)

	require.Len(t, done, 1)
	assert.NoError(t, done[0].err)
	assert.Empty(t, m.sending, "the Reply releases the hold")
	assert.Empty(t, m.pending)
}

// TestScriptSendPrompt_RefusedWhileASendLands: a Lua send to a session the
// TUI is still sending to is refused, as a second send of the TUI's is,
// and raises: its text would land between the other's paste and Enter.
func TestScriptSendPrompt_RefusedWhileASendLands(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	require.NoError(t, inst.EnsureRunning())
	m.syncViews()
	m.sending = map[core.InstanceID]bool{idOf(m, inst): true} // a send already in flight
	withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():send_prompt("hello") end)`)

	done := runKey(t, m, "Z")

	require.Len(t, done, 2)
	err := lastErr(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send_prompt: still sending the last prompt to "+inst.Title)
	assert.Empty(t, m.pending, "nothing was sent")
	assert.True(t, m.sending[idOf(m, inst)], "the other send still holds input")
}

// TestScriptNewInstance_ReturnsTheCreatedInstance: ctx:new_instance yields
// until the model has created the instance, unstarted, in the dispatch's
// workspace, and returns it.
func TestScriptNewInstance_ReturnsTheCreatedInstance(t *testing.T) {
	m := homeWithAppState(t)
	withScript(t, m, `cs.bind("Z", function(ctx)
  local inst = ctx:new_instance{title = "scripted", program = "aider", prompt = "go"}
  ctx:notify(inst:title() .. " " .. inst:program() .. " " .. tostring(inst:started()))
end)`)

	done := runKey(t, m, "Z")

	require.Len(t, done, 2, "the dispatch, then the Reply's resume")
	assert.Equal(t, []script.Intent{script.CreateInstanceIntent{Title: "scripted", Program: "aider", Path: m.repoPath(), Prompt: "go"}},
		intentsOf(done[0]))
	assert.NoError(t, done[1].err)
	assert.Equal(t, []string{"scripted aider false"}, done[1].notices, "the call returned the created, unstarted instance")
	row := m.list.GetInstanceByTitle("scripted")
	require.NotNil(t, row, "the row is in the dispatch's workspace")
	inst := testModel(m).InstanceForTest(row.ID)
	require.NotNil(t, inst)
	assert.Equal(t, "go", inst.Prompt())
	assert.False(t, inst.Started())
}
