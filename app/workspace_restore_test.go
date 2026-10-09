package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExec is a cmd.Executor that runs nothing and records every
// command's argv, so the workspace load paths can be driven through the
// model's executor seam (core.Model.SetExecForTest) and asserted on
// without touching a tmux server.
type recordingExec struct {
	mu   sync.Mutex
	args [][]string
}

func (r *recordingExec) record(c *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, append([]string(nil), c.Args...))
}

func (r *recordingExec) Run(c *exec.Cmd) error { r.record(c); return nil }

func (r *recordingExec) Output(c *exec.Cmd) ([]byte, error) { r.record(c); return nil, nil }

func (r *recordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { r.record(c); return nil, nil }

// ran reports whether any recorded command carried arg as a whole argument
// (e.g. "ls" for the orphan sweep, "kill-session" for a targeted kill).
func (r *recordingExec) ran(arg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, argv := range r.args {
		for _, a := range argv {
			if a == arg {
				return true
			}
		}
	}
	return false
}

var _ cmd2.Executor = (*recordingExec)(nil)

// isolateTmuxCounter gives each isolateTmux call within this process a
// distinct short suffix. Combined with the pid (distinguishing concurrent
// test processes), this keeps socket names unique without the 19-digit
// time.Now().UnixNano() this used to carry — length matters here: tmux's
// unix socket path is TMUX_TMPDIR/tmux-<uid>/<name>, and sun_path is capped
// at 108 bytes on Linux, 104 on macOS.
var isolateTmuxCounter atomic.Int64

// isolateTmux points every tmux.Command in this test at a private, throwaway
// server. The recording executor already keeps the load paths off tmux; this
// is the safety belt for code that builds its own executor (Instance.Start),
// so a regression can never reach the developer's real server.
func isolateTmux(t *testing.T) {
	t.Helper()
	sock := fmt.Sprintf("lt-%d-%d", os.Getpid(), isolateTmuxCounter.Add(1))
	t.Setenv(tmux.EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run() })
}

// writeWorkspaceState lays out a workspace directory whose state.json holds
// instancesJSON, with a harmless default program.
func writeWorkspaceState(t *testing.T, name, instancesJSON string) config.Workspace {
	t.Helper()
	ws := config.Workspace{Name: name, Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&ws)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName),
		[]byte(`{"instances":`+instancesJSON+`}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"true"}`), 0o644))
	return ws
}

// preservedTerminalWorkspace is a workspace whose only record is its
// workspace terminal, written by a newer loom (schema_version 99). A newer
// binary stamps every record with its own version, so after a downgrade
// this is exactly what each workspace looks like.
func preservedTerminalWorkspace(t *testing.T, name string) config.Workspace {
	t.Helper()
	rec, err := json.Marshal(map[string]any{
		"schema_version":        99,
		"title":                 name,
		"program":               "claude",
		"is_workspace_terminal": true,
		"worktree":              map[string]any{},
	})
	require.NoError(t, err)
	return writeWorkspaceState(t, name, "["+string(rec)+"]")
}

// newRestoreHome is a home as startHome leaves it with no saved tab to
// restore (startupHome), over a model whose every executor is exec: its
// classic slot shows the global workspace of the test's own
// LOOM_GLOBAL_DIR, which the caller sets first, and its registry is the
// one there. A test registers the workspaces it activates
// (registerWorkspaces).
func newRestoreHome(t *testing.T, exec cmd2.Executor) *home {
	t.Helper()
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	return startupHome(t, exec, reg, "", "")
}

