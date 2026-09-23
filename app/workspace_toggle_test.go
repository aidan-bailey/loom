package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingInstanceStorage counts SaveInstances calls so tests can
// observe whether home.applyWorkspaceToggle persisted state before
// mutating the slot configuration.
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

// TestApplyWorkspaceToggle_ClassicToGlobalPersists is the smaller of
// the two leak-fix tests. From a classic workspace slot, empty desired
// triggers enterGlobalMode (which replaces the slot) after the leak-fix's
// preemptive save, so the only SaveInstances call that hits the test
// recorder is the one the bug was missing. (From global mode there is no
// transition: see TestGlobalCommitFromGlobalMode_OnlyClosesFailedWorkspaces.)
func TestApplyWorkspaceToggle_ClassicToGlobalPersists(t *testing.T) {
	// LOOM_GLOBAL_DIR redirects enterGlobalMode's reconstruction of
	// global storage away from the real ~/.loom — tests must not write
	// to the user's home dir.
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	rec := &recordingInstanceStorage{}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)

	h := &home{
		workspaceSlot: &workspaceSlot{
			appConfig: config.DefaultConfig(),
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			storage:   storage,
			wsCtx:     &config.WorkspaceContext{Name: "classic-ws", ConfigDir: t.TempDir()},
		},
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
		// registry = nil, slots = nil — classic mode.,
	}

	require.Equal(t, 0, rec.calls, "no save calls before invoke")

	// Empty desired triggers classic → global with enterGlobalMode.
	_ = h.applyWorkspaceToggle(nil)
	require.NotNil(t, h.wsCtx)
	require.Empty(t, h.wsCtx.Name, "fixture: the transition ran")

	assert.GreaterOrEqual(t, rec.calls, 1,
		"global storage must be saved at least once during transition (leak-fix regression)")
}

// TestApplyWorkspaceToggle_GlobalToWorkspacePersists is the precise
// regression test for the user-reported bug: switching from global
// mode to a workspace tab via the picker silently dropped the in-
// memory list. We verify the leak-fix's save call happens BEFORE the
// downstream activation work runs (which we don't actually require to
// succeed in tests — tmux/git side effects are out of scope).
func TestApplyWorkspaceToggle_GlobalToWorkspacePersists(t *testing.T) {
	t.Setenv("LOOM_HOME", t.TempDir())

	rec := &recordingInstanceStorage{}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)

	h := &home{
		workspaceSlot: &workspaceSlot{
			appConfig: config.DefaultConfig(),
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			storage:   storage,
		},
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
		// Keep activation off tmux entirely: a recording executor, and a
		// workspace whose terminal record already exists (preserved), so
		// no workspace terminal is created and started.
		cmdExec: &recordingExec{},
	}

	// Non-empty desired forces the bug's actual code path:
	// len(m.slots)==0 → leak-fix → activate → loadSlot. Whether
	// activateWorkspace succeeds is irrelevant for this test — the
	// invariant under test is "the save call happens unconditionally
	// before activation."
	desired := []config.Workspace{preservedTerminalWorkspace(t, "test-ws")}
	_ = h.applyWorkspaceToggle(desired)

	assert.GreaterOrEqual(t, rec.calls, 1,
		"global m.list must be saved before activateWorkspace runs (leak-fix regression — pre-fix this was 0)")
}

// TestEnterGlobalMode_SetsGlobalCtxAndClearsSlots verifies the post-
// conditions of enterGlobalMode: workspace tabs are gone, the active
// context is the global one (no name, no repo path), and storage points
// at the global config dir.
func TestEnterGlobalMode_SetsGlobalCtxAndClearsSlots(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)

	h := &home{
		workspaceSlot: &workspaceSlot{
			appConfig: config.DefaultConfig(),
			list:      list,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			wsCtx:     &config.WorkspaceContext{Name: "stale-ws"},
		},
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
		// registry = nil so the SetOpenWorkspaces side effect is skipped.,
	}

	h.enterGlobalMode()

	assert.Empty(t, h.slots, "slots must be cleared")
	require.NotNil(t, h.wsCtx, "the global slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *h.wsCtx)
	assert.NotNil(t, h.storage, "storage must be reconstructed for global cfgDir")
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

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)
	split := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())

	h := &home{
		workspaceSlot: &workspaceSlot{
			appConfig: config.DefaultConfig(),
			list:      list,
			splitPane: split,
			workbench: ui.NewWorkbench(ui.NewDiffPane(), split.Terminal()),
			wsCtx:     &config.WorkspaceContext{Name: "stale-ws"},
		},
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	}
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

