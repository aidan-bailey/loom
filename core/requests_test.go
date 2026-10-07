package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run executes the jobs of out and delivers their results, returning what
// the model produced after them.
func run(m *Model, out Out) Out {
	for _, j := range out.Jobs {
		m.Deliver(j())
	}
	return m.Drain()
}

func replies(out Out) []Reply {
	var rs []Reply
	for _, ev := range out.Events {
		if r, ok := ev.(Reply); ok {
			rs = append(rs, r)
		}
	}
	return rs
}

func TestRequests_AnUnknownIDIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	m.Kill(99, 5)
	assert.Equal(t, []Reply{{Req: 5, ID: 99, Err: ErrNoSession}}, replies(m.Drain()))
	m.Kill(99, 0) // no reply wanted
	assert.Empty(t, replies(m.Drain()))
}

// TestRequests_RefusalsMatchErrRefused: every refusal's Err matches
// ErrRefused, its own message kept (a gone session, a failed precondition);
// a failure of the request's job does not, since the model already
// reported it in a Notice.
func TestRequests_RefusalsMatchErrRefused(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	busy, unstarted := newInst(t, "busy"), newInst(t, "unstarted")
	require.NoError(t, busy.TransitionTo(session.Loading))
	ws.add(busy)
	ws.add(unstarted)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Kill(99, 1)
	m.Kill(m.idOf(busy), 2)
	rs := replies(m.Drain())
	require.Len(t, rs, 2)
	assert.ErrorIs(t, rs[0].Err, ErrRefused, "a gone session is a refusal")
	assert.ErrorIs(t, rs[0].Err, ErrNoSession)
	assert.ErrorIs(t, rs[1].Err, ErrRefused, "so is a failed precondition")
	assert.EqualError(t, rs[1].Err, "kill busy: the session is busy (Loading)", "its message is kept")
	assert.NotErrorIs(t, rs[1].Err, ErrNoSession)

	m.Kill(m.idOf(unstarted), 3) // admitted; its job fails (no worktree)
	out := run(m, m.Drain())
	rs = replies(out)
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.NotErrorIs(t, rs[0].Err, ErrRefused, "a job's failure is no refusal")
	assert.Contains(t, out.Events, Event(Notice{Err: rs[0].Err}), "the model reported it itself")
}

// TestKill_RepliesWhenItFinishes: an unstarted instance has no worktree, so
// the kill job refuses (OpFailed); the Reply comes when its result lands,
// not when the request is made.
func TestKill_RepliesWhenItFinishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Kill(id, 7)
	out := m.Drain()
	assert.Empty(t, replies(out), "no reply before the job finishes")
	assert.Equal(t, session.Deleting, inst.GetStatus(), "the pre-step ran")
	require.Len(t, out.Jobs, 1)

	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, id, rs[0].ID)
	assert.Error(t, rs[0].Err)
}

// TestPause_AFailedPauseRevertsToTheStatusItHad: Pause takes the job (and
// with it the status a failure reverts to) before it moves the session to
// Loading, as the TUI's path does, so a failed pause does not leave the
// session stuck in Loading.
func TestPause_AFailedPauseRevertsToTheStatusItHad(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x") // unstarted: Instance.Pause refuses it
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Pause(id, 6)
	out := m.Drain()
	assert.Equal(t, session.Loading, inst.GetStatus(), "the spinner shows at once")
	assert.Empty(t, replies(out))
	require.Len(t, out.Jobs, 1)

	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(6), rs[0].Req)
	assert.Error(t, rs[0].Err)
	assert.Equal(t, session.Ready, inst.GetStatus(), "reverted to the status it had, not to Loading")
}

// TestResume_ShowsTheSpinnerAndRepliesOnlyWhenItFinishes: Resume moves the
// session to Loading at once and queues the resume; the Reply waits for it.
func TestResume_ShowsTheSpinnerAndRepliesOnlyWhenItFinishes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Resume(m.idOf(inst), 8)
	out := m.Drain()
	assert.Equal(t, session.Loading, inst.GetStatus())
	assert.Len(t, out.Jobs, 1)
	assert.Empty(t, replies(out), "no reply before the resume finishes")
}

