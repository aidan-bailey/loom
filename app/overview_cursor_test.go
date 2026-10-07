package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetSlot builds a fully-wired workspaceSlot (tempdir storage/state,
// own list, splitPane and workbench) so loadSlot/leaveFocusedSlot never
// nil-deref.
// Shared across the fleet nav/teardown tests.
func fleetSlot(t *testing.T, name string, titles ...string) *workspaceSlot {
	t.Helper()
	dir := t.TempDir()
	st := config.LoadStateFrom(dir)
	stor, err := session.NewStorage(st, dir)
	require.NoError(t, err)
	ws := testWS(core.WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: name, ConfigDir: dir},
		Storage: stor,
		Config:  config.DefaultConfig(),
		State:   st,
	})
	for _, ti := range titles {
		ws.Add(&session.Instance{Title: ti, Status: session.Ready})
	}
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	return &workspaceSlot{
		ws:        ws,
		list:      ui.NewList(&s, ws),
		splitPane: sp,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
	}
}

// fleetHome wires a focused slot ("afocus", f1/f2) and a non-focused
// peer ("bpeer", b1).
func fleetHome(t *testing.T) *home {
	t.Helper()
	focus := fleetSlot(t, "afocus", "f1", "f2")
	peer := fleetSlot(t, "bpeer", "b1")
	m := &home{
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		viewMode: viewOverview,
		overview: ui.NewOverview(), // fleetOrder() reads m.overview.IsCollapsed
		tabBar:   ui.NewWorkspaceTabBar(),
		menu:     ui.NewMenu(),
		core:     core.NewForTest(core.Options{Registry: &config.WorkspaceRegistry{}}),
	}
	focusSlots(m, 0, focus, peer)
	m.seedOverviewCursor()
	return wireCore(t, wirePanes(t, m))
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

func TestFocusCursorSlot_Focuses(t *testing.T) {
	m := fleetHome(t)
	// Cursor on the non-focused peer slot.
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}

	m.focusCursorSlot()
	assert.Equal(t, 1, m.focusedSlot, "focus moved to cursor slot")
	assert.Same(t, m.slots[1], m.workspaceSlot, "cursor slot is the focused slot")
	assert.Equal(t, 0, m.list.SelectedIdx(), "cursor instance selected")
	require.NoError(t, m.checkSlotInvariant())
}
