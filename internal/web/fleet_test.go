package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetView is a started instance in a status; a test sets what else it needs.
func fleetView(id core.InstanceID, title string, status session.Status) core.InstanceView {
	return core.InstanceView{ID: id, Title: title, Status: status, Started: true}
}

func TestGroupOf_EachStatusLandsInItsGroup(t *testing.T) {
	cases := []struct {
		name    string
		status  session.Status
		started bool
		group   string
		label   string
	}{
		{"prompting needs you", session.Prompting, true, "needs_you", "Needs you"},
		{"prompting needs you before it is started too", session.Prompting, false, "needs_you", "Needs you"},
		{"a started ready is your turn", session.Ready, true, "your_turn", "Your turn"},
		{"an unstarted ready is paused", session.Ready, false, "paused", "Not started"},
		{"running is working", session.Running, true, "working", "Working"},
		{"running is working before it is started too", session.Running, false, "working", "Working"},
		{"loading is working", session.Loading, false, "working", "Loading"},
		{"deleting is working", session.Deleting, true, "working", "Deleting"},
		{"paused", session.Paused, true, "paused", "Paused"},
		{"recoverable is paused", session.Recoverable, false, "paused", "Recoverable"},
		{"a status this build does not know is shown as itself", session.Status(99), true, "paused", "Status(99)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := fleetView(1, "t", tc.status)
			v.Started = tc.started
			group, label := groupOf(v)
			assert.Equal(t, tc.group, group)
			assert.Equal(t, tc.label, label)
		})
	}
}

func TestSessionOf_ProjectsEveryFactTheAppShows(t *testing.T) {
	v := core.InstanceView{
		ID:             1234567,
		Title:          "fix login",
		Branch:         "loom/fix-login",
		RepoPath:       "/home/me/src/app",
		WorktreePath:   "/home/me/src/app/.loom/worktrees/fix-login",
		TmuxSession:    "loom_fix-login",
		Program:        "claude",
		Status:         session.Prompting,
		StatusSince:    time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC),
		WaitReason:     "permission",
		WaitDetail:     "rm -rf build",
		LastMessage:    "Should I delete the build dir?",
		HasLastMessage: true,
		Subagents:      []subagent.View{{Name: "explorer", Description: "reads the tree", Idle: true}},
		Diff:           git.DiffStats{Added: 12, Removed: 3, Content: "a long diff body"},
		HasDiff:        true,
		GitHub: github.State{
			Known: true, HasPR: true, PRNumber: 42, PRState: github.PROpen, Checks: github.ChecksFailing,
		},
		Ahead:         2,
		Behind:        5,
		ParityKnown:   true,
		Account:       "work",
		RemoteSession: "session_01AbC",
		Started:       true,
		Bell:          true,
	}

	got, err := json.Marshal(sessionOf(7, v))
	require.NoError(t, err)

	// The worktree and tmux session are in it, for the kill sheet; the repo
	// path, the program and the diff body are not.
	assert.JSONEq(t, `{
		"id": 1234567, "workspace": 7, "title": "fix login", "branch": "loom/fix-login",
		"worktree": "/home/me/src/app/.loom/worktrees/fix-login", "tmux": "loom_fix-login",
		"group": "needs_you", "label": "Needs you", "status": "Prompting",
		"since": "2026-10-21T09:30:00Z",
		"wait": {"reason": "permission", "detail": "rm -rf build"},
		"last_message": "Should I delete the build dir?",
		"diff": {"added": 12, "removed": 3},
		"ahead": 2, "behind": 5, "parity_known": true,
		"pr": {"number": 42, "state": "open", "checks": "failing"},
		"account": "work",
		"agents": [{"name": "explorer", "description": "reads the tree", "idle": true}],
		"remote_url": "https://claude.ai/code/session_01AbC",
		"claude": true
	}`, string(got))
}