// TestRecover_RepliesWithTheAdoptedInstance: a Recover's Reply names the
// instance that replaced the placeholder, not the placeholder.
func TestRecover_RepliesWithTheAdoptedInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	placeholder, adopted := pausedInst(t, "x"), pausedInst(t, "x")
	ws.add(placeholder)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	pid := m.idOf(placeholder)

	m.spawn(m.track(4, pid, func() any {
		return RecoverResult{Placeholder: placeholder, Owner: ws, OldTitle: "x", Recovered: adopted}
	}))
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(4), rs[0].Req)
	assert.NoError(t, rs[0].Err)
	assert.Equal(t, m.idOf(adopted), rs[0].ID)
	assert.NotEqual(t, pid, rs[0].ID)
	assert.Same(t, adopted, ws.instances()[0], "the adoption was applied before the reply")
}

func TestCreate_RepliesWithTheNewIDAndStarts(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.createWS(ws, NewInstance{Title: "new", Path: t.TempDir(), Program: "claude", Prompt: "hi", Start: true,
		Launch: launch.Options{BranchPrefix: "me/"}}, 3)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	require.NotZero(t, rs[0].ID)
	v, ok := m.View(rs[0].ID)
	require.True(t, ok)
	assert.Equal(t, "new", v.Title)
	assert.Equal(t, session.Loading, v.Status)
	assert.Len(t, out.Jobs, 1, "the start job")
}

func TestCreate_WithoutStartLeavesItUnstarted(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.createWS(ws, NewInstance{Title: "s", Path: t.TempDir(), Program: "claude"}, 4)
	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	v, _ := m.View(rs[0].ID)
	assert.False(t, v.Started)
	assert.Equal(t, session.Ready, v.Status)
	assert.Empty(t, out.Jobs)
}

func TestCreate_AWorkspaceNoLongerLoadedIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	ws, closed := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.createWS(closed, NewInstance{Title: "s", Path: t.TempDir(), Program: "claude", Start: true}, 2)
	out := m.Drain()
	assert.Empty(t, out.Jobs)
	assert.Empty(t, closed.instances())
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Zero(t, rs[0].ID)
	assert.ErrorContains(t, rs[0].Err, "no longer open")
}

func TestResumeWith_AppliesTheLaunchOptions(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.ResumeWith(m.idOf(inst), launch.Options{Model: "sonnet", Account: "default"}, "claude", 0)
	assert.Contains(t, inst.Program(), "sonnet")
	assert.Equal(t, session.Loading, inst.GetStatus())
	assert.Len(t, m.Drain().Jobs, 1)
}

func TestFetchIssue_RepliesWithTheIssue(t *testing.T) {
	r := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if strings.Contains(c.String(), "issue view") {
				return []byte(`{"number":12,"title":"Fix it","body":"do","url":"u","state":"OPEN","labels":[]}`), nil
			}
			return nil, errors.New("unexpected " + c.String())
		},
	}
	m := NewForTest(Options{})
	m.spawn(m.track(9, 0, fetchIssueJob("/repo", 12, r)))
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.NoError(t, rs[0].Err)
	assert.Equal(t, 12, rs[0].Issue.Number)
	assert.Equal(t, "Fix it", rs[0].Issue.Title)
}

// TestIDTriggers_IgnoreAnUnknownID: the ID versions of the pane and tick
// triggers do nothing for an ID no loaded workspace holds (Tick still
// ticks, with no selection).
func TestIDTriggers_IgnoreAnUnknownID(t *testing.T) {
	m := NewForTest(Options{})
	m.PaneOutput(42)
	m.PaneQuiet(42)
	m.VerifyDead(42)
	assert.True(t, m.Drain().Empty())
	m.Tick(42)
	assert.NotEmpty(t, m.Drain().Jobs, "the probe is queued with no selection")
}

// statusInst builds a started instance titled title in status st (a
// reconciled record moved on, no tmux contacted): Paused or Recoverable
// as restored, anything else reached from Paused.
func statusInst(t *testing.T, title string, st session.Status) *session.Instance {
	t.Helper()
	from := session.Paused
	if st == session.Recoverable {
		from = session.Recoverable
	}
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: from, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(st))
	return inst
}

// runningTerminal builds a started, Running workspace terminal: it has no
// worktree (GetGitWorktree answers nil, nil).
func runningTerminal(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.True(t, inst.Started(), "fixture precondition")
	return inst
}

