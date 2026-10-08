package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceDef lays out a registered workspace named name: a repository
// directory whose config dir holds state (the instances array, raw JSON)
// and a config.json launching program.
func workspaceDef(t *testing.T, name, instancesJSON, program string) config.Workspace {
	t.Helper()
	def := config.Workspace{Name: name, Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":`+instancesJSON+`}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(`{"default_program":"`+program+`"}`), 0o644))
	return def
}

// bootModel is a model over registry defs, its own global dir, and a tmux
// server on which every session is dead (no sweep kills anything).
func bootModel(t *testing.T, defs ...config.Workspace) *Model {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	dead := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	return NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: defs}, CmdExec: dead})
}

// served is the model's workspace of def, nil when it serves none.
func served(m *Model, def config.Workspace) *Workspace {
	return m.wsByConfigDir(config.WorkspaceConfigDir(&def))
}

// killSessionAtEnd kills title's tmux session on the test's private server
// when the test ends.
func killSessionAtEnd(t *testing.T, title string) {
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run()
	})
}

// The model serves every registered workspace and the global one from the
// moment it boots, each loaded once; one that fails to load is kept,
// latched, so nothing overwrites its unreadable state.
func TestBoot_LoadsEveryRegisteredWorkspaceOnce(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	broken := workspaceDef(t, "broken", `{"not":"an array"}`, "true")
	m := bootModel(t, a, b, broken)

	m.boot()

	require.NotNil(t, served(m, a))
	require.NotNil(t, served(m, b))
	assert.NotNil(t, served(m, b).byTitle("x"), "its records are loaded")
	global, err := m.globalWS()
	require.NoError(t, err)
	assert.Len(t, m.Loaded(), 4, "the three registered workspaces and the global one")
	require.NotNil(t, served(m, broken), "a failed load is kept")
	assert.Error(t, served(m, broken).loadErr)
	assert.True(t, served(m, broken).storage.WritesRefused(), "latched shut")
	for _, ws := range m.Loaded() {
		assert.False(t, ws.opened, "nobody has opened %s", ws.Label())
	}

	m.boot()
	assert.Len(t, m.Loaded(), 4, "boot loads once")
	assert.Same(t, global, m.Loaded()[0], "the global workspace first: no startup context here")
}