func TestSessionOf_WithholdsWhatIsNotKnown(t *testing.T) {
	v := fleetView(2, "idle", session.Ready)
	v.LastMessage = "stale words"                                             // HasLastMessage is false
	v.Diff = git.DiffStats{Added: 9, Removed: 9}                              // HasDiff is false
	v.Ahead, v.Behind = 4, 4                                                  // ParityKnown is false
	v.GitHub = github.State{HasPR: true, PRNumber: 8, PRState: github.PROpen} // Known is false

	s := sessionOf(1, v)

	assert.Empty(t, s.LastMessage)
	assert.Equal(t, diffJSON{}, s.Diff)
	assert.Zero(t, s.Ahead)
	assert.Zero(t, s.Behind)
	assert.False(t, s.ParityKnown)
	assert.Nil(t, s.PR)
	assert.Empty(t, s.Since)
	assert.Empty(t, s.RemoteURL)
	assert.Empty(t, s.Worktree)
	assert.Empty(t, s.Tmux)

	// Empty lists and no PR are `[]` and `null` on the wire, never absent.
	b, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"pr":null`)
	assert.Contains(t, string(b), `"agents":[]`)
	assert.Contains(t, string(b), `"since":""`)
	assert.NotContains(t, string(b), `"worktree"`)
	assert.NotContains(t, string(b), `"tmux"`)
}

func TestSessionOf_ThePRNeedsAKnownPRInAStateWeName(t *testing.T) {
	cases := []struct {
		name  string
		state github.State
		want  bool
	}{
		{"a known PR", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen}, true},
		{"known, with no PR", github.State{Known: true}, false},
		{"a PR in a repo that was not queried", github.State{HasPR: true, PRNumber: 1, PRState: github.PROpen}, false},
		{"a PR in no state", github.State{Known: true, HasPR: true, PRNumber: 1}, false},
		{"a PR in a state this build does not know", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PRState(99)}, false},
		{"checks this build does not know", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.Checks(99)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := prOf(tc.state)
			if tc.want {
				require.NotNil(t, pr)
				assert.Equal(t, 1, pr.Number)
			} else {
				assert.Nil(t, pr)
			}
		})
	}
}

func TestPRWords(t *testing.T) {
	for state, word := range map[github.PRState]string{
		github.PRDraft:  "draft",
		github.PROpen:   "open",
		github.PRMerged: "merged",
		github.PRClosed: "closed",
	} {
		pr := prOf(github.State{Known: true, HasPR: true, PRNumber: 7, PRState: state})
		require.NotNil(t, pr, word)
		assert.Equal(t, word, pr.State)
		assert.Equal(t, "none", pr.Checks)
	}
	for checks, word := range map[github.Checks]string{
		github.ChecksNone:    "none",
		github.ChecksPending: "pending",
		github.ChecksPassing: "passing",
		github.ChecksFailing: "failing",
	} {
		pr := prOf(github.State{Known: true, HasPR: true, PRNumber: 7, PRState: github.PROpen, Checks: checks})
		require.NotNil(t, pr, word)
		assert.Equal(t, word, pr.Checks)
		assert.Equal(t, "open", pr.State)
	}
}

func TestSessionOf_TheRemoteLinkStaysOnClaudesPath(t *testing.T) {
	v := fleetView(1, "t", session.Running)
	v.RemoteSession = "a/b?c#d"
	assert.Equal(t, "https://claude.ai/code/a%2Fb%3Fc%23d", sessionOf(1, v).RemoteURL)

	v.RemoteSession = ""
	assert.Empty(t, sessionOf(1, v).RemoteURL)
}

func TestDeviceOf(t *testing.T) {
	until := time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)
	d := core.WebDevice{
		Name:     "iPhone",
		Paired:   time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		LastSeen: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		Notify:   core.NotifySettings{Waiting: true, PausedUntil: until},
		Push:     core.PushOK,
	}

	got := deviceOf(d)
	assert.Equal(t, deviceJSON{
		Name:   "iPhone",
		Notify: notifyJSON{Waiting: true, Finished: false, PausedUntil: "2026-10-21T11:00:00Z"},
		Push:   "ok",
	}, got)

	// The app's record has no token, no pairing time and no last-seen time.
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name": "iPhone", "notify": {"waiting": true, "finished": false, "paused_until": "2026-10-21T11:00:00Z"}, "push": "ok"}`, string(b))

	d.Notify.PausedUntil = time.Time{}
	d.Push = core.PushFailing(500)
	got = deviceOf(d)
	assert.Equal(t, "", got.Notify.PausedUntil)
	assert.Equal(t, "failing: 500", got.Push)
}