// TestRequests_RefuseWhatTheTUIRefuses: each ID request refuses, before it
// changes anything, what the TUI's gate for the same action refuses
// (precondition), with a Reply naming the operation, the session and why.
func TestRequests_RefuseWhatTheTUIRefuses(t *testing.T) {
	type fixture struct {
		m  *Model
		id map[string]InstanceID
	}
	for _, tc := range []struct {
		name string
		do   func(f fixture)
		want string
	}{
		{"kill a terminal", func(f fixture) { f.m.Kill(f.id["term"], 1) }, "kill term: not allowed on a workspace terminal"},
		{"kill a loading session", func(f fixture) { f.m.Kill(f.id["loading"], 1) }, "kill loading: the session is busy (Loading)"},
		{"kill a deleting session", func(f fixture) { f.m.Kill(f.id["deleting"], 1) }, "kill deleting: the session is busy (Deleting)"},
		{"pause a terminal", func(f fixture) { f.m.Pause(f.id["term"], 1) }, "pause term: not allowed on a workspace terminal"},
		{"pause a loading session", func(f fixture) { f.m.Pause(f.id["loading"], 1) }, "pause loading: the session is busy (Loading)"},
		{"push a terminal", func(f fixture) { f.m.Push(f.id["term"], 1) }, "push term: not allowed on a workspace terminal"},
		{"push a deleting session", func(f fixture) { f.m.Push(f.id["deleting"], 1) }, "push deleting: the session is busy (Deleting)"},
		{"resume a terminal", func(f fixture) { f.m.Resume(f.id["term"], 1) }, "resume term: not allowed on a workspace terminal"},
		{"resume a running session", func(f fixture) { f.m.Resume(f.id["running"], 1) }, "resume running: the session is not paused (Running)"},
		{"resume a recoverable session", func(f fixture) { f.m.Resume(f.id["orphan"], 1) }, "resume orphan: the session is not paused (Recoverable)"},
		{"restart a running session", func(f fixture) {
			f.m.ResumeWith(f.id["running"], launch.Options{Model: "sonnet"}, "claude", 1)
		}, "resume running: the session is not paused (Running)"},
		{"restart a terminal", func(f fixture) {
			f.m.ResumeWith(f.id["term"], launch.Options{Model: "sonnet"}, "claude", 1)
		}, "resume term: not allowed on a workspace terminal"},
		{"recover a running session", func(f fixture) { f.m.Recover(f.id["running"], 1) }, "recover running: the session is not recoverable (Running)"},
		{"recover a paused session", func(f fixture) { f.m.Recover(f.id["paused"], 1) }, "recover paused: the session is not recoverable (Paused)"},
		{"merge into a terminal", func(f fixture) { f.m.Merge(f.id["term"], f.id["paused"], 1) }, "merge into term: not allowed on a workspace terminal"},
		{"merge into a loading session", func(f fixture) { f.m.Merge(f.id["loading"], f.id["paused"], 1) }, "merge into loading: the session is busy (Loading)"},
		{"merge into an unstarted session", func(f fixture) { f.m.Merge(f.id["new"], f.id["paused"], 1) }, "merge into new: the session has not started"},
		{"merge from a terminal", func(f fixture) { f.m.Merge(f.id["paused"], f.id["term"], 1) }, "merge from term: not allowed on a workspace terminal"},
		{"merge from a deleting session", func(f fixture) { f.m.Merge(f.id["paused"], f.id["deleting"], 1) }, "merge from deleting: the session is busy (Deleting)"},
		{"merge into itself", func(f fixture) { f.m.Merge(f.id["paused"], f.id["paused"], 1) }, "merge paused: a session can't be merged into itself"},
		{"send to an unstarted session", func(f fixture) { f.m.SendPrompt(f.id["new"], "hi", 1) }, "send a prompt to new: the session has not started"},
		{"send to a paused session", func(f fixture) { f.m.SendPrompt(f.id["paused"], "hi", 1) }, "send a prompt to paused: the session is paused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewForTest(Options{})
			ws := storedWorkspace(t, "a")
			insts := map[string]*session.Instance{
				"term":     runningTerminal(t, "term"),
				"running":  statusInst(t, "running", session.Running),
				"paused":   statusInst(t, "paused", session.Paused),
				"loading":  statusInst(t, "loading", session.Loading),
				"deleting": statusInst(t, "deleting", session.Deleting),
				"orphan":   statusInst(t, "orphan", session.Recoverable),
				"new":      newInst(t, "new"),
			}
			before := map[string]session.Status{}
			programs := map[string]string{}
			ids := map[string]InstanceID{}
			for name, inst := range insts {
				ws.add(inst)
				before[name] = inst.GetStatus()
				programs[name] = inst.Program()
			}
			m.SetWorkspacesForTest(nil, []*Workspace{ws})
			for name, inst := range insts {
				ids[name] = m.idOf(inst)
			}

			tc.do(fixture{m: m, id: ids})
			out := m.Drain()
			assert.Empty(t, out.Jobs, "a refused request runs nothing")
			rs := replies(out)
			require.Len(t, rs, 1)
			assert.Equal(t, ReqID(1), rs[0].Req)
			assert.EqualError(t, rs[0].Err, tc.want)
			for name, inst := range insts {
				assert.Equal(t, before[name], inst.GetStatus(), "%s's status is unchanged", name)
				assert.Equal(t, programs[name], inst.Program(), "%s's program is unchanged", name)
			}
		})
	}
}