// TestActivateWorkspace_PreservedTerminalIsNotReplaced: after a downgrade the
// workspace terminal's record is undecodable, so the decoded list has no
// terminal. Activation used to take that as "none exists": it killed the
// tmux session backing the preserved terminal and auto-created a second
// record under the same title, and both were persisted.
func TestActivateWorkspace_PreservedTerminalIsNotReplaced(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	ws := preservedTerminalWorkspace(t, "ws-term")
	rec := &recordingExec{}
	m := newRestoreHome(t, rec)
	registerWorkspaces(t, m, ws)

	_, err := m.activateWorkspace(ws)
	require.NoError(t, err)
	require.Len(t, m.slots, 1)
	slot := m.slots[0]

	assert.False(t, rec.ran("kill-session"), "the preserved terminal's tmux session must not be killed")
	for _, inst := range slot.list.GetInstances() {
		assert.NotEqual(t, "ws-term", inst.Title, "no second workspace terminal under the preserved record's title")
	}

	require.NoError(t, slot.storage().SaveInstances(core.Persistable(slot.ws().InstancesForTest())))
	raw, err := os.ReadFile(filepath.Join(config.WorkspaceConfigDir(&ws), config.StateFileName))
	require.NoError(t, err)
	var st struct {
		Instances []struct {
			Title string `json:"title"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(raw, &st))
	count := 0
	for _, r := range st.Instances {
		if r.Title == "ws-term" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly the preserved record may carry the terminal's title")
}

// TestStartupRestore_AFailedWorkspaceDoesNotStopTheSweep: the startup
// sweep spares only the titles it can see. A workspace whose load failed
// contributes none (they can't be read), so its roots are left out of the
// sweep, which then still runs for every other workspace: with every
// registered workspace loaded, skipping it would leave every orphan behind
// for as long as one workspace stays broken. What the sweep kills and
// spares is TestOrphanSweep_SparesWhatThisLoomCannotVouchFor's.
func TestStartupRestore_AFailedWorkspaceDoesNotStopTheSweep(t *testing.T) {
	isolateTmux(t)

	t.Run("control: every workspace loads, sweep runs", func(t *testing.T) {
		rec := &recordingExec{}
		m, _ := restoreModeHome(t, rec, `[]`, preservedTerminalWorkspace(t, "ws-good"))

		require.Len(t, m.slots, 1)
		assert.True(t, rec.ran("ls"), "with every workspace loaded the sweep must still run")
	})

	t.Run("one workspace fails, the sweep still runs", func(t *testing.T) {
		rec := &recordingExec{}
		bad := writeWorkspaceState(t, "ws-bad", `{"not":"an array"}`)
		m, _ := restoreModeHome(t, rec, `[]`, bad, preservedTerminalWorkspace(t, "ws-good"))

		require.Len(t, m.slots, 1, "the good workspace still opens")
		assert.True(t, rec.ran("ls"), "the others are still swept")
	})
}

// restoreModeHome is a home as startHome leaves it at startup (startupHome)
// over a fresh global dir whose state.json — instancesJSON — the global
// workspace loads, with saved registered and in the registry's open list:
// the startup restores them as tabs (restoreSavedWorkspaces), and with
// none saved or none opening shows the global workspace. Returns the
// global state.json's path.
func restoreModeHome(t *testing.T, exec cmd2.Executor, instancesJSON string, saved ...config.Workspace) (*home, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	statePath := filepath.Join(dir, config.StateFileName)
	require.NoError(t, os.WriteFile(statePath, []byte(`{"instances":`+instancesJSON+`}`), 0o644))
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	var names []string
	for _, def := range saved {
		require.NoError(t, reg.Add(def.Name, def.Path))
		names = append(names, def.Name)
	}
	require.NoError(t, reg.SetOpenWorkspaces(names))
	return wirePanes(t, newRestoreHome(t, exec)), statePath
}

func corruptWorkspaces(t *testing.T, names ...string) []config.Workspace {
	t.Helper()
	out := make([]config.Workspace, 0, len(names))
	for _, n := range names {
		out = append(out, writeWorkspaceState(t, n, `{"not":"an array"}`))
	}
	return out
}

func listTitles(m *home) []string {
	var titles []string
	for _, inst := range m.list.GetInstances() {
		titles = append(titles, inst.Title)
	}
	return titles
}

// savedRegistry registers defs in the registry under the test's own
// LOOM_GLOBAL_DIR, with open as its open list and lastUsed as the
// workspace last focused ("" for none), as a previous run left it.
func savedRegistry(t *testing.T, open []string, lastUsed string, defs ...config.Workspace) *config.WorkspaceRegistry {
	t.Helper()
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	for _, def := range defs {
		require.NoError(t, reg.Add(def.Name, def.Path))
	}
	require.NoError(t, reg.SetOpenWorkspaces(open))
	if lastUsed != "" {
		require.NoError(t, reg.UpdateLastUsed(lastUsed))
	}
	return reg
}

// lastUsedOnDisk is the registry's last used workspace as the next launch
// reads it.
func lastUsedOnDisk(t *testing.T) string {
	t.Helper()
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	return fresh.LastUsed
}

// TestStartupRestore_FocusesTheLastUsedTab: a plain `loom` restores the
// saved tabs and focuses the one last used, and records it as the last
// used again. The client's replica of the registry once dropped LastUsed,
// so the restore focused the first tab and then wrote it as the last used.
func TestStartupRestore_FocusesTheLastUsedTab(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	reg := savedRegistry(t, []string{"ws-a", "ws-b"}, "ws-b", a, b)

	m := startupHome(t, &recordingExec{}, reg, "", "")

	assert.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
	assert.Equal(t, "ws-b", m.name(), "the last used tab takes focus")
	assert.Equal(t, "ws-b", lastUsedOnDisk(t))
}

// TestStartupRestore_TheStartupWorkspaceTakesFocus: `loom <dir>` or
// --workspace names the workspace this TUI starts on. The restore opens it
// beside the saved tabs when it is not among them, focuses it over the last
// used tab, and records it as the last used.
func TestStartupRestore_TheStartupWorkspaceTakesFocus(t *testing.T) {
	isolateTmux(t)

	t.Run("not among the saved tabs: opened beside them", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		a, b, c := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b"), preservedTerminalWorkspace(t, "ws-c")
		reg := savedRegistry(t, []string{"ws-a", "ws-b"}, "ws-b", a, b, c)

		m := startupHome(t, &recordingExec{}, reg, "ws-c", "")

		assert.Equal(t, []string{"ws-a", "ws-b", "ws-c"}, m.slotNames())
		assert.Equal(t, "ws-c", m.name(), "the startup workspace takes focus")
		assert.Equal(t, "ws-c", lastUsedOnDisk(t))
	})

	t.Run("among the saved tabs: focused over the last used", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
		reg := savedRegistry(t, []string{"ws-a", "ws-b"}, "ws-b", a, b)

		m := startupHome(t, &recordingExec{}, reg, "ws-a", "")

		assert.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
		assert.Equal(t, "ws-a", m.name())
		assert.Equal(t, "ws-a", lastUsedOnDisk(t), "the focused tab is the last used from now on")
	})
}

// TestEnterGlobalMode_ClosesTheOpenList: returning to global mode closes
// every tab and the workspaces that failed to restore, and clears the
// registry's open list, so the next launch lands in global mode rather than
// restoring what the user just closed.
func TestEnterGlobalMode_ClosesTheOpenList(t *testing.T) {
	isolateTmux(t)
	good := preservedTerminalWorkspace(t, "ws-good")
	bad := corruptWorkspaces(t, "ws-bad")[0]
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`, good, bad)
	m.ctx = cancelledCtx()
	require.Equal(t, []string{"ws-good"}, m.slotNames())
	require.Equal(t, []string{"ws-bad"}, m.failedOpen)

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	assert.Empty(t, m.failedOpen)
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Empty(t, fresh.OpenWorkspaces)
}

// TestOpenTab_AFailedWorkspaceThatOpensIsNoLongerFailed: a workspace that
// failed to restore and opens later (its cause fixed, then checked in the
// picker) is an ordinary tab from then on, no longer marked failed.
func TestOpenTab_AFailedWorkspaceThatOpensIsNoLongerFailed(t *testing.T) {
	isolateTmux(t)
	good := preservedTerminalWorkspace(t, "ws-good")
	bad := corruptWorkspaces(t, "ws-bad")[0]
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`, good, bad)
	m.ctx = cancelledCtx()
	require.Equal(t, []string{"ws-bad"}, m.failedOpen)
	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&bad), config.StateFileName),
		[]byte(`{"instances":[]}`), 0o644))

	drainCmd(m.applyWorkspaceToggle([]config.Workspace{good, bad}))

	assert.Equal(t, []string{"ws-good", "ws-bad"}, m.slotNames())
	assert.Empty(t, m.failedOpen)
	_, _ = runOpenWorkspacePicker(m)
	require.NotNil(t, m.workspacePicker())
	m.workspacePicker().SetWidth(200)
	assert.NotContains(t, m.workspacePicker().Render(), "(failed to load)")
}

// TestHandleQuit_PersistsTheOpenList: quitting writes this TUI's tabs as
// the registry's open list, whatever another process wrote there since, so
// the next launch restores what was open at quit.
func TestHandleQuit_PersistsTheOpenList(t *testing.T) {
	isolateTmux(t)
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`, a, b)
	m.ctx = cancelledCtx()
	require.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
	other, err := config.LoadWorkspaceRegistry() // another process's
	require.NoError(t, err)
	require.NoError(t, other.SetOpenWorkspaces([]string{"ws-b"}))

	_, cmd := m.handleQuit()
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())

	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-a", "ws-b"}, fresh.OpenWorkspaces)
}

