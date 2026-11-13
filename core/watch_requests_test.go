package core

import (
	"encoding/json"
	"fmt"
	"os"
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

// TestReply_TheWatchViewCrossesTheWire: a Reply has a wire form of its own
// (replyJSON), so a field it does not list is one every client reads as
// zero.
func TestReply_TheWatchViewCrossesTheWire(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	in := Reply{Req: 3, Watch: WatchView{
		On: true, Err: "no loom-watch.json on origin/main", Viewer: "bot", Considered: 4, Unconsidered: 1,
		Issues: []WatchIssueView{{
			Number: 12, State: "open", Session: "heal 12", PR: 30,
			Waiting: "a foreign session holds it", Failed: "start: the hook said no", Nudges: map[string]int{"ci": 2}, Escalated: true,
		}},
		Actions:     []watch.Action{{Kind: watch.ActWait, Issue: 12, Reason: "a foreign session holds it"}},
		Escalations: []WatchEscalation{{Issue: 12, Reason: "stuck", At: at}},
	}}

	data, err := json.Marshal(in)
	require.NoError(t, err)
	for _, key := range []string{`"on":true`, `"considered":4`, `"unconsidered":1`, `"escalations"`, `"waiting"`, `"failed":"start: the hook said no"`, `"escalated":true`, `"issue":12`} {
		assert.Contains(t, string(data), key, "the views' keys are snake_case")
	}

	var out Reply
	require.NoError(t, json.Unmarshal(data, &out))
	assert.EqualValues(t, 3, out.Req)
	assert.Equal(t, in.Watch, out.Watch)
}

// watchWS is a served workspace over a repository dir and a config dir
// (the global workspace has neither, so it cannot be watched), its
// watcher switched on or off.
func watchWS(t *testing.T, on bool) (*Model, *Workspace, WorkspaceID) {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	if on {
		cfg.Watch = &on
	}
	ws := NewWorkspace(WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: "w", RepoPath: t.TempDir(), ConfigDir: dir},
		Storage: storage, Config: cfg, State: state,
	})
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	return m, ws, m.wsIDOf(ws)
}

// watchBranch is the head branch of the watcher's PR for issue n under the
// title template "heal {issue}" and the branch prefix "loom/", built with
// the real sanitizer the template's branch pattern uses.
func watchBranch(n int) string { return git.SanitizeBranchName(fmt.Sprintf("loom/heal %d", n)) }

// watchEscalatedMemory holds 27 escalated issues (1 to 25 one minute
// apart, 28 and 30 at issue 25's time) and an issue that is not escalated.
func watchEscalatedMemory() watch.Memory {
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	mem := watch.Memory{Version: 1, Issues: map[int]watch.IssueMemory{}}
	for n := 1; n <= 25; n++ {
		mem.Issues[n] = watch.IssueMemory{Escalated: &watch.Escalation{At: base.Add(time.Duration(n) * time.Minute), Reason: fmt.Sprintf("r%d", n)}}
	}
	for _, n := range []int{28, 30} {
		mem.Issues[n] = watch.IssueMemory{Escalated: &watch.Escalation{At: base.Add(25 * time.Minute), Reason: fmt.Sprintf("r%d", n)}}
	}
	mem.Issues[40] = watch.IssueMemory{Seen: "h:1"}
	return mem
}

// watchEscalationOrder is the issue numbers of watchEscalatedMemory's
// escalations, newest first, a tie by issue number.
func watchEscalationOrder() []int {
	order := []int{25, 28, 30}
	for n := 24; n >= 1; n-- {
		order = append(order, n)
	}
	return order
}

func watchIssueNumbers(es []WatchEscalation) []int {
	var out []int
	for _, e := range es {
		out = append(out, e.Issue)
	}
	return out
}

