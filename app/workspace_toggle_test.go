package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"github.com/charmbracelet/bubbles/spinner"
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

// TestApplyWorkspaceToggle_GlobalToGlobalPersists is the smaller of
// the two leak-fix tests. Empty desired triggers enterGlobalMode after
// the leak-fix's preemptive save, so the only SaveInstances call that
// hits the test recorder is the one the bug was missing.
func TestApplyWorkspaceToggle_GlobalToGlobalPersists(t *testing.T) {
	// LOOM_HOME redirects enterGlobalMode's reconstruction of global
	// storage away from the real ~/.loom — tests must not write to
	// the user's home dir.
	t.Setenv("LOOM_HOME", t.TempDir())

	rec := &recordingInstanceStorage{}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   storage,
		tabBar:    ui.NewWorkspaceTabBar(),
		errBox:    ui.NewErrBox(),
		// registry = nil, slots = nil — global mode.
	}

	require.Equal(t, 0, rec.calls, "no save calls before invoke")

	// Empty desired triggers global → global with enterGlobalMode.
	_ = h.applyWorkspaceToggle(nil)

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
	list := ui.NewList(&s, false)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   storage,
		tabBar:    ui.NewWorkspaceTabBar(),
		errBox:    ui.NewErrBox(),
	}

	// Non-empty desired forces the bug's actual code path:
	// len(m.slots)==0 → leak-fix → activate → loadSlot. Whether
	// activateWorkspace succeeds is irrelevant for this test — the
	// invariant under test is "the save call happens unconditionally
	// before activation."
	desired := []config.Workspace{
		{Name: "test-ws", Path: t.TempDir()},
	}
	_ = h.applyWorkspaceToggle(desired)

	assert.GreaterOrEqual(t, rec.calls, 1,
		"global m.list must be saved before activateWorkspace runs (leak-fix regression — pre-fix this was 0)")
}

// TestEnterGlobalMode_ClearsActiveCtxAndSlots verifies the post-
// conditions of enterGlobalMode: workspace tabs are gone, the active
// context flips to nil (signaling global mode), and storage points at
// the global config dir.
func TestEnterGlobalMode_ClearsActiveCtxAndSlots(t *testing.T) {
	t.Setenv("LOOM_HOME", t.TempDir())

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		tabBar:    ui.NewWorkspaceTabBar(),
		errBox:    ui.NewErrBox(),
		activeCtx: &config.WorkspaceContext{Name: "stale-ws"},
		// registry = nil so the SetOpenWorkspaces side effect is skipped.
	}

	h.enterGlobalMode()

	assert.Empty(t, h.slots, "slots must be cleared")
	assert.Nil(t, h.activeCtx, "activeCtx must be nil in global mode")
	assert.NotNil(t, h.storage, "storage must be reconstructed for global cfgDir")
	assert.NotNil(t, h.list, "list must be reset to a fresh ui.List")
}

// TestEnterGlobalMode_WithSlots_PersistsAndDeactivates verifies the
// workspace → global transition properly fires deactivateWorkspace
// (which saves each slot's instances via slot.storage) before the
// slot is dropped. The pre-fix path didn't iterate slots in
// enterGlobalMode at all; this test guards against regressing to a
// version that drops slots without persisting.
func TestEnterGlobalMode_WithSlots_PersistsAndDeactivates(t *testing.T) {
	t.Setenv("LOOM_HOME", t.TempDir())

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	// Two slots, each with its own recording storage. Each one has
	// to receive a SaveInstances call before being dropped.
	slotRecA := &recordingInstanceStorage{}
	storageA, err := session.NewStorage(slotRecA, t.TempDir())
	require.NoError(t, err)
	slotARecListings := ui.NewList(&s, false)

	slotRecB := &recordingInstanceStorage{}
	storageB, err := session.NewStorage(slotRecB, t.TempDir())
	require.NoError(t, err)
	slotBRecListings := ui.NewList(&s, false)

	h := &home{
		ctx:         context.Background(),
		state:       stateDefault,
		appConfig:   config.DefaultConfig(),
		list:        slotARecListings,
		menu:        ui.NewMenu(),
		splitPane:   ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		tabBar:      ui.NewWorkspaceTabBar(),
		errBox:      ui.NewErrBox(),
		activeCtx:   &config.WorkspaceContext{Name: "ws-a"},
		focusedSlot: 0,
		slots: []workspaceSlot{
			{
				wsCtx:     &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()},
				storage:   storageA,
				appConfig: config.DefaultConfig(),
				list:      slotARecListings,
				splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			},
			{
				wsCtx:     &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()},
				storage:   storageB,
				appConfig: config.DefaultConfig(),
				list:      slotBRecListings,
				splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			},
		},
	}

	h.enterGlobalMode()

	assert.GreaterOrEqual(t, slotRecA.calls, 1, "slot ws-a must be persisted before dropping")
	assert.GreaterOrEqual(t, slotRecB.calls, 1, "slot ws-b must be persisted before dropping")
	assert.Empty(t, h.slots, "all slots dropped after enterGlobalMode")
	assert.Nil(t, h.activeCtx)
}