// TestStartupRestore_OnlyASavedTabIsKeptAsFailed: the restore also opens
// the startup workspace when the saved tabs lack it. If that one fails, it
// was never open, so it is not kept open to be retried (failedOpen): the
// open list stays the tabs that were.
func TestStartupRestore_OnlyASavedTabIsKeptAsFailed(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	good := preservedTerminalWorkspace(t, "ws-good")
	bad := corruptWorkspaces(t, "ws-bad")[0]
	reg := savedRegistry(t, []string{"ws-good"}, "", good, bad)

	m := startupHome(t, &recordingExec{}, reg, "ws-bad", "")

	assert.Equal(t, []string{"ws-good"}, m.slotNames())
	assert.Empty(t, m.failedOpen, "the startup workspace was not saved open")
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-good"}, fresh.OpenWorkspaces)
}

// TestStartupRestore_ANameListedTwiceIsOneTab: an open list naming a
// workspace twice (a hand edit, a race between two writers) restores it as
// one tab, and the list persisted after names it once.
func TestStartupRestore_ANameListedTwiceIsOneTab(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	a, b := preservedTerminalWorkspace(t, "ws-a"), preservedTerminalWorkspace(t, "ws-b")
	reg := savedRegistry(t, []string{"ws-a", "ws-a", "ws-b"}, "", a, b)
	require.Len(t, reg.GetOpenWorkspaces(), 3, "fixture: the open list holds a name twice")

	m := startupHome(t, &recordingExec{}, reg, "", "")

	assert.Equal(t, []string{"ws-a", "ws-b"}, m.slotNames())
	require.NoError(t, m.checkSlotInvariant())
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-a", "ws-b"}, fresh.OpenWorkspaces)
}