// TestRequests_ARefusalWithoutAReqIDIsOnlyLogged: with no ReqID a refused
// precondition answers nothing and changes nothing.
func TestRequests_ARefusalWithoutAReqIDIsOnlyLogged(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	term := runningTerminal(t, "term")
	ws.add(term)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.Kill(m.idOf(term), 0)
	assert.True(t, m.Drain().Empty())
	assert.Equal(t, session.Running, term.GetStatus())
}

// TestMerge_AnUnknownSourceIsRefused: a source no loaded workspace holds
// refuses the merge, naming the target.
func TestMerge_AnUnknownSourceIsRefused(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	target := pausedInst(t, "t")
	ws.add(target)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(target)

	m.Merge(id, 99, 2)
	out := m.Drain()
	assert.Empty(t, out.Jobs)
	assert.Equal(t, []Reply{{Req: 2, ID: id, Err: ErrNoSession}}, replies(out))
}

// TestKill_ASecondKillIsRefusedWhileTheFirstRuns: the first kill moved the
// session to Deleting, which refuses the second, so one kill job runs.
func TestKill_ASecondKillIsRefusedWhileTheFirstRuns(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Kill(id, 1)
	m.Kill(id, 2)
	out := m.Drain()
	assert.Len(t, out.Jobs, 1, "one kill job")
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(2), rs[0].Req)
	assert.EqualError(t, rs[0].Err, "kill x: the session is busy (Deleting)")
}

// TestKill_AdmitsWhatTheTUIKills: D kills a Paused session and discards a
// Recoverable one, so both are admitted.
func TestKill_AdmitsWhatTheTUIKills(t *testing.T) {
	for _, st := range []session.Status{session.Paused, session.Recoverable, session.Running} {
		t.Run(st.String(), func(t *testing.T) {
			m := NewForTest(Options{})
			ws := storedWorkspace(t, "a")
			inst := statusInst(t, "x", st)
			ws.add(inst)
			m.SetWorkspacesForTest(nil, []*Workspace{ws})

			m.Kill(m.idOf(inst), 1)
			out := m.Drain()
			assert.Empty(t, replies(out), "no refusal")
			assert.Len(t, out.Jobs, 1)
			assert.Equal(t, session.Deleting, inst.GetStatus())
		})
	}
}

// TestInstJobs_ANilWorktreeIsAnErrorNotAPanic: a started workspace terminal
// has no worktree. The kill, push and merge jobs must report that, not
// dereference nil, since a job panics on the TUI's Cmd goroutine.
func TestInstJobs_ANilWorktreeIsAnErrorNotAPanic(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	term := runningTerminal(t, "term")
	other := pausedInst(t, "other")
	ws.add(term)
	ws.add(other)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	_, kill := m.killInst(ws, term)
	var killed any
	require.NotPanics(t, func() { killed = kill() })
	failed, ok := killed.(OpFailed)
	require.True(t, ok, "an OpFailed, got %#v", killed)
	assert.EqualError(t, failed.Err, "instance term has no worktree")
	assert.Equal(t, session.Running, failed.Previous)

	var pushed any
	require.NotPanics(t, func() { pushed = m.pushInst(term)() })
	assert.Equal(t, pushResult{err: errors.New("push: term has no worktree")}, pushed)

	var merged any
	require.NotPanics(t, func() { merged = m.mergeInst(term, other)() })
	assert.Equal(t, MergeResult{Err: errors.New("merge: term has no worktree")}, merged)
}

