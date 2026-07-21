package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetSlot builds a fully-wired workspaceSlot (tempdir storage/state,
// own list + splitPane) so loadSlot/saveCurrentSlot never nil-deref.
// Shared across the fleet nav/teardown tests.
func fleetSlot(t *testing.T, name string, titles ...string) workspaceSlot {
	t.Helper()
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s)
	for _, ti := range titles {
		list.AddInstance(&session.Instance{Title: ti, Status: session.Ready})
	}
	dir := t.TempDir()
	st := config.LoadStateFrom(dir)
	stor, err := session.NewStorage(st, dir)
	require.NoError(t, err)
	return workspaceSlot{
		wsCtx:     &config.WorkspaceContext{Name: name, ConfigDir: dir},
		list:      list,
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   stor,
		appConfig: config.DefaultConfig(),
		appState:  st,
	}
}

// fleetHome wires a focused slot ("afocus", f1/f2) and a background peer
// ("bpeer", b1), with home's active fields hoisted from the focused slot.
func fleetHome(t *testing.T) *home {
	t.Helper()
	focus := fleetSlot(t, "afocus", "f1", "f2")
	peer := fleetSlot(t, "bpeer", "b1")
	peer.background = true
	m := &home{
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		viewMode:    viewOverview,
		focusedSlot: 0,
		overview:    ui.NewOverview(), // fleetOrder() reads m.overview.IsCollapsed
		tabBar:      ui.NewWorkspaceTabBar(),
		menu:        ui.NewMenu(),
		registry:    &config.WorkspaceRegistry{},
		slots:       []workspaceSlot{focus, peer},
		list:        focus.list,
		splitPane:   focus.splitPane,
		storage:     focus.storage,
		appConfig:   focus.appConfig,
		appState:    focus.appState,
	}
	m.seedOverviewCursor()
	return m
}

func TestMoveCursor_CrossesGroupBoundary(t *testing.T) {
	m := fleetHome(t)
	// Start at focused slot inst 0. Two forward steps: f2, then into bpeer/b1.
	assert.Equal(t, overviewCursor{slot: 0, inst: 0}, m.overviewCursor)
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 0, inst: 1}, m.overviewCursor)
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 1, inst: 0}, m.overviewCursor, "crossed into peer group")
	// Does not fall off the end.
	m.moveCursor(1)
	assert.Equal(t, overviewCursor{slot: 1, inst: 0}, m.overviewCursor)
	// Backward returns.
	m.moveCursor(-1)
	assert.Equal(t, overviewCursor{slot: 0, inst: 1}, m.overviewCursor)
}

func TestFocusCursorSlot_PromotesAndFocuses(t *testing.T) {
	m := fleetHome(t)
	// Cursor on the background peer slot.
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}

	m.focusCursorSlot()
	assert.Equal(t, 1, m.focusedSlot, "focus moved to cursor slot")
	assert.False(t, m.slots[1].background, "background slot promoted on focus")
	assert.Equal(t, m.slots[1].list, m.list, "focused list hoisted")
	assert.Equal(t, 0, m.list.SelectedIdx(), "cursor instance selected")
}