func TestNoticeOf(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	failed := noticeOf(core.Notice{Err: errors.New("push failed")}, at)
	assert.Equal(t, "push failed", failed.Text)
	assert.True(t, failed.Error)
	assert.True(t, failed.At.Equal(at))

	info := noticeOf(core.Notice{Info: "stash kept"}, at)
	assert.Equal(t, "stash kept", info.Text)
	assert.False(t, info.Error)

	b, err := json.Marshal(info)
	require.NoError(t, err)
	assert.JSONEq(t, `{"text": "stash kept", "error": false, "at": "2026-10-21T12:00:00Z"}`, string(b))

	// A time in another zone goes out in UTC, as since and paused_until do.
	local := time.Date(2026, 10, 21, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	b, err = json.Marshal(noticeOf(core.Notice{Info: "x"}, local))
	require.NoError(t, err)
	assert.JSONEq(t, `{"text": "x", "error": false, "at": "2026-10-21T12:00:00Z"}`, string(b))
}

func TestWorkspaceOf(t *testing.T) {
	b, err := json.Marshal(workspaceOf(core.WorkspaceView{ID: 3, Name: "loom", Label: "loom", RepoPath: "/home/me/loom", ConfigDir: "/home/me/loom/.loom"}))
	require.NoError(t, err)
	assert.JSONEq(t, `{"id": 3, "name": "loom", "label": "loom", "shown": false}`, string(b), "shown is the projection's to set (layout)")

	global := workspaceOf(core.WorkspaceView{ID: 4, Label: "global"})
	assert.Equal(t, "", global.Name)
	assert.Equal(t, "global", global.Label)
}

func TestSnapshotJSON_HasTheContractsKeys(t *testing.T) {
	b, err := json.Marshal(snapshotJSON{
		Workspaces:   []workspaceJSON{},
		Sessions:     map[core.WorkspaceID][]sessionJSON{},
		Notices:      []noticeJSON{},
		Usage:        []usageJSON{},
		QuickReplies: []string{},
	})
	require.NoError(t, err)

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &keys))
	var got []string
	for k := range keys {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{"build", "host", "workspaces", "sessions", "device", "badge", "notices", "usage", "usage_stale_after_s", "quick_replies"}, got)
}

func TestProjection_KeepsTheModelsOrderAndLeavesWorkspaceTerminalsOut(t *testing.T) {
	p := newProjection()
	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Name: "a", Label: "a"}})
	terminal := fleetView(12, "terminal", session.Ready)
	terminal.IsWorkspaceTerminal = true

	ok := p.setViews(1, []core.InstanceView{
		fleetView(11, "listed first", session.Running),
		terminal,
		fleetView(10, "listed second, with the lower ID", session.Prompting),
	})

	require.True(t, ok)
	require.Len(t, p.sessions[1], 2)
	assert.Equal(t, core.InstanceID(11), p.sessions[1][0].ID)
	assert.Equal(t, core.InstanceID(10), p.sessions[1][1].ID)
	assert.Equal(t, core.WorkspaceID(1), p.sessions[1][0].Workspace)
}

func TestProjection_IgnoresViewsOfAWorkspaceItDoesNotServe(t *testing.T) {
	p := newProjection()
	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Label: "a"}})

	ok := p.setViews(9, []core.InstanceView{fleetView(1, "x", session.Running)})

	assert.False(t, ok)
	assert.NotContains(t, p.sessions, core.WorkspaceID(9))
}

func TestProjection_ANewWorkspaceStartsEmptyAndAGoneOneLosesItsSessions(t *testing.T) {
	p := newProjection()
	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Label: "a"}, {ID: 2, Label: "b"}})
	require.True(t, p.setViews(1, []core.InstanceView{fleetView(10, "kept", session.Running)}))
	require.True(t, p.setViews(2, []core.InstanceView{fleetView(20, "lost", session.Running)}))

	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Label: "a"}, {ID: 3, Label: "c"}})

	require.Len(t, p.workspaces, 2)
	assert.Equal(t, core.WorkspaceID(3), p.workspaces[1].ID)
	require.Len(t, p.sessions[1], 1, "a workspace that stays keeps its sessions")
	assert.Equal(t, "kept", p.sessions[1][0].Title)
	assert.NotNil(t, p.sessions[3], "a new workspace has an empty list, not none")
	assert.Empty(t, p.sessions[3])
	assert.NotContains(t, p.sessions, core.WorkspaceID(2))
	assert.True(t, p.served(1))
	assert.True(t, p.served(3))
	assert.False(t, p.served(2))
	assert.False(t, p.served(9))
}

