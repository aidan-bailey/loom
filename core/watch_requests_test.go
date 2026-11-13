package core

import (
	"encoding/json"
	"fmt"
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