// TestWatchViewOf_ListsEachIssueWithItsSessionPRAndWait: one row per issue
// the last tick ruled on or a watcher session or PR names, in number
// order, each with only what the watcher owns.
func TestWatchViewOf_ListsEachIssueWithItsSessionPRAndWait(t *testing.T) {
	m, ws, _ := watchWS(t, true)
	st := m.watchFor(ws)
	tmpl, err := watch.ParseTitleTemplate("heal {issue}")
	require.NoError(t, err)
	st.tmpl, st.viewer = tmpl, "bot"
	st.snap = &watch.Snapshot{
		Viewer: "bot", Prefix: "loom/", Considered: 5, Unconsidered: 1,
		Issues: []github.WatchIssue{{Number: 17}, {Number: 12}, {Number: 13, Closed: true}, {Number: 15}, {Number: 16}},
		PRs: []github.WatchPR{
			{Number: 30, State: "OPEN", HeadRefName: watchBranch(12), Author: "bot", Closes: []int{12}},
			{Number: 31, State: "MERGED", HeadRefName: watchBranch(13), Author: "bot", Closes: []int{13}},
			{Number: 32, State: "OPEN", HeadRefName: watchBranch(17), Author: "someone", Closes: []int{17}},
		},
		Sessions: []watch.Session{
			{ID: 7, Title: "heal 12", Status: "Ready", Issue: 12},
			{ID: 9, Title: "heal 14", Status: "Running", Issue: 14},
			{ID: 10, Title: "scratch", Status: "Ready", Issue: 17},
		},
		HookFailed: map[int]string{16: "hook: exit status 1", 17: "hook: exit status 1"},
	}
	st.mem = watch.Memory{
		Version: 1,
		PRs:     map[int]watch.PRMemory{30: {Nudges: map[string]watch.NudgeRecord{"ci": {N: 2}, "conflict": {N: 1}}}},
		Issues:  map[int]watch.IssueMemory{16: {Escalated: &watch.Escalation{Reason: "stuck"}}},
	}
	actions := []watch.Action{
		{Kind: watch.ActWait, Issue: 15, Reason: "a foreign session holds it"},
		{Kind: watch.ActWait, Issue: 16, Reason: "the cap is full"},
	}

	v := m.watchViewOf(ws, actions)

	assert.True(t, v.On)
	assert.Equal(t, "bot", v.Viewer)
	assert.Equal(t, 5, v.Considered)
	assert.Equal(t, 1, v.Unconsidered)
	assert.Equal(t, actions, v.Actions)
	assert.Equal(t, []WatchIssueView{
		{Number: 12, State: "open", Session: "heal 12", PR: 30, Nudges: map[string]int{"ci": 2, "conflict": 1}},
		{Number: 13, State: "closed"},
		{Number: 14, State: "unknown", Session: "heal 14"},
		{Number: 15, State: "open", Waiting: "a foreign session holds it"},
		{Number: 16, State: "open", Waiting: "the cap is full", Escalated: true},
		{Number: 17, State: "open", Waiting: "hook: exit status 1"},
	}, v.Issues)
}

// TestWatchViewOf_BeforeAnyTickListsOnlyWhatTheActionsName: a dry run
// before the watcher's first decided tick has no snapshot; its actions'
// issues are listed as unknown and the counts are zero.
func TestWatchViewOf_BeforeAnyTickListsOnlyWhatTheActionsName(t *testing.T) {
	m, ws, _ := watchWS(t, true)
	st := m.watchFor(ws)
	st.viewer = "bot"
	actions := []watch.Action{{Kind: watch.ActStart, Issue: 7}, {Kind: watch.ActWait, Issue: 4, Reason: "the cap is full"}}

	v := m.watchViewOf(ws, actions)

	assert.True(t, v.On)
	assert.Equal(t, "bot", v.Viewer)
	assert.Zero(t, v.Considered)
	assert.Equal(t, []WatchIssueView{
		{Number: 4, State: "unknown", Waiting: "the cap is full"},
		{Number: 7, State: "unknown"},
	}, v.Issues)
	assert.Equal(t, actions, v.Actions)
}

// TestWatchViewOf_AnOffWatcherIsJustOff: a workspace whose switch is off
// answers On false and nothing else, whatever state an earlier run left.
func TestWatchViewOf_AnOffWatcherIsJustOff(t *testing.T) {
	m, ws, _ := watchWS(t, false)
	st := m.watchFor(ws)
	st.err, st.viewer, st.mem = "no loom-watch.json on origin/main", "bot", watchEscalatedMemory()

	assert.Equal(t, WatchView{}, m.watchViewOf(ws, []watch.Action{{Kind: watch.ActWait, Issue: 1}}))
}