func TestProjection_TheBadgeCountsWhatNeedsYouAcrossTheWorkspaces(t *testing.T) {
	p := newProjection()
	assert.Zero(t, p.badge(), "an empty fleet needs no one")
	p.setWorkspaces([]core.WorkspaceView{{ID: 1, Label: "a"}, {ID: 2, Label: "b"}})
	terminal := fleetView(14, "terminal", session.Prompting)
	terminal.IsWorkspaceTerminal = true
	require.True(t, p.setViews(1, []core.InstanceView{
		fleetView(10, "asks", session.Prompting),
		fleetView(11, "works", session.Running),
		fleetView(12, "idle", session.Ready),
		terminal,
	}))
	require.True(t, p.setViews(2, []core.InstanceView{
		fleetView(20, "also asks", session.Prompting),
		fleetView(21, "paused", session.Paused),
	}))

	assert.Equal(t, 2, p.badge(), "the terminal is not a session of the fleet")

	require.True(t, p.setViews(1, []core.InstanceView{fleetView(10, "answered", session.Running)}))
	assert.Equal(t, 1, p.badge())
}

// shownOf is the projection's workspaces as "label:shown", in its order.
func shownOf(p *projection) []string {
	var out []string
	for _, w := range p.workspaces {
		out = append(out, fmt.Sprintf("%s:%v", w.Label, w.Shown))
	}
	return out
}

// projectionOf is a projection serving ws in that order, with the views each
// workspace holds and the open list open.
func projectionOf(t *testing.T, ws []core.WorkspaceView, views map[core.WorkspaceID][]core.InstanceView, open ...string) *projection {
	t.Helper()
	p := newProjection()
	p.setWorkspaces(ws)
	for id, list := range views {
		require.True(t, p.setViews(id, list))
	}
	p.setOpen(open)
	return p
}

func TestProjection_ShowsTheOpenTabsAndTheWorkspacesWithALiveSession(t *testing.T) {
	ws := []core.WorkspaceView{
		{ID: 1, Label: "global"},
		{ID: 2, Name: "loom", Label: "loom"},
		{ID: 3, Name: "flow", Label: "flow"},
		{ID: 4, Name: "system", Label: "system"},
		{ID: 5, Name: "idle", Label: "idle"},
	}
	views := map[core.WorkspaceID][]core.InstanceView{
		2: {fleetView(20, "paused", session.Paused)},
		3: {fleetView(30, "works", session.Running), fleetView(31, "paused", session.Paused)},
		5: {fleetView(50, "paused", session.Paused), fleetView(51, "recoverable", session.Recoverable)},
	}

	p := projectionOf(t, ws, views, "system", "loom")

	assert.Equal(t, []string{"system:true", "loom:true", "global:false", "flow:true", "idle:false"}, shownOf(p),
		"the tabs first in tab order, shown with no session or only paused ones; then the rest in serve order, shown while a session isn't paused")
}

func TestProjection_WithNoTabOpenEveryWorkspaceIsShown(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 1, Label: "global"}, {ID: 2, Name: "loom", Label: "loom"}}

	p := projectionOf(t, ws, nil)
	assert.Equal(t, []string{"global:true", "loom:true"}, shownOf(p), "global mode, or a list never saved")

	assert.True(t, p.setOpen([]string{"gone"}))
	assert.Equal(t, []string{"global:true", "loom:true"}, shownOf(p), "a list that names no served workspace is no list")
}

func TestProjection_TheGlobalWorkspaceIsShownOnlyForALiveSession(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 1, Label: "global"}, {ID: 2, Name: "loom", Label: "loom"}}
	p := projectionOf(t, ws, map[core.WorkspaceID][]core.InstanceView{1: {fleetView(10, "asks", session.Prompting)}}, "", "loom")
	assert.Equal(t, []string{"loom:true", "global:true"}, shownOf(p), "shown for its waiting session; a tab named \"\" is not it")

	require.True(t, p.setViews(1, []core.InstanceView{fleetView(10, "asks", session.Paused)}))
	assert.Equal(t, []string{"loom:true", "global:false"}, shownOf(p))
}