// TestResumeIfLoadingInst_ASkipRepliesWithAnError: a resume whose job finds
// the session no longer Loading did nothing, so its Reply is an error, not
// a success. It shows no notice, as the skip never did.
func TestResumeIfLoadingInst_ASkipRepliesWithAnError(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.spawn(m.track(3, id, m.resumeIfLoadingInst(ws, inst)))
	out := run(m, m.Drain())
	require.Len(t, out.Events, 1, "only the Reply: no notice")
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.EqualError(t, rs[0].Err, "resume skipped: x is no longer loading (Paused)")
	assert.Equal(t, session.Paused, inst.GetStatus(), "nothing reverted")
}

// TestTracked_ANilResultRepliesSuccess: a job with nothing to report (a
// prompt sent) answers its request with a success: no Err, no Notice.
func TestTracked_ANilResultRepliesSuccess(t *testing.T) {
	m := NewForTest(Options{})
	m.spawn(m.track(3, 9, func() any { return nil }))
	assert.Equal(t, []Reply{{Req: 3, ID: 9}}, replies(run(m, m.Drain())))
}

// TestSendPrompt_RepliesSuccessOnceSent: the send's job finished without
// error, so the Reply has none; it comes only after the job.
func TestSendPrompt_RepliesSuccessOnceSent(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)
	id := m.idOf(inst)

	m.SendPrompt(id, "hi", 6)
	out := m.Drain()
	assert.Empty(t, replies(out), "no reply before the send finishes")
	require.Len(t, out.Jobs, 1)
	assert.Equal(t, []Reply{{Req: 6, ID: id}}, replies(run(m, out)))
}

// TestPush_RepliesWithTheJobsError: an unstarted session has no worktree to
// push (the TUI's gate admits it), so the job's error is the Reply's.
func TestPush_RepliesWithTheJobsError(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	id := m.idOf(inst)

	m.Push(id, 4)
	out := m.Drain()
	assert.Empty(t, replies(out), "no reply before the push finishes")
	rs := replies(run(m, out))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(4), rs[0].Req)
	assert.Equal(t, id, rs[0].ID)
	assert.ErrorContains(t, rs[0].Err, "has not been started")
}

// TestMerge_RepliesWithTheOutcome: a merge of two real worktrees succeeds
// (the branches are level, so git has nothing to do); a source on a branch
// that doesn't exist fails. Both reply naming the target.
func TestMerge_RepliesWithTheOutcome(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	repo := gitRepo(t)
	target := pausedWorktreeInst(t, repo, "target", "loom-target")
	source := pausedWorktreeInst(t, repo, "source", "loom-source")
	ghost, err := session.FromInstanceData(session.InstanceData{Title: "ghost", Status: session.Paused, Branch: "no-such-branch", Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	ws.add(target)
	ws.add(source)
	ws.add(ghost)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	tid := m.idOf(target)

	m.Merge(tid, m.idOf(source), 5)
	assert.Equal(t, []Reply{{Req: 5, ID: tid}}, replies(run(m, m.Drain())))

	m.Merge(tid, m.idOf(ghost), 6)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(6), rs[0].Req)
	assert.Equal(t, tid, rs[0].ID)
	assert.ErrorContains(t, rs[0].Err, "no-such-branch")
}

// TestCreate_WithAnIssueJoinsGitHubState: an issue-born session gets the
// latest poll's state for its issue at once, not at the next poll.
func TestCreate_WithAnIssueJoinsGitHubState(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	repo := t.TempDir()
	m.ghState = map[string]github.Snapshot{repo: {Issues: map[int]github.Issue{7: {Number: 7, Title: "Fix it"}}}}

	m.createWS(ws, NewInstance{Title: "i", Path: repo, Program: "claude", Issue: 7}, 1)
	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	v, ok := m.View(rs[0].ID)
	require.True(t, ok)
	assert.Equal(t, 7, v.Issue)
	assert.Equal(t, github.State{Known: true, IssueNumber: 7, IssueTitle: "Fix it"}, v.GitHub)
}