// TestWatchViewOf_TheErrAndTheEscalationsAreTheStates: the tick's error
// text is the view's, and every escalated issue is listed, newest first, a
// tie broken by issue number.
func TestWatchViewOf_TheErrAndTheEscalationsAreTheStates(t *testing.T) {
	m, ws, _ := watchWS(t, true)
	st := m.watchFor(ws)
	st.err, st.mem = "memory: watch.json: version 9 is newer than this loom understands", watchEscalatedMemory()

	v := m.watchViewOf(ws, nil)

	assert.Equal(t, st.err, v.Err)
	assert.Len(t, v.Escalations, 27, "the view's list is whole; an issue that is not escalated is not in it")
	assert.Equal(t, watchEscalationOrder(), watchIssueNumbers(v.Escalations))
	assert.Equal(t, "r25", v.Escalations[0].Reason)
	assert.Equal(t, st.mem.Issues[25].Escalated.At, v.Escalations[0].At)
}

// TestWatchViewOf_AFailedActionShowsOnItsIssue: an issue's last failed or
// refused action (watchState.actErrs) is its Failed text, apart from its
// wait reason, and an issue only an error names is listed.
func TestWatchViewOf_AFailedActionShowsOnItsIssue(t *testing.T) {
	m, ws, _ := watchWS(t, true)
	st := m.watchFor(ws)
	st.snap = &watch.Snapshot{Viewer: "bot", Issues: []github.WatchIssue{{Number: 12}, {Number: 15}}}
	st.actErrs = map[int]string{12: "start: the hook said no", 30: "kill: boom"}
	actions := []watch.Action{{Kind: watch.ActWait, Issue: 15, Reason: "the cap is full"}}

	v := m.watchViewOf(ws, actions)

	assert.Equal(t, []WatchIssueView{
		{Number: 12, State: "open", Failed: "start: the hook said no"},
		{Number: 15, State: "open", Waiting: "the cap is full"},
		{Number: 30, State: "unknown", Failed: "kill: boom"},
	}, v.Issues)
}

// TestWorkspaceView_CarriesTheWatchSummary: the summary is the switch,
// whether memory is loaded, the last tick's error and the 20 newest
// escalations, and it is empty while the switch is off.
func TestWorkspaceView_CarriesTheWatchSummary(t *testing.T) {
	m, ws, id := watchWS(t, true)
	st := m.watchFor(ws)
	st.err, st.mem = "no loom-watch.json on origin/main", watchEscalatedMemory()

	v, ok := m.Workspace(id)
	require.True(t, ok)
	assert.True(t, v.Watch.On)
	assert.False(t, v.Watch.Loaded, "memory is not loaded until a poll loads it, whatever it holds")
	assert.Equal(t, "no loom-watch.json on origin/main", v.Watch.Err)
	require.Len(t, v.Watch.Escalations, 20, "the newest 20 of 27")
	assert.Equal(t, watchEscalationOrder()[:20], watchIssueNumbers(v.Watch.Escalations))
	assert.True(t, v.Settings.WatchEnabled(), "and the switch is in the settings copy too")

	st.memLoaded = true
	v, _ = m.Workspace(id)
	assert.True(t, v.Watch.Loaded, "a loaded memory shows as loaded")

	off := false
	ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })
	v, _ = m.Workspace(id)
	assert.Equal(t, WatchSummary{}, v.Watch, "off shows nothing, whatever the state holds")
}

// TestWorkspaceView_AnEscalationRepublishesTheWorkspaces: a new escalation
// in memory reaches clients as a WorkspacesChanged, and nothing changing
// publishes nothing.
func TestWorkspaceView_AnEscalationRepublishesTheWorkspaces(t *testing.T) {
	m, ws, _ := watchWS(t, true)
	st := m.watchFor(ws)
	m.syncEvents()
	changed := func(events []Event) (WorkspacesChanged, bool) {
		for _, e := range events {
			if wc, ok := e.(WorkspacesChanged); ok {
				return wc, true
			}
		}
		return WorkspacesChanged{}, false
	}
	_, again := changed(m.syncEvents())
	require.False(t, again, "fixture: nothing changed, nothing published")

	st.mem.Issues = map[int]watch.IssueMemory{12: {Escalated: &watch.Escalation{At: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC), Reason: "stuck"}}}

	wc, ok := changed(m.syncEvents())
	require.True(t, ok, "the new escalation is published")
	require.Len(t, wc.Views, 1)
	assert.Equal(t, []WatchEscalation{{Issue: 12, Reason: "stuck", At: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}}, wc.Views[0].Watch.Escalations)
}