func TestProjection_AClosedWorkspaceHidesWhenItsLastLiveSessionPauses(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "flow", Label: "flow"}}
	p := projectionOf(t, ws, map[core.WorkspaceID][]core.InstanceView{3: {fleetView(30, "works", session.Running)}}, "loom")
	require.Equal(t, []string{"loom:true", "flow:true"}, shownOf(p))

	require.True(t, p.setViews(3, []core.InstanceView{fleetView(30, "works", session.Paused)}))

	assert.Equal(t, []string{"loom:true", "flow:false"}, shownOf(p))
	require.Len(t, p.sessions[3], 1, "its sessions stay in the snapshot: a link still opens them")
}

func TestProjection_ARenamedTabKeepsItsPlace(t *testing.T) {
	p := projectionOf(t, []core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "flow", Label: "flow"}}, nil, "flow", "loom")
	require.Equal(t, []string{"flow:true", "loom:true"}, shownOf(p))

	// RenameWorkspace renames the served workspace and the open list together.
	p.setWorkspaces([]core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "river", Label: "river"}})
	require.True(t, p.setOpen([]string{"river", "loom"}))

	assert.Equal(t, []string{"river:true", "loom:true"}, shownOf(p))
	assert.False(t, p.setOpen([]string{"river", "loom"}), "the same list again changes nothing")
}

func TestUsageOf_FollowsTheRegistryAndLeavesOutWhatHasNoLimits(t *testing.T) {
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	resets := time.Date(2026, 10, 23, 12, 40, 0, 0, time.UTC)
	v := core.AccountsView{
		Names: core.AccountNames{Present: true, Default: "default", Names: []string{"default", "work", "api", "new"}},
		Usage: map[string]account.Usage{
			"default": {Available: true, Plan: "max", FiveHour: &account.Window{Pct: 62, ResetsAt: resets}, SevenDay: &account.Window{Pct: 38}, At: at},
			"work":    {Available: true, Plan: "team", SevenDay: &account.Window{Pct: 71, ResetsAt: resets}, At: at},
			"api":     {Available: false, At: at},
			"ghost":   {Available: true, Plan: "pro", At: at},
		},
	}

	b, err := json.Marshal(usageOf(v))

	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"account": "default", "plan": "max", "five_hour": {"pct": 62, "resets_at": "2026-10-23T12:40:00Z"}, "seven_day": {"pct": 38, "resets_at": ""}, "at": "2026-10-23T07:00:00Z"},
		{"account": "work", "plan": "team", "five_hour": null, "seven_day": {"pct": 71, "resets_at": "2026-10-23T12:40:00Z"}, "at": "2026-10-23T07:00:00Z"}
	]`, string(b), "no plan limits (api), no sample yet (new) and no registered name (ghost) are left out")
	assert.NotNil(t, usageOf(core.AccountsView{}), "an empty list, never null")
}

func TestPrOf_KeepsTheURLOnlyWhenItIsHTTPS(t *testing.T) {
	base := github.State{Known: true, HasPR: true, PRNumber: 318, PRState: github.PROpen, Checks: github.ChecksPassing}
	for _, tc := range []struct{ url, want string }{
		{"https://github.com/o/r/pull/318", "https://github.com/o/r/pull/318"},
		{"http://github.com/o/r/pull/318", ""},
		{"javascript:void(0)", ""},
		{"/o/r/pull/318", ""},
		{"", ""},
	} {
		s := base
		s.PRURL = tc.url
		pr := prOf(s)
		require.NotNil(t, pr, tc.url)
		assert.Equal(t, tc.want, pr.URL, tc.url)
	}

	b, err := json.Marshal(prOf(base))
	require.NoError(t, err)
	assert.JSONEq(t, `{"number": 318, "state": "open", "checks": "passing"}`, string(b), "no URL, no key")
}

func TestResolvedQuickReplies(t *testing.T) {
	assert.Equal(t, core.DefaultQuickReplies(), resolvedQuickReplies(nil), "unset: the defaults")
	none := resolvedQuickReplies([]string{})
	assert.NotNil(t, none, "none: an empty list, never null")
	assert.Empty(t, none)
	assert.Equal(t, []string{"ship it"}, resolvedQuickReplies([]string{"ship it"}))
}