// twinRegistry registers ws-first, and ws-second for the same directory
// through a symlink, under the test's own LOOM_GLOBAL_DIR, with open as the
// open list. The model serves the directory once, as ws-first.
func twinRegistry(t *testing.T, open ...string) *config.WorkspaceRegistry {
	t.Helper()
	first := preservedTerminalWorkspace(t, "ws-first")
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(first.Path, link))
	return savedRegistry(t, open, "", first, config.Workspace{Name: "ws-second", Path: link})
}

// TestStartupHome_OnATwinShowsItsServedWorkspace: `loom --workspace
// ws-second` (or `loom <link>`) names a directory loom serves as ws-first.
// It starts on ws-first, saying so, rather than refusing to start.
func TestStartupHome_OnATwinShowsItsServedWorkspace(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	reg := twinRegistry(t)

	m := startupHome(t, &recordingExec{}, reg, "ws-second", "")

	assert.Empty(t, m.slots)
	assert.Equal(t, "ws-first", m.name(), "the classic slot shows the served twin")
	assert.Contains(t, m.errBox.String(), "ws-second is the same directory as ws-first; showing ws-first")
	require.NoError(t, m.checkSlotInvariant())
}

// TestStartupRestore_ATwinInTheOpenListOpensItsServedWorkspace: a saved
// tab named for a directory loom serves under another registered name
// opens that workspace's tab, once, saying so; it is never kept as a
// failed name, which no retry could open.
func TestStartupRestore_ATwinInTheOpenListOpensItsServedWorkspace(t *testing.T) {
	isolateTmux(t)

	t.Run("the twin alone", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		reg := twinRegistry(t, "ws-second")

		m := startupHome(t, &recordingExec{}, reg, "", "")

		assert.Equal(t, []string{"ws-first"}, m.slotNames())
		assert.Empty(t, m.failedOpen)
		assert.Contains(t, m.errBox.String(), "ws-second is the same directory as ws-first; showing ws-first")
		fresh, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		assert.Equal(t, []string{"ws-first"}, fresh.OpenWorkspaces, "the open list heals")
	})

	t.Run("both names: one tab", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		reg := twinRegistry(t, "ws-first", "ws-second")

		m := startupHome(t, &recordingExec{}, reg, "", "")

		assert.Equal(t, []string{"ws-first"}, m.slotNames())
		assert.Empty(t, m.failedOpen)
		require.NoError(t, m.checkSlotInvariant())
	})
}