// TestAnswerWatchDry: a dry run's poll answers its requester with the view
// of the actions it decided, or refuses; it records nothing and takes
// none.
func TestAnswerWatchDry(t *testing.T) {
	t.Run("the dry actions are the view's, the stored ones stay", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		st := m.watchFor(ws)
		stored := []watch.Action{{Kind: watch.ActKill, Issue: 1, Session: 5}}
		st.actions = stored
		dry := []watch.Action{{Kind: watch.ActStart, Issue: 9}}

		m.answerWatchDry(watchResult{ws: ws, dry: true, req: 6}, dry, "")

		r := requestReply(t, m, 6)
		require.NoError(t, r.Err)
		assert.True(t, r.Watch.On)
		assert.Equal(t, dry, r.Watch.Actions)
		assert.Equal(t, stored, st.actions, "a dry run stores nothing")
	})
	t.Run("a failed poll refuses with its reason", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		m.watchFor(ws)

		m.answerWatchDry(watchResult{ws: ws, dry: true, req: 6}, nil, "no loom-watch.json on origin/main")

		r := requestReply(t, m, 6)
		require.Error(t, r.Err)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "no loom-watch.json on origin/main")
	})
	t.Run("a watcher switched off meanwhile refuses", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		m.watchFor(ws)
		off := false
		ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })

		m.answerWatchDry(watchResult{ws: ws, dry: true, req: 6}, nil, "")

		r := requestReply(t, m, 6)
		require.Error(t, r.Err)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "switched off")
	})
}

// TestDeliverWatch_ADryResultAnswersItsRequester: a dry result reaching
// deliverWatch is answered, and stores nothing.
func TestDeliverWatch_ADryResultAnswersItsRequester(t *testing.T) {
	t.Run("a failed poll is refused with its reason", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		st := m.watchFor(ws)
		st.polling = true

		m.Deliver(watchResult{ws: ws, dry: true, req: 4, err: "no loom-watch.json on origin/main"})

		r := requestReply(t, m, 4)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "no loom-watch.json on origin/main")
		assert.False(t, st.polling)
		assert.Empty(t, st.err, "a dry run stores nothing")
	})
	t.Run("a decided poll is answered with the view", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		st := m.watchFor(ws)
		st.polling = true
		tmpl, err := watch.ParseTitleTemplate("heal {issue}")
		require.NoError(t, err)

		m.Deliver(watchResult{ws: ws, dry: true, req: 4, startOK: true, tmpl: tmpl})

		r := requestReply(t, m, 4)
		require.NoError(t, r.Err)
		assert.True(t, r.Watch.On)
		assert.Nil(t, st.snap, "a dry run records no tick")
	})
	t.Run("a workspace the model no longer serves is refused, not left waiting", func(t *testing.T) {
		m, ws, _ := watchWS(t, true)
		m.watchFor(ws).polling = true
		m.SetWorkspacesForTest()

		m.Deliver(watchResult{ws: ws, dry: true, req: 4, startOK: true})

		r := requestReply(t, m, 4)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "switched off")
	})
}

// TestSetWatch_WritesTheSwitchToTheWorkspaceConfig: the switch lands in the
// model's config and in the workspace's config.json, the published summary
// follows, and switching on starts a poll at once.
func TestSetWatch_WritesTheSwitchToTheWorkspaceConfig(t *testing.T) {
	m, ws, id := watchWS(t, false)
	require.False(t, ws.cfg.GetWatchEnabled(), "fixture: off")

	m.gate(gateWatch).last = time.Now() // the gate is not due: only the expedite makes the poll start
	m.SetWatch(id, true, 1)

	require.NoError(t, requestReply(t, m, 1).Err)
	assert.True(t, ws.cfg.GetWatchEnabled())
	require.NotNil(t, ws.watch, "a switch-on polls at once")
	assert.True(t, ws.watch.polling, "a switch-on polls at once, not at the next cadence")
	assert.True(t, config.LoadConfigFrom(ws.ctx.ConfigDir).WatchEnabled(), "and the file")
	v, _ := m.Workspace(id)
	assert.True(t, v.Watch.On)

	ws.watch.polling = false
	m.SetWatch(id, false, 2)

	require.NoError(t, requestReply(t, m, 2).Err)
	assert.False(t, ws.watch.polling, "switching off starts no poll")
	assert.False(t, ws.cfg.GetWatchEnabled())
	assert.False(t, config.LoadConfigFrom(ws.ctx.ConfigDir).WatchEnabled(), "off is written too, not left unset")
}

