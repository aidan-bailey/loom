package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stateEvents picks the model, account and GitHub events out of events.
func stateEvents(events []Event) (model, accounts, gh int) {
	for _, ev := range events {
		switch ev.(type) {
		case ModelChanged:
			model++
		case AccountsChanged:
			accounts++
		case GitHubChanged:
			gh++
		}
	}
	return
}

// TestPublishState_OnceThenOnChange: the first Sync publishes the model,
// account and GitHub views; later ones publish only a view that changed.
func TestPublishState_OnceThenOnChange(t *testing.T) {
	m := NewForTest(Options{Program: "aider"})
	model, accounts, gh := stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{1, 1, 1}, [3]int{model, accounts, gh}, "each published the first time")
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{model, accounts, gh}, "nothing changed")

	m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, 1, model, "the default account's auth is the model's state")
	assert.Equal(t, 1, accounts, "and the account view's too")
	assert.Zero(t, gh)

	m.Deliver(GitHubResultForTest(true, "", map[string]github.Snapshot{"/r": {}}, nil))
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 0, 1}, [3]int{model, accounts, gh}, "a poll republishes the GitHub view")

	m.Deliver(UsageResultForTest(nil, map[string]error{account.DefaultName: errors.New("probe failed")}))
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 1, 0}, [3]int{model, accounts, gh}, "a usage probe republishes the account view")
}

// TestPublishState_EveryUsageRoundRepublishes: a usage probe round
// reaches the TUI even when it changed nothing the account view holds. The
// TUI renders a sample's age when it refreshes its account views, so a
// probe failing the same way again must still refresh them.
func TestPublishState_EveryUsageRoundRepublishes(t *testing.T) {
	m := NewForTest(Options{})
	m.Sync()
	for round := 1; round <= 2; round++ {
		m.Deliver(UsageResultForTest(nil, map[string]error{account.DefaultName: errors.New("x")}))
		model, accounts, gh := stateEvents(m.Sync().Events)
		assert.Equal(t, [3]int{0, 1, 0}, [3]int{model, accounts, gh}, "round %d republishes the account view", round)
	}
	_, accounts, _ := stateEvents(m.Sync().Events)
	assert.Zero(t, accounts, "and nothing republishes it between rounds")
}

// TestPublishState_CarriesTheState: each event carries the view a replica
// answers from.
func TestPublishState_CarriesTheState(t *testing.T) {
	m := NewForTest(Options{})
	m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
	m.Deliver(GitHubResultForTest(false, "gh is not logged in", nil, map[string]error{"/r": errors.New("boom")}))
	for _, ev := range m.Sync().Events {
		switch ev := ev.(type) {
		case ModelChanged:
			assert.Equal(t, "not logged in", ev.View.RCAuth.Reason)
		case GitHubChanged:
			assert.True(t, ev.View.Unavailable)
			assert.Equal(t, "gh is not logged in", ev.View.Reason)
			assert.Equal(t, map[string]string{"/r": "boom"}, ev.View.Errs)
		}
	}
}

// TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone: Snapshot carries
// every published view, what changed since the last Sync included, and the
// next Sync still publishes what changed since the last Sync.
func TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.add(pausedInst(t, "x"))
	m.SetWorkspacesForTest(ws)
	m.Sync()
	m.SetRCAuth(session.RemoteControlAuth{Reason: "codex"})

	snap := m.Snapshot()
	require.Len(t, snap, 5)
	assert.IsType(t, WorkspacesChanged{}, snap[0])
	require.IsType(t, ModelChanged{}, snap[1])
	assert.Equal(t, "codex", snap[1].(ModelChanged).View.RCAuth.Reason, "the state as it is now, not as last published")
	assert.IsType(t, AccountsChanged{}, snap[2])
	assert.IsType(t, GitHubChanged{}, snap[3])
	vc := snap[4].(ViewsChanged)
	assert.Equal(t, m.wsIDOf(ws), vc.WS)
	require.Len(t, vc.Views, 1)
	assert.Equal(t, "x", vc.Views[0].Title)

	model, accounts, gh := stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{1, 1, 0}, [3]int{model, accounts, gh}, "a snapshot is no publish: the auth change still goes out at the next Sync")
	assert.Empty(t, m.Sync().Events, "and only once")
}

