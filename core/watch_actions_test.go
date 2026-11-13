package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginWatch_NoClientCanSendIt(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginWatch}, 9)

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Contains(t, rs[0].Err.Error(), "is no sender's origin")
	assert.Empty(t, m.mods, "nothing reaches the mod")
}

func TestOriginWatch_NeedsALiveMod(t *testing.T) {
	assert.True(t, OriginWatch.needsMod())
}

func TestOriginWatch_ANudgeGoesToALiveModAsItsOwnText(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))

	m.enqueue(inst, &modMessage{text: "merge origin/main", origin: OriginWatch})

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "merge origin/main"}, msgs[0], "no From line, and not the person's words")
}

func TestOriginWatch_ANudgeTheModNeverTookIsReportedAndNotPasted(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("offered=%v", offered), func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := modInst(t, m, "x")
			var why string
			m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})
			if offered {
				require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
			}

			m.ModsStop()

			if offered {
				assert.Contains(t, why, "may or may not have reached Claude")
			} else {
				assert.Equal(t, modsStoppedWhy, why)
			}
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

func TestOriginWatch_AProgramWithoutAModIsReportedAtOnce(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := startedInst(t, "x", "aider")
	hold(m, inst)
	var why string

	m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})

	assert.Equal(t, "it runs aider, not Claude", why)
	assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
}

// wactModel is a model serving one workspace "a" with the watcher on and a
// decided tick's state: the title template "heal {issue}", a loaded memory in
// a file of its own, the base main at abc123, and the snapshot's viewer "bot".
func wactModel(t *testing.T, o Options) (*Model, *Workspace, *watchState) {
	t.Helper()
	withMod(t)
	m := NewForTest(o)
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	ws := NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "a", ConfigDir: dir, RepoPath: t.TempDir()}, Storage: storage, Config: config.DefaultConfig(), State: state})
	m.SetWorkspacesForTest(ws)
	wactOn(ws, true)
	tmpl, err := watch.ParseTitleTemplate("heal {issue}")
	require.NoError(t, err)
	st := m.watchFor(ws)
	st.tmpl, st.cfg = tmpl, watch.Config{Label: "drift", EscalateLabel: "needs-human", Title: "heal {issue}", Cap: 2, NudgeLimit: 3, LogTail: 50}
	st.mem, st.memLoaded, st.memPath = watch.Memory{Version: 1}, true, filepath.Join(dir, watch.MemoryFile)
	st.repo, st.base, st.baseSHA, st.startOK, st.checkout = "o/r", "main", "abc123", true, t.TempDir()
	st.snap = &watch.Snapshot{Now: time.Now(), Viewer: "bot"}
	st.inFlight = map[string]bool{}
	return m, ws, st
}

// wactOn switches ws's watcher on or off in its settings.
func wactOn(ws *Workspace, on bool) {
	ws.cfg.Mutate(func(c *config.Config) { c.Settings.Watch = &on })
}

// wactPR is a watcher PR for issue, open or not: its head branch is what the
// title "heal <issue>" makes, its author the snapshot's viewer.
func wactPR(issue, number int, state string) github.WatchPR {
	return github.WatchPR{Number: number, State: state, HeadRefName: git.SanitizeBranchName(fmt.Sprintf("heal %d", issue)), Author: "bot", Closes: []int{issue}, URL: fmt.Sprintf("https://github.test/o/r/pull/%d", number)}
}

// wactPaused is a Paused session in ws linked to issue.
func wactPaused(t *testing.T, ws *Workspace, title string, issue int) *session.Instance {
	t.Helper()
	inst := statusInst(t, title, session.Paused)
	inst.SetIssue(issue)
	ws.add(inst)
	return inst
}

func TestWatchKill_KillsTheWatchersOwnSession(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	inst := wactPaused(t, ws, "heal 7", 7)

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

	assert.Equal(t, session.Deleting, inst.GetStatus())
	assert.Len(t, m.Drain().Jobs, 1, "the kill job")
}