// homeWorkspace registers "home", a workspace at the directory whose .loom
// is the global config dir (as $HOME's is), with open as the open list.
// The model serves that config dir once, as the global workspace.
func homeWorkspace(t *testing.T, open ...string) (config.Workspace, *config.WorkspaceRegistry) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvGlobalDir, filepath.Join(home, ".loom"))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".loom"), 0o755))
	def := config.Workspace{Name: "home", Path: home}
	return def, savedRegistry(t, open, "", def)
}

// TestHomeWorkspace_IsTheGlobalWorkspace: a workspace registered at $HOME
// has the global config dir, so the model serves it as the global
// workspace. `loom --workspace home` starts on the global workspace, saying
// why, where it refused to start; a saved tab of it opens nothing; and the
// picker refuses it with that reason, not a restart that cannot help.
func TestHomeWorkspace_IsTheGlobalWorkspace(t *testing.T) {
	isolateTmux(t)

	t.Run("startup", func(t *testing.T) {
		_, reg := homeWorkspace(t)

		m := startupHome(t, &recordingExec{}, reg, "home", "")

		assert.Empty(t, m.slots)
		assert.Empty(t, m.name(), "the classic slot shows the global workspace")
		assert.Contains(t, m.errBox.String(), "home shares its config dir with the global workspace; showing global")
		require.NoError(t, m.checkSlotInvariant())
	})

	t.Run("a saved tab", func(t *testing.T) {
		_, reg := homeWorkspace(t)
		good := preservedTerminalWorkspace(t, "ws-good")
		require.NoError(t, reg.Add(good.Name, good.Path))
		require.NoError(t, reg.SetOpenWorkspaces([]string{"home", "ws-good"}))

		m := startupHome(t, &recordingExec{}, reg, "", "")

		assert.Equal(t, []string{"ws-good"}, m.slotNames())
		assert.Empty(t, m.failedOpen)
		assert.Contains(t, m.errBox.String(), "home shares its config dir with the global workspace; not restored as a tab")
		fresh, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		assert.Equal(t, []string{"ws-good"}, fresh.OpenWorkspaces, "the open list heals")
	})

	t.Run("the picker", func(t *testing.T) {
		def, _ := homeWorkspace(t)
		m := newRestoreHome(t, &recordingExec{})

		_, err := m.activateWorkspace(def)

		require.Error(t, err)
		assert.Equal(t, "home shares its config dir with the global workspace, which loom serves: pick Global", err.Error())
		assert.Empty(t, m.slots)
	})
}

// TestRegisterHome_ShowsTheGlobalWorkspace: registering the directory
// whose .loom is the global config dir (`loom ~`, with $HOME unregistered)
// gives the global workspace, which the model serves once and which is no
// tab. The TUI stays on global mode, or switches to it, saying why, rather
// than opening the global workspace as a tab named "".
func TestRegisterHome_ShowsTheGlobalWorkspace(t *testing.T) {
	isolateTmux(t)
	home := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		t.Setenv(config.EnvGlobalDir, filepath.Join(dir, ".loom"))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".loom"), 0o755))
		return dir
	}

	t.Run("from global mode: the startup prompt", func(t *testing.T) {
		dir := home(t)
		reg, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		m := startupHome(t, &recordingExec{}, reg, "", dir)
		require.Equal(t, stateConfirm, m.state, "fixture: the registration prompt")

		m.Update(m.pendingConfirmation.Run()())

		assert.Empty(t, m.slots, "no tab")
		assert.Empty(t, m.name(), "the global workspace shows")
		assert.Contains(t, m.errBox.String(), filepath.Base(dir)+" shares its config dir with the global workspace; showing global")
		require.NoError(t, m.checkSlotInvariant())
	})

	t.Run("from a tab", func(t *testing.T) {
		dir := home(t)
		a := preservedTerminalWorkspace(t, "ws-a")
		reg := savedRegistry(t, []string{"ws-a"}, "", a)
		m := startupHome(t, &recordingExec{}, reg, "", "")
		m.ctx = cancelledCtx()
		require.Equal(t, []string{"ws-a"}, m.slotNames())

		m.Update(registerWorkspaceMsg{name: "home", dir: dir})

		assert.Empty(t, m.slots, "switched to global mode")
		assert.Empty(t, m.name())
		assert.Contains(t, m.errBox.String(), "home shares its config dir with the global workspace; showing global")
		require.NoError(t, m.checkSlotInvariant())
	})
}