// TestStateViews_AnswerAsTheModel: every query a replica answers from the
// state views gives what the model's own query gives, for known names and
// misses alike.
func TestStateViews_AnswerAsTheModel(t *testing.T) {
	noCredentialOverride(t)
	for _, setup := range []struct {
		name  string
		build func(*testing.T, *Model)
	}{
		{"bare", func(*testing.T, *Model) {}},
		{"accounts and GitHub", func(t *testing.T, m *Model) {
			withAccounts(t, m, "max-2", "max-3")
			m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthOK, Identity: account.Identity{ConfigDir: "/c", LoggedIn: true}})
			m.SetAccountAuthForTest(map[string]session.RemoteControlAuth{
				"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out", Identity: account.Identity{ConfigDir: "/a2"}},
			})
			m.SetAccountSyncForTest("max-2", account.SyncReport{Linked: []string{"projects"}, Diverged: []string{"settings.json"}})
			m.SetAccountUsageForTest("max-3", account.Usage{Available: true, Plan: "max", At: time.Unix(10, 0)}, errors.New("probe failed"))
			m.Deliver(GitHubResultForTest(true, "", map[string]github.Snapshot{
				"/r": {PRs: map[string]github.PR{"dev/x": {Number: 4}}, Issues: map[int]github.Issue{7: {Number: 7, Title: "t"}}},
			}, map[string]error{"/s": errors.New("no remote")}))
		}},
		{"unavailable registry and gh", func(t *testing.T, m *Model) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), []byte("{not json"), 0o600))
			reg := account.LoadRegistry(dir)
			require.Error(t, reg.LoadErr(), "fixture: the registry must have failed to load")
			m.AdoptAccountsForTest(reg)
			t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
			m.Deliver(GitHubResultForTest(false, "gh is not logged in", nil, nil))
		}},
	} {
		t.Run(setup.name, func(t *testing.T) {
			m := NewForTest(Options{Program: "claude"})
			setup.build(t, m)
			av, gv := m.accountsView(), m.githubView()
			for _, name := range []string{"", account.DefaultName, "max-2", "max-3", "nobody"} {
				assert.Equal(t, m.RCAuthFor(name), av.RCAuthFor(name), "RCAuthFor(%q)", name)
				assert.Equal(t, m.AccountLoggedOut(name), av.AccountLoggedOut(name), "AccountLoggedOut(%q)", name)
				a, ok := m.Account(name)
				va, vok := av.Account(name)
				assert.Equal(t, [2]any{a, ok}, [2]any{va, vok}, "Account(%q)", name)
				rep, ok := m.AccountSync(name)
				vrep, vok := av.AccountSync(name)
				assert.Equal(t, [2]any{rep, ok}, [2]any{vrep, vok}, "AccountSync(%q)", name)
				u, err := m.AccountUsage(name)
				vu, verr := av.AccountUsage(name)
				assert.Equal(t, u, vu, "AccountUsage(%q)", name)
				assert.Equal(t, fmt.Sprint(err), fmt.Sprint(verr), "AccountUsage(%q) error", name)
				env, err := m.AccountEnv(name)
				venv, verr := av.AccountEnv(name)
				assert.Equal(t, env, venv, "AccountEnv(%q)", name)
				assert.Equal(t, fmt.Sprint(err), fmt.Sprint(verr), "AccountEnv(%q) error", name)
			}
			assert.Equal(t, m.AccountNames(), av.AccountNames())
			assert.Equal(t, m.AccountsLoaded(), av.AccountsLoaded())
			assert.Equal(t, m.HasExtraAccounts(), av.HasExtraAccounts())
			assert.Equal(t, m.ClaudeProgram(), av.ClaudeProgram)
			for _, repo := range []string{"/r", "/s", "/nowhere"} {
				s, ok := m.GitHubSnapshot(repo)
				vs, vok := gv.GitHubSnapshot(repo)
				assert.Equal(t, [2]any{s, ok}, [2]any{vs, vok}, "GitHubSnapshot(%q)", repo)
				assert.Equal(t, fmt.Sprint(m.GitHubErr(repo)), fmt.Sprint(gv.GitHubErr(repo)), "GitHubErr(%q)", repo)
			}
			assert.Equal(t, m.GitHubUnavailable(), gv.GitHubUnavailable())
			assert.Equal(t, m.GitHubUnavailableReason(), gv.GitHubUnavailableReason())
		})
	}
}

// TestWorkspacesView_AnswersAsTheModel: the workspace queries a replica
// answers from the published workspaces give what the model gives, a
// workspace whose load failed included.
func TestWorkspacesView_AnswersAsTheModel(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	b.loadErr = errors.New("load instances for workspace b: corrupt")
	m.SetWorkspacesForTest(a, b)
	w := WorkspacesView{Views: m.Workspaces()}
	assert.Equal(t, m.Workspaces(), w.Workspaces())
	for _, id := range []WorkspaceID{0, m.wsIDOf(a), m.wsIDOf(b), 99} {
		v, ok := m.Workspace(id)
		vv, vok := w.Workspace(id)
		assert.Equal(t, [2]any{v, ok}, [2]any{vv, vok}, "Workspace(%d)", id)
		assert.Equal(t, m.IsLoaded(id), w.IsLoaded(id), "IsLoaded(%d)", id)
	}
}