func TestWatchKill_RefusesWhatIsNotItsOwnSession(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		issue     int
		elsewhere bool
	}{
		{"a title that does not parse", "other 7", 7, false},
		{"another issue's title", "heal 8", 7, false},
		{"no link to the issue", "heal 7", 0, false},
		{"another workspace's session", "heal 7", 7, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactModel(t, Options{})
			inst := statusInst(t, c.title, session.Paused)
			inst.SetIssue(c.issue)
			if c.elsewhere {
				other := storedWorkspace(t, "b")
				other.add(inst)
				m.SetWorkspacesForTest(ws, other)
			} else {
				ws.add(inst)
			}

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

			assert.Equal(t, session.Paused, inst.GetStatus(), "nothing was killed")
			assert.Empty(t, m.Drain().Jobs)
			assert.NotEmpty(t, st.actErrs[7], "the refusal is reported")
		})
	}
}

func TestDispatchWatchActions_ActsOnlyOnADecidedTickOfAnOwnedWorkspace(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(ws *Workspace, st *watchState)
	}{
		{"no decided tick", func(_ *Workspace, st *watchState) { st.snap = nil }},
		{"memory not loaded", func(_ *Workspace, st *watchState) { st.memLoaded = false }},
		{"the watcher is off", func(ws *Workspace, _ *watchState) { wactOn(ws, false) }},
		{"no repository", func(ws *Workspace, _ *watchState) { ws.ctx.RepoPath = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactModel(t, Options{})
			inst := wactPaused(t, ws, "heal 7", 7)
			c.spoil(ws, st)

			m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActKill, Issue: 7, Session: uint64(m.idOf(inst))}})

			assert.Equal(t, session.Paused, inst.GetStatus())
			assert.Empty(t, m.Drain().Jobs)
		})
	}
}

func TestWatchBusy_AStartWaitsForAnyStartAndOtherKindsOnlyForTheirOwnKey(t *testing.T) {
	st := &watchState{inFlight: map[string]bool{}}
	start := watch.Action{Kind: watch.ActStart, Issue: 8}
	assert.False(t, watchBusy(st, start))

	st.inFlight[watchActKey(watch.Action{Kind: watch.ActStart, Issue: 7})] = true
	assert.True(t, watchBusy(st, start), "another issue's start is out")

	send := watch.Action{Kind: watch.ActSend, Issue: 8, Session: 3}
	assert.False(t, watchBusy(st, send), "a start in flight blocks only starts")
	st.inFlight[watchActKey(send)] = true
	assert.True(t, watchBusy(st, send))
	assert.False(t, watchBusy(st, watch.Action{Kind: watch.ActSend, Issue: 8, Session: 4}), "another session's nudge")

	closePR := watch.Action{Kind: watch.ActClosePR, Issue: 8, PR: 5}
	kill := watch.Action{Kind: watch.ActKill, Issue: 8, Session: 3}
	assert.NotEqual(t, watchActKey(closePR), watchActKey(kill), "one issue's close and kill don't block each other")
}

func TestDeliverWatchAction_ReleasesTheKeyAndReportsAFailureOrARefusal(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	a := watch.Action{Kind: watch.ActClosePR, Issue: 7, PR: 5}

	st.inFlight["k"] = true
	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, err: errors.New("boom")})
	assert.Empty(t, st.inFlight)
	assert.Contains(t, st.actErrs[7], "boom")

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, refused: "the hook said no"})
	assert.Contains(t, st.actErrs[7], "the hook said no")

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a})
	assert.Empty(t, st.actErrs, "a success clears the kind's error")
}

func TestDeliverWatchAction_ALateResultForAStateNoLongerLiveRecordsNothing(t *testing.T) {
	m, ws, st := wactModel(t, Options{})
	a := watch.Action{Kind: watch.ActClosePR, Issue: 7, PR: 5}
	st.inFlight["k"] = true
	wactOn(ws, false)

	m.Deliver(watchActionResult{ws: ws, st: st, key: "k", act: a, err: errors.New("boom")})

	assert.Empty(t, st.inFlight, "the key is still released")
	assert.Empty(t, st.actErrs)
}