// TestStartupRestore_AllFail_ShowsTheGlobalWorkspace: when every
// workspace failed to restore, the user used to land in global mode over a
// never-loaded startup storage with an empty list, and the first save
// (here: quit) replaced its readable records with nothing. The model loads
// every workspace when it boots, whatever restores, so the fallback shows
// the global workspace's real sessions.
func TestStartupRestore_AllFail_ShowsTheGlobalWorkspace(t *testing.T) {
	isolateTmux(t)
	rec := &recordingExec{}
	m, statePath := restoreModeHome(t, rec,
		`[{"title":"keeper","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/wt-keeper"}}]`,
		corruptWorkspaces(t, "ws-bad-1", "ws-bad-2")...)

	require.Empty(t, m.slots)
	assert.Contains(t, listTitles(m), "keeper", "the global list must show its real sessions")

	_, _ = m.handleQuit()
	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"keeper"`, "quitting must not drop the global record")
}

// TestStartupRestore_AllFail_GlobalLoadFailsClosed: when the global
// storage is unreadable too, the fallback fails closed — the error is
// shown, nothing is written, and the user can still open a workspace.
func TestStartupRestore_AllFail_GlobalLoadFailsClosed(t *testing.T) {
	isolateTmux(t)
	rec := &recordingExec{}
	m, statePath := restoreModeHome(t, rec, `{"not":"an array"}`, corruptWorkspaces(t, "ws-bad")...)
	before, err := os.ReadFile(statePath)
	require.NoError(t, err)

	require.Empty(t, m.slots)
	assert.Empty(t, listTitles(m))
	assert.Contains(t, m.errBox.String(), "no workspace could be restored")

	_, _ = m.handleQuit()
	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the unreadable global state.json must be untouched")

	good := preservedTerminalWorkspace(t, "ws-good")
	registerWorkspaces(t, m, good)
	m.applyWorkspaceToggle([]config.Workspace{good})
	require.Len(t, m.slots, 1, "the user must still be able to open a workspace")
	assert.Equal(t, "ws-good", m.slots[0].wsCtx().Name)
}

// TestHandleQuit_LatchedFallbackQuitsAndKeepsOpenWorkspaces: after a
// fail-closed restore fallback, q used to refuse to quit forever — the
// latched storage refuses every save and the TUI never reloads it, so the
// "fix it and retry" rationale of the sticky quit cannot apply. It must
// quit, leave the unreadable file untouched, and keep the workspaces that
// failed to restore in the registry's open list so the next launch retries
// them (they were not closed by the user).
func TestHandleQuit_LatchedFallbackQuitsAndKeepsOpenWorkspaces(t *testing.T) {
	isolateTmux(t)
	m, statePath := restoreModeHome(t, &recordingExec{}, `{"not":"an array"}`, corruptWorkspaces(t, "ws-bad")...)
	before, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.Empty(t, m.slots)

	// A cancelled ctx makes handleError's toast Cmd return at once, so the
	// assertion below can run the Cmd whichever branch handleQuit takes.
	m.ctx = cancelledCtx()
	_, cmd := m.handleQuit()
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd(), "q must quit even though nothing can be saved")

	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the unreadable state.json must be untouched")
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-bad"}, fresh.OpenWorkspaces, "the failed workspace is retried on the next launch")
}

// TestStartupRestore_KeepsAFailedSavedTabAndPersistsIt: a saved tab that
// fails to open at startup is this TUI's to keep open (failedOpen): the
// restore persists it in the open list beside the tabs that opened, and the
// picker shows it checked.
func TestStartupRestore_KeepsAFailedSavedTabAndPersistsIt(t *testing.T) {
	isolateTmux(t)
	good := preservedTerminalWorkspace(t, "ws-good")
	bad := corruptWorkspaces(t, "ws-bad")[0]
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`, bad, good)

	assert.Equal(t, []string{"ws-good"}, m.slotNames())
	assert.Equal(t, []string{"ws-bad"}, m.failedOpen)
	fresh, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-good", "ws-bad"}, fresh.OpenWorkspaces, "the tabs, then the failed one")
	assert.True(t, m.pickerActiveNames()["ws-bad"])
}