// TestWorkspacesView_WorkspacesIsNeverNil: Workspaces answers an empty
// slice, never nil, as the model does, whether the view is empty or the
// zero value (what a client holds before its first snapshot, or decodes
// from a view with no workspaces).
func TestWorkspacesView_WorkspacesIsNeverNil(t *testing.T) {
	for _, w := range []WorkspacesView{{}, {Views: []WorkspaceView{}}} {
		assert.NotNil(t, w.Workspaces(), "%+v", w)
		assert.Empty(t, w.Workspaces(), "%+v", w)
	}
	assert.Equal(t, NewForTest(Options{}).Workspaces(), WorkspacesView{}.Workspaces(), "as the model serving nothing")
}

// TestWireError_KeepsTheSentinels: an error that crossed the wire still
// matches the sentinels clients test, and nothing else.
func TestWireError_KeepsTheSentinels(t *testing.T) {
	for _, tc := range []struct {
		err       error
		code      string
		refused   bool
		noSession bool
		storage   bool
	}{
		{ErrNoSession, CodeNotFound, true, true, false},
		{refusedError{errors.New("kill x: busy")}, CodeRefused, true, false, false},
		{fmt.Errorf("save: %w", session.ErrStorageLoadFailed), CodeStorage, false, false, true},
		{errors.New("git failed"), CodeError, false, false, false},
	} {
		w := ToWire(tc.err)
		assert.Equal(t, tc.code, w.Code, "%v", tc.err)
		assert.Equal(t, tc.err.Error(), w.Error())
		var back error = w
		assert.Equal(t, tc.refused, errors.Is(back, ErrRefused), "%v is ErrRefused", tc.err)
		assert.Equal(t, tc.noSession, errors.Is(back, ErrNoSession), "%v is ErrNoSession", tc.err)
		assert.Equal(t, tc.storage, errors.Is(back, session.ErrStorageLoadFailed), "%v is ErrStorageLoadFailed", tc.err)
	}
	assert.Nil(t, ToWire(nil))
	assert.True(t, FromWire(nil) == nil, "a nil interface, not a typed nil (assert.Nil accepts both)")
	w := &WireError{Code: CodeRefused, Message: "m"}
	assert.Same(t, w, ToWire(w), "a WireError passes through")
}

// TestToWire_KeepsTheContextAroundAWireError: an error wrapping a WireError
// keeps its code and the wrapping's whole message, not just the inner one.
func TestToWire_KeepsTheContextAroundAWireError(t *testing.T) {
	w := ToWire(fmt.Errorf("decode params: %w", &WireError{Code: CodeProtocol, Message: "bad frame"}))
	assert.Equal(t, CodeProtocol, w.Code)
	assert.Equal(t, "decode params: bad frame", w.Message)

	nf := ToWire(fmt.Errorf("kill x: %w", &WireError{Code: CodeNotFound, Message: "no such session"}))
	assert.Equal(t, "kill x: no such session", nf.Message)
	assert.True(t, errors.Is(nf, ErrNoSession), "the sentinel survives the wrapping")
}

// TestEventsWithErrors_RoundTripJSON: Notice and Reply keep their errors'
// text and sentinels through JSON, and a nil error stays nil.
func TestEventsWithErrors_RoundTripJSON(t *testing.T) {
	n := Notice{Err: refusedError{errors.New("pause y: busy")}, Info: "i"}
	b, err := json.Marshal(n)
	require.NoError(t, err)
	var back Notice
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, "pause y: busy", back.Err.Error())
	assert.True(t, errors.Is(back.Err, ErrRefused))
	assert.Equal(t, "i", back.Info)

	r := Reply{Req: 3, ID: 9, Notice: errors.New("stash forgotten"), Issue: github.Issue{Number: 5}}
	b, err = json.Marshal(r)
	require.NoError(t, err)
	var rback Reply
	require.NoError(t, json.Unmarshal(b, &rback))
	assert.Equal(t, ReqID(3), rback.Req)
	assert.Equal(t, InstanceID(9), rback.ID)
	assert.True(t, rback.Err == nil, "nil stays a nil interface, not a typed nil (assert.Nil accepts both)")
	assert.Equal(t, "stash forgotten", rback.Notice.Error())
	assert.Equal(t, 5, rback.Issue.Number)
}