// TestSetWatch_RefusesWhatItCannotSwitch: an unserved workspace, a
// workspace with no repository and a save that fails each leave the switch
// as it was and answer with a refusal.
func TestSetWatch_RefusesWhatItCannotSwitch(t *testing.T) {
	t.Run("an unserved workspace", func(t *testing.T) {
		m, _, _ := watchWS(t, false)

		m.SetWatch(99, true, 1)

		r := requestReply(t, m, 1)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "not served")
	})
	t.Run("a workspace with no repository", func(t *testing.T) {
		m := NewForTest(Options{})
		ws := storedWorkspace(t, "global")
		m.SetWorkspacesForTest(ws)

		m.SetWatch(m.wsIDOf(ws), true, 1)

		r := requestReply(t, m, 1)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.Contains(t, r.Err.Error(), "no repository")
		assert.False(t, ws.cfg.GetWatchEnabled())
	})
	t.Run("a save that fails puts the switch back", func(t *testing.T) {
		m, ws, id := watchWS(t, false)
		require.NoError(t, os.Mkdir(filepath.Join(ws.ctx.ConfigDir, config.ConfigFileName), 0o755), "fixture: the file's path is a directory, so the write fails")

		m.SetWatch(id, true, 1)

		r := requestReply(t, m, 1)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.False(t, ws.cfg.GetWatchEnabled(), "the in-memory switch is the file's again")
	})
}

// TestReadWatch_StatusAnswersTheLastTickAndPollsNothing: a status read is
// answered at once from the state's last actions, with no poll started.
func TestReadWatch_StatusAnswersTheLastTickAndPollsNothing(t *testing.T) {
	m, ws, id := watchWS(t, true)
	st := m.watchFor(ws)
	st.viewer = "bot"
	st.actions = []watch.Action{{Kind: watch.ActWait, Issue: 3, Reason: "the cap is full"}}

	m.ReadWatch(id, false, 1)

	r := requestReply(t, m, 1)
	require.NoError(t, r.Err)
	assert.True(t, r.Watch.On)
	assert.Equal(t, "bot", r.Watch.Viewer)
	assert.Equal(t, st.actions, r.Watch.Actions)
	assert.Equal(t, []WatchIssueView{{Number: 3, State: "unknown", Waiting: "the cap is full"}}, r.Watch.Issues)
	assert.False(t, st.polling, "a status read starts no poll")
}

// TestReadWatch_AnOffWorkspaceAnswersOffAndPollsNothing: with the switch
// off both a status read and a dry run answer On false at once, and no
// watcher state is made.
func TestReadWatch_AnOffWorkspaceAnswersOffAndPollsNothing(t *testing.T) {
	m, ws, id := watchWS(t, false)

	m.ReadWatch(id, false, 1)
	m.ReadWatch(id, true, 2)

	events := m.Drain().Events // requestReply drains, so both replies are read from one drain
	for _, req := range []ReqID{1, 2} {
		r := replyTo(events, req)
		require.NotNil(t, r, "a Reply to request %d", req)
		require.NoError(t, r.Err, "request %d", req)
		assert.Equal(t, WatchView{}, r.Watch, "request %d", req)
	}
	assert.Nil(t, ws.watch, "no poll was started, so no state was made")
}

// TestReadWatch_ADryRunWaitsForItsPollAndIsRefusedWhileOneIsOut: a dry run
// starts a poll and answers when it is delivered, so no Reply comes at
// once; a second dry run while it is out is refused.
func TestReadWatch_ADryRunWaitsForItsPollAndIsRefusedWhileOneIsOut(t *testing.T) {
	m, ws, id := watchWS(t, true)

	m.ReadWatch(id, true, 5)

	assert.Nil(t, replyTo(m.Drain().Events, 5), "the poll answers it, not the request")
	assert.True(t, m.watchFor(ws).polling, "a dry run starts a poll")

	m.ReadWatch(id, true, 6)

	r := requestReply(t, m, 6)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Contains(t, r.Err.Error(), "already running")
}

// TestReadWatch_RefusesAnUnservedWorkspace: the request names a workspace
// the model does not serve.
func TestReadWatch_RefusesAnUnservedWorkspace(t *testing.T) {
	m, _, _ := watchWS(t, true)

	m.ReadWatch(99, false, 1)

	assert.ErrorIs(t, requestReply(t, m, 1).Err, ErrRefused)
}