// TestStartupHome_ShowsTheBootsNotices: the model boots before any client
// connects, so the notices its boot raised (the account registry's) come
// to the TUI by hand, and the error bar shows them.
func TestStartupHome_ShowsTheBootsNotices(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	model := core.NewForTest(core.Options{CmdExec: &recordingExec{}})
	model.Boot()
	client, stop, err := startTestCore(model)
	require.NoError(t, err)
	t.Cleanup(stop)

	m, err := startHome(context.Background(), client, stop,
		[]core.Event{core.Notice{Err: errors.New("accounts: the registry is unreadable")}}, "", "true", "", true)
	require.NoError(t, err)
	m.errBox.SetSize(400, 1)

	assert.Contains(t, m.errBox.String(), "the registry is unreadable")
}

// TestStartupHome_AWorkspaceThatWillNotLoad: started on a workspace whose
// load fails, with no saved tabs to restore, the TUI has nothing to show,
// and says why.
func TestStartupHome_AWorkspaceThatWillNotLoad(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	bad := corruptWorkspaces(t, "ws-bad")[0]
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Add(bad.Name, bad.Path))

	_, err = tryStartupHome(t, &recordingExec{}, reg, "ws-bad", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load instances: ")
}

// TestRegisterPendingDir_RegistryWriteRunsOnUpdate: confirming the startup
// "Register '<dir>' as workspace?" prompt used to run registry.Add inside the
// confirmation's Cmd, off the Update goroutine. The registry has no lock and
// Update reads and writes it (quit, tab switches), so the Cmd may only carry
// the request back; Update performs the Add and then activates the slot.
func TestRegisterPendingDir_RegistryWriteRunsOnUpdate(t *testing.T) {
	isolateTmux(t)
	t.Setenv("LOOM_HOME", t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	dir := t.TempDir()
	name := filepath.Base(dir)
	// A preserved workspace-terminal record under the workspace's name keeps
	// activation from starting a real terminal.
	rec, err := json.Marshal(map[string]any{
		"schema_version":        99,
		"title":                 name,
		"program":               "claude",
		"is_workspace_terminal": true,
		"worktree":              map[string]any{},
	})
	require.NoError(t, err)
	cfgDir := config.WorkspaceConfigDir(&config.Workspace{Name: name, Path: dir})
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName),
		[]byte(`{"instances":[`+string(rec)+`]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"true"}`), 0o644))

	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	m := startupHome(t, &recordingExec{}, reg, "", dir)
	require.Equal(t, stateConfirm, m.state, "a pending dir opens the registration prompt")

	cmd := m.pendingConfirmation.Run()
	require.NotNil(t, cmd)
	msg := cmd()
	assert.Empty(t, reg.Workspaces, "the confirmation Cmd must not write the registry")
	onDisk, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	assert.Empty(t, onDisk.Workspaces, "nor register the workspace on disk")

	m.Update(msg)
	ws := reg.FindByPath(dir)
	require.NotNil(t, ws, "Update registers the workspace")
	assert.Equal(t, name, ws.Name)
	assert.Equal(t, []string{name}, m.slotNames(), "and opens it as the focused tab")
	require.NoError(t, m.checkSlotInvariant())
}

