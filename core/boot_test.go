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

// openDef opens def's served workspace (Open) and returns its view.
func openDef(t *testing.T, m *Model, def config.Workspace) (WorkspaceView, error) {
	t.Helper()
	ws := served(m, def)
	require.NotNil(t, ws, "the model serves %s", def.Name)
	return m.Open(m.wsIDOf(ws))
}

// Opening a workspace the first time starts its terminal, once; a later
// open (another tab, another client) shows the same workspace, under the
// same ID.
func TestOpen_StartsTheWorkspaceTerminalOnTheFirstOpen(t *testing.T) {
	a := workspaceDef(t, "term-a", `[]`, fakeClaude(t))
	killSessionAtEnd(t, "term-a")
	m := bootModel(t, a)
	m.boot()
	ws := served(m, a)
	require.Nil(t, ws.terminal())

	v, err := openDef(t, m, a)
	require.NoError(t, err)
	term := ws.terminal()
	require.NotNil(t, term, "the first open created it")
	assert.True(t, term.Started())

	again, err := openDef(t, m, a)
	require.NoError(t, err)
	assert.Len(t, ws.insts, 1, "a later open creates no second terminal")
	assert.Equal(t, v.ID, again.ID, "the same workspace, the same ID")
}

// An unknown workspace ID opens nothing.
func TestOpen_AnUnknownWorkspaceIsAnError(t *testing.T) {
	m := bootModel(t)
	m.boot()
	_, err := m.Open(99)
	assert.Error(t, err)
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
	m.SetWorkspacesForTest(ws)
	m.Drain()

	m.ensureTerminal(ws)

	assert.Equal(t, session.Running, term.GetStatus())
	assert.Zero(t, term.RestartFailureCount(), "its breaker is reset")
	assert.Contains(t, m.Drain().Events, Event(SessionLaunched{ID: m.idOf(term)}))
}

// Opening a workspace whose load failed loads it again, rereading it from
// disk: the user may have fixed what broke it. Until then the open reports
// the load's error, and the workspace's view carries it (LoadErr).
func TestOpen_RetriesAFailedLoadAndReportsItsError(t *testing.T) {
	def := workspaceDef(t, "flaky", `{"not":"an array"}`, "true")
	m := bootModel(t, def)
	m.boot()
	require.Error(t, served(m, def).loadErr)
	id := m.wsIDOf(served(m, def))
	v, ok := m.Workspace(id)
	require.True(t, ok, "a failed workspace is still served")
	assert.Contains(t, v.LoadErr, "flaky", "its view names the failure")

	_, err := m.Open(id)
	require.Error(t, err, "still broken")
	assert.Contains(t, err.Error(), "load instances for workspace flaky")
	assert.False(t, served(m, def).opened, "a failed open is no first open")

	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&def), config.StateFileName),
		[]byte(`{"instances":[{"title":"x","status":3,"program":"claude"}]}`), 0o644))
	v, err = m.Open(id)
	require.NoError(t, err)
	assert.Equal(t, id, v.ID)
	assert.Empty(t, v.LoadErr, "loaded at last")
	assert.NotNil(t, served(m, def).byTitle("x"))
	assert.False(t, served(m, def).storage.WritesRefused())
}

// A workspace registered, or found registered on a reread, is served from
// then on, like every registered workspace.
func TestRegisterAndReloadRegistry_LoadTheNewWorkspace(t *testing.T) {
	m := bootModel(t)
	m.boot()
	n := len(m.Loaded())

	repo := t.TempDir()
	v, err := m.Register("new", repo)
	require.NoError(t, err)
	assert.Len(t, m.Loaded(), n+1)
	assert.Equal(t, "new", v.Name)
	assert.True(t, m.IsLoaded(v.ID), "its view names the served workspace")
	assert.NotNil(t, served(m, config.Workspace{Name: "new", Path: repo}))

	other := workspaceDef(t, "elsewhere", `[]`, "true")
	elsewhere, err := config.LoadWorkspaceRegistry() // another process's
	require.NoError(t, err)
	require.NoError(t, elsewhere.Add(other.Name, other.Path))
	require.NoError(t, m.ReloadRegistry())
	assert.NotNil(t, served(m, other))
}

// Opening the global workspace, as a client entering global mode does,
// shows the one the model has served since boot: nothing loads or is
// dropped, and the workspaces the client stops showing stay served.
func TestOpen_TheGlobalWorkspaceIsTheServedOne(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	m := bootModel(t, a)
	m.boot()
	global, err := m.globalWS()
	require.NoError(t, err)
	_, err = openDef(t, m, a)
	require.NoError(t, err)
	n := len(m.Loaded())

	v, err := m.Open(m.wsIDOf(global))
	require.NoError(t, err)

	assert.Equal(t, m.wsIDOf(global), v.ID)
	assert.Equal(t, "global", v.Label)
	assert.Len(t, m.Loaded(), n, "nothing loads")
	assert.True(t, m.isLoadedWS(served(m, a)), "the workspace no longer shown is still served")
}

