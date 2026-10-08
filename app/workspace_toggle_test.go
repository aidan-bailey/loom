package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingInstanceStorage records SaveInstances calls, standing in for a
// workspace's state.json.
type recordingInstanceStorage struct {
	calls    int
	lastData json.RawMessage
}

func (r *recordingInstanceStorage) SaveInstances(data json.RawMessage) error {
	r.calls++
	r.lastData = data
	return nil
}

func (r *recordingInstanceStorage) GetInstances() json.RawMessage { return r.lastData }
func (r *recordingInstanceStorage) DeleteAllInstances() error     { return nil }

// TestEnterGlobalMode_SetsGlobalCtxAndClearsSlots verifies the post-
// conditions of enterGlobalMode: workspace tabs are gone, the active
// context is the global one (no name, no repo path), and storage points
// at the global config dir.
func TestEnterGlobalMode_SetsGlobalCtxAndClearsSlots(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	ws := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "stale-ws"}, Config: config.DefaultConfig()})
	list := fixtureList(t)

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
		// registry = nil so the SetOpenWorkspaces side effect is skipped.,
	})
	bootFixture(t, h) // the global workspace, served from boot

	h.enterGlobalMode()

	assert.Empty(t, h.slots, "slots must be cleared")
	require.NotNil(t, h.wsCtx(), "the global slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *h.wsCtx())
	assert.NotNil(t, h.storage(), "storage must be reconstructed for global cfgDir")
	assert.NotNil(t, h.list, "list must be reset to a fresh ui.List")
}

// TestEnterGlobalMode_CleansUpWorkbench guards the picker escape hatch
// (W → deselect-all) that reaches enterGlobalMode without passing the
// leaveFocusedSlot/loadSlot choke points: workbench residue (wbRatio,
// force-hidden split terminal) must be cleaned up before the slots are
// dropped, or handleQuit later flushes the stale ratio into the wrong
// (global) state.json.
func TestEnterGlobalMode_CleansUpWorkbench(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	ws := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "stale-ws"}, Config: config.DefaultConfig()})
	list := fixtureList(t)
	split := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())

	h := wireCore(t, &home{
		workspaceSlot: slotWith(ws, &workspaceSlot{
			list:      list,
			splitPane: split,
			workbench: ui.NewWorkbench(ui.NewDiffPane(), split.Terminal()),
		}),
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	})
	bootFixture(t, h) // the global workspace, served from boot
	// Simulate an active workbench: terminal force-hidden, non-default ratio.
	h.viewMode = viewWorkbench
	h.wbPrevTerminalHidden = false
	h.splitPane.SetTerminalHidden(true)
	h.wbRatio = 0.7

	h.enterGlobalMode()

	assert.NotEqual(t, viewWorkbench, h.viewMode,
		"workbench must not survive the workspace → global transition")
	assert.False(t, h.splitPane.IsTerminalHidden(),
		"the force-hidden split terminal must be restored")
	assert.Zero(t, h.wbRatio,
		"wbRatio residue must be cleared so handleQuit can't flush it into global state.json")
}

// TestEnterGlobalMode_WithSlots_Deactivates verifies the workspace →
// global transition drops every slot and lands on the global workspace.
// Nothing needs saving first: the model keeps serving the workspaces whose
// tabs closed (daemon stage 3A), so no unsaved state is dropped with them.
func TestEnterGlobalMode_WithSlots_Deactivates(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	// Two slots, each with its own recording storage.
	slotRecA := &recordingInstanceStorage{}
	storageA, err := session.NewStorage(slotRecA, t.TempDir())
	require.NoError(t, err)
	wsA := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()}, Storage: storageA, Config: config.DefaultConfig()})
	slotARecListings := fixtureList(t)

	slotRecB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(slotRecB, t.TempDir())
	require.NoError(t, err)
	wsB := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()}, Storage: storageB, Config: config.DefaultConfig()})
	slotBRecListings := fixtureList(t)

	h := &home{
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	}
	focusSlots(h, 0,
		slotWith(wsA, &workspaceSlot{
			list:      slotARecListings,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
		slotWith(wsB, &workspaceSlot{
			list:      slotBRecListings,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
	)
	wireCore(t, h)
	bootFixture(t, h) // the global workspace, served from boot

	h.enterGlobalMode()

	assert.Empty(t, h.slots, "all slots dropped after enterGlobalMode")
	require.NotNil(t, h.wsCtx())
	assert.Equal(t, globalDir, h.wsCtx().ConfigDir)
	require.NoError(t, h.checkSlotInvariant())
}

// TestEnterGlobalMode_LoadFailureLeavesWorkspaceModeIntact is the
// regression guard for the global-list wipe: enterGlobalMode used to tear
// down every workspace slot first, then log a failed global load and carry
// on with an empty list — whose next save rewrote the global state.json
// with nothing. A failure must leave the slots, storage and global
// state.json untouched. (The model serves the global workspace from boot;
// entering global mode opens it, which retries a load that failed, and
// aborts if it fails again.) Driven
// through applyWorkspaceToggle(nil), enterGlobalMode's only caller (the
// picker's Global row), so the path is the real one.
func TestEnterGlobalMode_LoadFailureLeavesWorkspaceModeIntact(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	statePath := filepath.Join(globalDir, config.StateFileName)
	corrupt := []byte(`{"help_screens_seen":0,"instances":{"not":"an array"}}`)
	require.NoError(t, os.WriteFile(statePath, corrupt, 0o644))

	recA := &recordingInstanceStorage{}
	storageA, err := session.NewStorage(recA, t.TempDir())
	require.NoError(t, err)
	recB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(recB, t.TempDir())
	require.NoError(t, err)

	split := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	ctxA := &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()}
	wsA := testWS(core.WorkspaceParts{Ctx: ctxA, Storage: storageA, Config: config.DefaultConfig()})
	listA := fixtureList(t)
	wsB := testWS(core.WorkspaceParts{Ctx: &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()}, Storage: storageB, Config: config.DefaultConfig()})
	h := &home{
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	}
	focusSlots(h, 0,
		slotWith(wsA, &workspaceSlot{list: listA, splitPane: split}),
		slotWith(wsB, &workspaceSlot{
			list:      fixtureList(t),
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		}),
	)
	wireCore(t, h)
	bootFixture(t, h) // its global load fails, latched
	h.errBox.SetSize(400, 1)

	cmd := h.applyWorkspaceToggle(nil)

	assert.NotNil(t, cmd, "the failure must be surfaced, not just logged")
	assert.Contains(t, h.errBox.String(), "global")
	require.Len(t, h.slots, 2, "no workspace slot may be deactivated")
	assert.Same(t, ctxA, h.wsCtx(), "still in workspace mode")
	assert.Same(t, storageA, h.storage(), "storage must not be swapped for the unreadable global one")
	assert.Same(t, listA, h.list)
	require.NoError(t, h.checkSlotInvariant())

	got, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, corrupt, got, "the global state.json must be untouched")
}