// TestRestoreFailure_KeepsTheWorkspaceOpenUntilOpenedOrDeselected: a
// workspace that failed to restore is still one the user has open, but
// restore rewrote the registry's open list without it, and so did the next
// picker commit or quit, so the next launch never retried it (and, before
// the model served every workspace, its sweep killed the live agents). It
// must stay in the open list until it is opened or explicitly deselected.
func TestRestoreFailure_KeepsTheWorkspaceOpenUntilOpenedOrDeselected(t *testing.T) {
	isolateTmux(t)
	good := preservedTerminalWorkspace(t, "ws-good")
	bad := corruptWorkspaces(t, "ws-bad")[0]
	m, _ := restoreModeHome(t, &recordingExec{}, `[]`, good, bad)
	m.ctx = cancelledCtx()
	openList := func() []string {
		t.Helper()
		fresh, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		return fresh.OpenWorkspaces
	}

	require.Equal(t, []string{"ws-good"}, m.slotNames())
	assert.ElementsMatch(t, []string{"ws-good", "ws-bad"}, openList(), "restore keeps the failed workspace open")
	assert.True(t, m.pickerActiveNames()["ws-bad"], "the picker shows it still selected")
	_, _ = runOpenWorkspacePicker(m)
	require.NotNil(t, m.workspacePicker())
	m.workspacePicker().SetWidth(200)
	rendered := m.workspacePicker().Render()
	assert.Contains(t, rendered, "ws-bad (failed to load)", "labelled")
	assert.Contains(t, rendered, "stops retrying it at start", "with a warning that closing it ends the retries")
	m.dismissOverlay()
	m.state = stateDefault

	// A picker commit that keeps it selected retries it; it fails again.
	_ = m.applyWorkspaceToggle([]config.Workspace{good, bad})
	assert.ElementsMatch(t, []string{"ws-good", "ws-bad"}, openList(), "a picker commit keeps it open")

	_, _ = m.handleQuit()
	assert.ElementsMatch(t, []string{"ws-good", "ws-bad"}, openList(), "quit keeps it open")

	// Deselecting it in the picker is the explicit close.
	_ = m.applyWorkspaceToggle([]config.Workspace{good})
	assert.Equal(t, []string{"ws-good"}, openList())
	assert.False(t, m.pickerActiveNames()["ws-bad"])
}

// TestGlobalCommitFromGlobalMode_OnlyClosesFailedWorkspaces: committing
// the picker with nothing selected while already in global mode rebuilt
// the global slot from disk — a second attach client on every live session
// (then a release), crash restarts and orphan recovery all over again —
// and on a latched global storage failed with "staying in workspace mode",
// so a workspace that failed to restore could never be deselected. There
// is nothing to switch: the commit only closes the failed workspaces.
func TestGlobalCommitFromGlobalMode_OnlyClosesFailedWorkspaces(t *testing.T) {
	for _, tc := range []struct {
		name    string
		global  string
		latched bool
	}{
		{name: "loaded global storage", global: `[]`},
		{name: "latched global storage", global: `{"not":"an array"}`, latched: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			rec := &recordingExec{}
			m, _ := restoreModeHome(t, rec, tc.global, corruptWorkspaces(t, "ws-bad")...)
			m.ctx = cancelledCtx()

			require.Empty(t, m.slots)
			require.True(t, m.pickerActiveNames()["ws-bad"], "fixture: the failed workspace is still open")
			require.Equal(t, tc.latched, m.storage().WritesRefused())
			var live *session.Instance
			if !tc.latched { // a latched list stays empty (latchedStorageErr)
				live = liveInstance(t, "g-live")
				m.ws().AddForTest(live)
				m.syncViews()
				pointAt(m, live)
			}
			slot, list, storage := m.workspaceSlot, m.list, m.storage()
			rec.mu.Lock()
			rec.args = nil
			rec.mu.Unlock()
			m.errBox.Clear()

			drainCmd(m.applyWorkspaceToggle(nil))

			assert.Empty(t, m.failedOpen)
			assert.False(t, m.pickerActiveNames()["ws-bad"], "the failed workspace is closed")
			fresh, err := config.LoadWorkspaceRegistry()
			require.NoError(t, err)
			assert.Empty(t, fresh.OpenWorkspaces, "and gone from the registry's open list")
			assert.NotContains(t, m.errBox.String(), "staying in workspace mode")
			assert.Same(t, slot, m.workspaceSlot, "no reload: the global slot stays")
			assert.Same(t, list, m.list)
			assert.Same(t, storage, m.storage())
			assert.Empty(t, rec.args, "no reload: no reconcile, orphan discovery or hooks sweep")
			if live != nil {
				assert.Contains(t, listIDs(m.list), idOf(m, live))
				assert.Same(t, clientOf(t, live), m.panes.Get(live.Pane().TmuxSessionName()), "the same client stays attached")
			}
			require.NoError(t, m.checkSlotInvariant())
		})
	}
}