// Quitting saves every workspace the model serves, not only the opened
// ones.
func TestSaveForQuit_SavesEveryServedWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := bootModel(t, a, b)
	m.boot()
	_, err := openDef(t, m, a)
	require.NoError(t, err)
	served(m, b).add(pausedInst(t, "unshown"))

	require.NoError(t, m.SaveForQuit())

	data, err := os.ReadFile(filepath.Join(config.WorkspaceConfigDir(&b), config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"unshown"`)
}

// The published state is every served workspace and its instances, in
// serve order (Workspaces): which of them a client shows is its own.
func TestPublish_EveryServedWorkspace(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	broken := workspaceDef(t, "broken", `{"not":"an array"}`, "true")
	m := bootModel(t, a, b, broken)
	m.boot()
	global, err := m.globalWS()
	require.NoError(t, err)

	views := m.Workspaces()
	require.Len(t, views, 4)
	for i, ws := range []*Workspace{global, served(m, a), served(m, b), served(m, broken)} {
		assert.Equal(t, m.wsIDOf(ws), views[i].ID, "serve order: the global workspace, then the registry's")
	}
	assert.Empty(t, views[1].LoadErr)
	assert.NotEmpty(t, views[3].LoadErr, "a failed load is published with its error")

	out := m.Sync()
	wc := workspacesEvent(out.Events)
	require.NotNil(t, wc)
	assert.Equal(t, views, wc.Views)
	var vb *ViewsChanged
	for _, ev := range out.Events {
		if vc, ok := ev.(ViewsChanged); ok && vc.WS == m.wsIDOf(served(m, b)) {
			vb = &vc
		}
	}
	require.NotNil(t, vb, "an unopened workspace's instances are published too")
	require.Len(t, vb.Views, 1)
	assert.Equal(t, "x", vb.Views[0].Title)
	assert.True(t, m.IsLoaded(m.wsIDOf(served(m, b))))
}

// Which workspaces a client shows is its own state (daemon stage 3A): an
// open publishes nothing tab-like, so one client opening a workspace
// changes nothing another client shows. The workspace views carry nothing
// of who opened what, and an open of a workspace that loaded publishes no
// workspace view at all.
func TestOpen_PublishesNothingTabLike(t *testing.T) {
	a := preservedTerminalWorkspace(t, "ws-a") // its terminal record is preserved: no launch
	b := preservedTerminalWorkspace(t, "ws-b")
	m := bootModel(t, a, b)
	m.boot()
	before := m.Workspaces()
	m.Sync()

	_, err := openDef(t, m, a)
	require.NoError(t, err)

	assert.Equal(t, before, m.Workspaces(), "no view says a is open")
	assert.Nil(t, workspacesEvent(m.Sync().Events), "the open published no workspace view")
}

// PersistOpenList writes exactly the names a client gives, in its order:
// the open list is the client's, and the model keeps none of its own.
func TestPersistOpenList_WritesExactlyTheNamesGiven(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	c := workspaceDef(t, "c", `[]`, "true")
	m := bootModel(t)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	for _, def := range []config.Workspace{a, b, c} {
		require.NoError(t, reg.Add(def.Name, def.Path))
	}
	m.SetRegistryForTest(reg)
	onDisk := func() []string {
		t.Helper()
		fresh, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		return fresh.OpenWorkspaces
	}

	m.PersistOpenList([]string{"c", "a"})
	assert.Equal(t, []string{"c", "a"}, onDisk())

	m.PersistOpenList(nil)
	assert.Empty(t, onDisk(), "an empty list clears it")
}

// Boot loads the account registry before anything else and hands back
// the notices it raised, since no client is connected yet to be sent
// them; it leaves none behind for the first Sync.
func TestBoot_ReturnsTheAccountRegistrysNotices(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(global, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(global, "accounts.json"), []byte(`{not json`), 0o644))

	notices := m.Boot()

	require.Len(t, notices, 1)
	n, ok := notices[0].(Notice)
	require.True(t, ok)
	assert.Contains(t, n.Err.Error(), "accounts:")
	assert.True(t, m.booted, "and boots")
	for _, ev := range m.Sync().Events {
		assert.NotEqual(t, notices[0], ev, "nothing left for the first Sync")
	}
}