// TestEnterGlobalMode_LoadsTheGlobalDir: startup's global context is
// config.GlobalWorkspaceContext (LOOM_GLOBAL_DIR), but enterGlobalMode
// resolved config.GetConfigDir (LOOM_HOME), so where the two differ — the
// loomdev sandbox — W → Global loaded, and wrote loom-context files and
// swept hooks and orphans in, another directory than startup's. The global
// slot also had a nil context, so sessions created in it got ConfigDir ""
// and no subagent hooks.
func TestEnterGlobalMode_LoadsTheGlobalDir(t *testing.T) {
	isolateTmux(t)
	globalDir, loomHome := t.TempDir(), t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	t.Setenv("LOOM_HOME", loomHome)
	paused := func(title string) []byte {
		return []byte(`{"instances":[{"title":"` + title + `","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/loom-test-` + title + `"}}]}`)
	}
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.StateFileName), paused("in-global"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(loomHome, config.StateFileName), paused("in-loom-home"), 0o644))
	m := fleetHome(t)
	m.ctx = cancelledCtx()
	m.errBox = ui.NewErrBox()
	testModel(m).SetExecForTest(&recordingExec{})
	bootFixture(t, m) // the global workspace, served from boot

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	require.NoError(t, m.checkSlotInvariant())
	assert.NotNil(t, m.list.GetInstanceByTitle("in-global"), "global mode shows the global dir's sessions")
	assert.Nil(t, m.list.GetInstanceByTitle("in-loom-home"))
	for _, inst := range m.list.GetInstances() {
		assert.False(t, inst.IsWorkspaceTerminal, "global mode has no repo, so no workspace terminal")
	}
	require.NotNil(t, m.wsCtx(), "the global slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *m.wsCtx())
	assert.Equal(t, globalDir, m.configDir(), "sessions created in global mode get the global config dir")
	entries, err := os.ReadDir(loomHome)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing is written to LOOM_HOME")

	require.NoError(t, m.storage().SaveInstances(core.Persistable(m.ws().InstancesForTest())))
	raw, err := os.ReadFile(filepath.Join(globalDir, config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "in-global", "global mode saves back to the global dir")
}

// TestEnterGlobalMode_OrphanPlaceholdersUseTheGlobalProgram: the shared
// loader gave Recoverable orphan placeholders the program the process
// started with, possibly a workspace's, rather than the program of the
// config the workspace loaded. Recovering one then relaunched it with
// another workspace's agent.
func TestEnterGlobalMode_OrphanPlaceholdersUseTheGlobalProgram(t *testing.T) {
	isolateTmux(t)
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.ConfigFileName),
		[]byte(`{"default_program":"global-agent"}`), 0o644))
	// A dirty orphan worktree under the global dir: surfaced as Recoverable.
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	orphan := filepath.Join(globalDir, "worktrees", "u", "dirty_18be000000000002")
	runGit(t, repo, "worktree", "add", "-b", "u/dirty", orphan)
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "UNSAVED.txt"), []byte("wip"), 0o644))

	m := fleetHome(t)
	m.ctx = cancelledCtx()
	m.errBox = ui.NewErrBox()
	// The model's own program is the one the process started with (-p, or
	// a workspace's): what the loader must not give the placeholders.
	m.core = testLoop(t, core.NewForTest(core.Options{Registry: &config.WorkspaceRegistry{}, Program: "startup-agent"}))
	wireCore(t, m)
	bootFixture(t, m) // the global workspace, served from boot

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	placeholder := m.list.GetInstanceByTitle("dirty")
	require.NotNil(t, placeholder, "fixture: the orphan surfaces inline")
	require.Equal(t, session.Recoverable, placeholder.Status)
	assert.Equal(t, "global-agent", placeholder.Program)
}

// TestEnterGlobalMode_LoadsLikeStartup: enterGlobalMode ran only
// LoadAndReconcile — no crash restarts, no inline orphan recovery and no
// recovery summary — unlike every other workspace-load path. Here the
// global payload holds a record this binary can't decode; the startup
// loader reports it, as it does on every other path.
func TestEnterGlobalMode_LoadsLikeStartup(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.StateFileName),
		[]byte(`{"instances":[{"schema_version":99,"title":"from-the-future","program":"claude","worktree":{}}]}`), 0o644))
	m := fleetHome(t)
	m.ctx = cancelledCtx()
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(400, 1)
	bootFixture(t, m) // the global workspace, served from boot

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	assert.Contains(t, m.errBox.String(), "could not be read by this version", "the global load reports its recovery summary")
}