// A workspace terminal is the workspace's first open's to start: booting
// loads every workspace but starts no terminal, and a terminal record whose
// session died waits for that open to be relaunched, rather than being
// probed, found dead and paused for a workspace nobody has looked at.
func TestBoot_AnUnopenedWorkspacesTerminalIsDormant(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"b","status":0,"program":"claude","is_workspace_terminal":true}]`, "true")
	m := bootModel(t, a, b)
	m.boot()

	assert.Nil(t, served(m, a).terminal(), "no terminal created before the first open")
	term := served(m, b).terminal()
	require.NotNil(t, term)
	assert.True(t, term.CrashRecovered(), "its session is dead: the relaunch waits for the first open")
	assert.NotContains(t, m.activeInstances(), term, "and nothing probes it meanwhile")

	served(m, b).opened = true
	assert.Contains(t, m.activeInstances(), term, "an opened workspace's terminal is probed")
}

// Opening a workspace the first time starts its terminal, once.
func TestOpen_StartsTheWorkspaceTerminalOnTheFirstOpen(t *testing.T) {
	a := workspaceDef(t, "term-a", `[]`, fakeClaude(t))
	killSessionAtEnd(t, "term-a")
	m := bootModel(t, a)
	m.boot()
	ws := served(m, a)
	require.Nil(t, ws.terminal())

	_, err := m.OpenTab(a)
	require.NoError(t, err)
	term := ws.terminal()
	require.NotNil(t, term, "the first open created it")
	assert.True(t, term.Started())

	require.NoError(t, m.CloseTab("x")) // no such tab: nothing happens
	_, err = m.OpenTab(a)
	require.NoError(t, err)
	assert.Len(t, ws.insts, 1, "a later open creates no second terminal")
}

// A terminal its restart breaker stopped stays Paused, since nothing can
// resume a workspace terminal: the first open after a start is the user
// asking for it again, so it relaunches with its breaker reset.
func TestEnsureTerminal_ATrippedTerminalIsRelaunched(t *testing.T) {
	repo := t.TempDir()
	killSessionAtEnd(t, "tripped")
	term, err := session.FromInstanceData(session.InstanceData{
		Title: "tripped", Path: repo, Status: session.Paused, Program: fakeClaude(t), IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	for range maxWorkspaceTerminalRestartFailures {
		term.RecordRestartFailure()
	}
	ws := storedWorkspace(t, "tripped")
	ws.ctx.RepoPath = repo
	ws.add(term)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	m.Drain()

	m.ensureTerminal(ws)

	assert.Equal(t, session.Running, term.GetStatus())
	assert.Zero(t, term.RestartFailureCount(), "its breaker is reset")
	assert.Contains(t, m.Drain().Events, Event(SessionLaunched{ID: m.idOf(term)}))
}

// Opening a workspace whose load failed loads it again: the user may have
// fixed what broke it.
func TestOpenTab_RetriesAFailedLoad(t *testing.T) {
	def := workspaceDef(t, "flaky", `{"not":"an array"}`, "true")
	m := bootModel(t, def)
	m.boot()
	require.Error(t, served(m, def).loadErr)

	_, err := m.OpenTab(def)
	require.Error(t, err, "still broken")
	assert.Empty(t, m.Tabs())

	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName),
		[]byte(`{"instances":[{"title":"x","status":3,"program":"claude"}]}`), 0o644))
	v, err := m.OpenTab(def)
	require.NoError(t, err)
	assert.Equal(t, v.ID, m.Tabs()[0].ID)
	assert.NotNil(t, served(m, def).byTitle("x"), "loaded at last")
	assert.False(t, served(m, def).storage.WritesRefused())
}

// Closing a tab only stops showing its workspace: reopening it shows the
// very workspace the model kept serving, under the same ID, with whatever
// happened to it meanwhile.
func TestOpenTab_AReopenedTabIsTheSameWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := bootModel(t, a, b)
	m.boot()
	va, err := m.OpenTab(a)
	require.NoError(t, err)
	_, err = m.OpenTab(b)
	require.NoError(t, err)
	require.NoError(t, m.CloseTab("a"))
	m.Sync() // published while closed
	served(m, a).add(pausedInst(t, "landed-meanwhile"))

	again, err := m.OpenTab(a)
	require.NoError(t, err)
	assert.Equal(t, va.ID, again.ID, "the same workspace, the same ID")
	assert.NotNil(t, served(m, a).byTitle("landed-meanwhile"))
}

// A workspace registered, or found registered on a reread, is served from
// then on, like every registered workspace.
func TestRegisterAndReloadRegistry_LoadTheNewWorkspace(t *testing.T) {
	m := bootModel(t)
	m.boot()
	n := len(m.Loaded())

	repo := t.TempDir()
	def, err := m.Register("new", repo)
	require.NoError(t, err)
	assert.Len(t, m.Loaded(), n+1)
	assert.NotNil(t, served(m, def))

	other := workspaceDef(t, "elsewhere", `[]`, "true")
	elsewhere, err := config.LoadWorkspaceRegistry() // another process's
	require.NoError(t, err)
	require.NoError(t, elsewhere.Add(other.Name, other.Path))
	require.NoError(t, m.ReloadRegistry())
	assert.NotNil(t, served(m, other))
}

// Global mode shows the global workspace the model has served since boot:
// nothing loads or is dropped, and the tabs' workspaces stay served.
func TestEnterGlobal_ShowsTheServedGlobalWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	m := bootModel(t, a)
	m.boot()
	global, err := m.globalWS()
	require.NoError(t, err)
	_, err = m.OpenTab(a)
	require.NoError(t, err)

	v, err := m.EnterGlobal(0)
	require.NoError(t, err)

	assert.Empty(t, m.Tabs())
	assert.Same(t, global, m.classic)
	assert.Equal(t, m.wsIDOf(global), v.ID)
	assert.True(t, m.isLoadedWS(served(m, a)), "the closed tab's workspace is still served")
}

// Quitting saves every workspace the model serves, not only the shown ones.
func TestSaveForQuit_SavesEveryServedWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := bootModel(t, a, b)
	m.boot()
	_, err := m.OpenTab(a)
	require.NoError(t, err)
	served(m, b).add(pausedInst(t, "unshown"))

	require.NoError(t, m.SaveForQuit())

	data, err := os.ReadFile(filepath.Join(config.WorkspaceConfigDir(&b), config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"unshown"`)
}

// The published state is what the TUI shows: the model serves more, but
// publishes only the shown workspaces and their instances, as if they
// were all it loaded (until the TUI keeps its own tabs).
func TestPublish_OnlyTheShownWorkspaces(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	m := bootModel(t, a, b)
	m.boot()
	va, err := m.OpenTab(a)
	require.NoError(t, err)

	out := m.Sync()
	wc := workspacesEvent(out.Events)
	require.NotNil(t, wc)
	require.Len(t, wc.Views, 1)
	assert.Equal(t, va.ID, wc.Views[0].ID)
	for _, ev := range out.Events {
		if vc, ok := ev.(ViewsChanged); ok {
			assert.Equal(t, va.ID, vc.WS, "views of the shown workspace only")
		}
	}
	assert.False(t, m.IsLoaded(m.wsIDOf(served(m, b))), "a client sees only what is shown")
}