// TestEnterGlobalMode_WithSlots_PersistsAndDeactivates verifies the
// workspace → global transition properly fires deactivateWorkspace
// (which saves each slot's instances via slot.storage) before the
// slot is dropped. The pre-fix path didn't iterate slots in
// enterGlobalMode at all; this test guards against regressing to a
// version that drops slots without persisting.
func TestEnterGlobalMode_WithSlots_PersistsAndDeactivates(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	// Two slots, each with its own recording storage. Each one has
	// to receive a SaveInstances call before being dropped.
	slotRecA := &recordingInstanceStorage{}
	storageA, err := session.NewStorage(slotRecA, t.TempDir())
	require.NoError(t, err)
	slotARecListings := ui.NewList(&s)

	slotRecB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(slotRecB, t.TempDir())
	require.NoError(t, err)
	slotBRecListings := ui.NewList(&s)

	h := &home{
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	}
	focusSlots(h, 0,
		&workspaceSlot{
			wsCtx:     &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()},
			storage:   storageA,
			appConfig: config.DefaultConfig(),
			list:      slotARecListings,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		},
		&workspaceSlot{
			wsCtx:     &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()},
			storage:   storageB,
			appConfig: config.DefaultConfig(),
			list:      slotBRecListings,
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		},
	)

	h.enterGlobalMode()

	assert.GreaterOrEqual(t, slotRecA.calls, 1, "slot ws-a must be persisted before dropping")
	assert.GreaterOrEqual(t, slotRecB.calls, 1, "slot ws-b must be persisted before dropping")
	assert.Empty(t, h.slots, "all slots dropped after enterGlobalMode")
	require.NotNil(t, h.wsCtx)
	assert.Equal(t, globalDir, h.wsCtx.ConfigDir)
	require.NoError(t, h.checkSlotInvariant())
}

// TestEnterGlobalMode_LoadFailureLeavesWorkspaceModeIntact is the
// regression guard for the global-list wipe: enterGlobalMode used to tear
// down every workspace slot first, then log a failed global load and carry
// on with an empty list — whose next save rewrote the global state.json
// with nothing. The global load now runs before anything is torn down, and
// a failure must leave the slots, storage and global state.json untouched.
// (The tabs are saved first — before the load, whose side effects an
// abort couldn't undo — which closes nothing and costs nothing.) Driven
// through applyWorkspaceToggle(nil), enterGlobalMode's only caller (the
// picker's Global row), so the path is the real one.
func TestEnterGlobalMode_LoadFailureLeavesWorkspaceModeIntact(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, globalDir)
	statePath := filepath.Join(globalDir, config.StateFileName)
	corrupt := []byte(`{"help_screens_seen":0,"instances":{"not":"an array"}}`)
	require.NoError(t, os.WriteFile(statePath, corrupt, 0o644))

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	recA := &recordingInstanceStorage{}
	storageA, err := session.NewStorage(recA, t.TempDir())
	require.NoError(t, err)
	listA := ui.NewList(&s)
	recB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(recB, t.TempDir())
	require.NoError(t, err)

	split := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	ctxA := &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()}
	h := &home{
		ctx:    context.Background(),
		state:  stateDefault,
		menu:   ui.NewMenu(),
		tabBar: ui.NewWorkspaceTabBar(),
		errBox: ui.NewErrBox(),
	}
	focusSlots(h, 0,
		&workspaceSlot{wsCtx: ctxA, storage: storageA, appConfig: config.DefaultConfig(), list: listA, splitPane: split},
		&workspaceSlot{
			wsCtx:     &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()},
			storage:   storageB,
			appConfig: config.DefaultConfig(),
			list:      ui.NewList(&s),
			splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		},
	)
	h.errBox.SetSize(400, 1)

	cmd := h.applyWorkspaceToggle(nil)

	assert.NotNil(t, cmd, "the failure must be surfaced, not just logged")
	assert.Contains(t, h.errBox.String(), "global")
	require.Len(t, h.slots, 2, "no workspace slot may be deactivated")
	assert.Same(t, ctxA, h.wsCtx, "still in workspace mode")
	assert.Same(t, storageA, h.storage, "storage must not be swapped for the unreadable global one")
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
	m.cmdExec = &recordingExec{}

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	require.NoError(t, m.checkSlotInvariant())
	assert.NotNil(t, m.list.GetInstanceByTitle("in-global"), "global mode shows the global dir's sessions")
	assert.Nil(t, m.list.GetInstanceByTitle("in-loom-home"))
	for _, inst := range m.list.GetInstances() {
		assert.False(t, inst.IsWorkspaceTerminal, "global mode has no repo, so no workspace terminal")
	}
	require.NotNil(t, m.wsCtx, "the global slot carries the global context")
	assert.Equal(t, config.WorkspaceContext{ConfigDir: globalDir}, *m.wsCtx)
	assert.Equal(t, globalDir, m.configDir(), "sessions created in global mode get the global config dir")
	entries, err := os.ReadDir(loomHome)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing is written to LOOM_HOME")

	require.NoError(t, m.storage.SaveInstances(persistableInstances(m.list.GetInstances())))
	raw, err := os.ReadFile(filepath.Join(globalDir, config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "in-global", "global mode saves back to the global dir")
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

	drainCmd(m.applyWorkspaceToggle(nil))

	require.Empty(t, m.slots)
	assert.Contains(t, m.errBox.String(), "could not be read by this version", "the global load reports its recovery summary")
}
