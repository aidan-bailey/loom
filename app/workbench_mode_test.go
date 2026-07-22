package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// newWorkbenchTestHome builds the bare home from newTestHome plus the
// workbench panel, wired to the split pane's terminal exactly like the
// real constructors do.
func newWorkbenchTestHome(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	m.workbench = ui.NewWorkbench(ui.NewDiffPane(), m.splitPane.Terminal())
	return m
}

// wbKey builds a KeyPressMsg for either a named key or a printable rune.
func wbKey(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	default:
		return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
}

// TestWorkbench_EnterFromFocus pins the entry path: enter in focus mode
// flips to workbench mode and force-hides the split's terminal (the
// panel owns the terminal tab now).
func TestWorkbench_EnterFromFocus(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	require.Equal(t, viewFocus, m.viewMode)
	require.False(t, m.splitPane.IsTerminalHidden())

	_, cmd := handleStateDefaultKey(m, wbKey("enter"))
	assert.Equal(t, viewWorkbench, m.viewMode)
	assert.True(t, m.splitPane.IsTerminalHidden(),
		"entering workbench must hide the split terminal")
	assert.NotNil(t, cmd, "entry must request a relayout")
}

// TestWorkbench_EscReturnsToFocus_RestoresTerminal pins the exit path:
// esc returns to focus mode and restores the terminal-hidden setting to
// whatever it was before entry — both polarities.
func TestWorkbench_EscReturnsToFocus_RestoresTerminal(t *testing.T) {
	for _, pre := range []bool{false, true} {
		m := newWorkbenchTestHome(t)
		mustAddInstance(t, m, "a")
		m.splitPane.SetTerminalHidden(pre)

		_, _ = handleStateDefaultKey(m, wbKey("enter"))
		require.Equal(t, viewWorkbench, m.viewMode)
		require.True(t, m.splitPane.IsTerminalHidden())

		_, _ = handleStateDefaultKey(m, wbKey("esc"))
		assert.Equal(t, viewFocus, m.viewMode)
		assert.Equal(t, pre, m.splitPane.IsTerminalHidden(),
			"terminal-hidden must be restored to its pre-entry value (%v)", pre)
	}
}

// TestWorkbench_TabGoesToOverview pins tab as the workbench→overview
// hop (mirroring focus-mode tab), restoring the terminal on the way out.
func TestWorkbench_TabGoesToOverview(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")

	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)

	_, _ = handleStateDefaultKey(m, wbKey("tab"))
	assert.Equal(t, viewOverview, m.viewMode)
	assert.False(t, m.splitPane.IsTerminalHidden(),
		"leaving workbench must restore the terminal setting")
}

// TestWorkbench_NumberKeysSwitchTabs pins the panel-tab keys: 1-4
// select tabs directly, and d is a diff alias (matching the focus-mode
// diff mnemonic).
func TestWorkbench_NumberKeysSwitchTabs(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	require.Equal(t, ui.WbTabMarkdown, m.workbench.Tab())

	_, _ = handleStateDefaultKey(m, wbKey("2"))
	assert.Equal(t, ui.WbTabDiff, m.workbench.Tab())

	_, _ = handleStateDefaultKey(m, wbKey("4"))
	assert.Equal(t, ui.WbTabTerminal, m.workbench.Tab())

	_, _ = handleStateDefaultKey(m, wbKey("d"))
	assert.Equal(t, ui.WbTabDiff, m.workbench.Tab(), "d is the diff-tab alias")
}

// TestWorkbench_NonWhitelistedKeysNoOp pins the key gate: layout keys
// addressing the hidden focus chrome (rail toggle, terminal toggle,
// list-index jumps) neither dispatch nor leave workbench mode.
func TestWorkbench_NonWhitelistedKeysNoOp(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)

	for _, k := range []string{"\\", "T", "K", "J"} {
		_, cmd := handleStateDefaultKey(m, wbKey(k))
		assert.Equal(t, viewWorkbench, m.viewMode,
			"key %q must not leave workbench mode", k)
		assert.Nil(t, cmd, "non-whitelisted key %q must not dispatch in workbench", k)
	}
}

// TestWorkbench_SlotSwitchCleansUp pins the v1 rule that workbench mode
// does not survive an implicit workspace slot switch: the departing
// slot's terminal-hidden setting is restored, any in-progress markdown
// edit is canceled, and the mode drops out of workbench — the exact
// saveCurrentSlot → loadSlot sequence every switch path (workspace nav
// keys, picker toggle, cross-workspace jumps) runs.
func TestWorkbench_SlotSwitchCleansUp(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")

	stateB := config.LoadStateFrom(t.TempDir())
	storageB, err := session.NewStorage(stateB, t.TempDir())
	require.NoError(t, err)
	listB := ui.NewList(&m.spinner)
	splitB := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	m.slots = []workspaceSlot{
		{
			wsCtx:     &config.WorkspaceContext{Name: "ws-a", ConfigDir: t.TempDir()},
			storage:   m.storage,
			appConfig: m.appConfig,
			appState:  m.appState,
			list:      m.list,
			splitPane: m.splitPane,
			workbench: m.workbench,
		},
		{
			wsCtx:     &config.WorkspaceContext{Name: "ws-b", ConfigDir: t.TempDir()},
			storage:   storageB,
			appConfig: config.DefaultConfig(),
			appState:  stateB,
			list:      listB,
			splitPane: splitB,
			workbench: ui.NewWorkbench(ui.NewDiffPane(), splitB.Terminal()),
		},
	}
	m.focusedSlot = 0
	m.activeCtx = m.slots[0].wsCtx

	departingSplit := m.splitPane
	departingWb := m.workbench
	require.False(t, departingSplit.IsTerminalHidden(), "terminal visible before entry")

	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	require.True(t, departingSplit.IsTerminalHidden())

	// Start a markdown edit so cleanup has something to cancel.
	departingWb.Markdown.SetDocument("notes.md", "# hi", time.Now())
	require.True(t, departingWb.Markdown.StartEdit())
	require.True(t, departingWb.Markdown.Editing())

	// The choke-point sequence every implicit switch path runs.
	m.saveCurrentSlot()
	m.loadSlot(1)

	assert.NotEqual(t, viewWorkbench, m.viewMode,
		"workbench must not survive a slot switch")
	assert.False(t, departingSplit.IsTerminalHidden(),
		"departing slot's terminal-hidden must be restored to its pre-entry value")
	assert.False(t, departingWb.Markdown.Editing(),
		"in-progress markdown edit must be canceled")
	assert.Same(t, m.slots[1].workbench, m.workbench,
		"target slot's workbench must be live after the switch")
}

// TestWorkbench_WheelOverRightPanelScrollsMarkdown pins the mouse-wheel
// routing split: wheel events at/past wbLeftWidth scroll the active
// right-panel tab (markdown here) instead of the hidden focus split.
func TestWorkbench_WheelOverRightPanelScrollsMarkdown(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	require.Equal(t, ui.WbTabMarkdown, m.workbench.Tab())

	m.wbLeftWidth = 40
	m.workbench.SetSize(40, 10) // small body so a big document overflows and can scroll
	var doc strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&doc, "line %d\n", i)
	}
	m.workbench.Markdown.SetDocument("/tmp/doc.md", doc.String(), time.Now())
	require.Equal(t, 0, m.workbench.Markdown.Scroll())

	_, cmd := m.Update(tea.MouseWheelMsg{X: 50, Y: 5, Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
	assert.Equal(t, 1, m.workbench.Markdown.Scroll(),
		"wheel-down over the right panel must scroll the markdown pane")

	_, cmd = m.Update(tea.MouseWheelMsg{X: 50, Y: 5, Button: tea.MouseWheelUp})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, m.workbench.Markdown.Scroll(),
		"wheel-up over the right panel must scroll the markdown pane back")
}

// TestWorkbench_WheelOverLeftHalfDoesNotScrollMarkdown pins the other
// side of the split: wheel events left of wbLeftWidth are routed to the
// agent pane via the split machinery, never to the right-panel tab.
func TestWorkbench_WheelOverLeftHalfDoesNotScrollMarkdown(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)

	m.wbLeftWidth = 40
	m.workbench.SetSize(40, 10)
	var doc strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&doc, "line %d\n", i)
	}
	m.workbench.Markdown.SetDocument("/tmp/doc.md", doc.String(), time.Now())
	require.Equal(t, 0, m.workbench.Markdown.Scroll())

	_, cmd := m.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, m.workbench.Markdown.Scroll(),
		"wheel events left of wbLeftWidth must not touch the right-panel tab")
}

// TestWorkbench_WheelOverTerminalTabNoOps pins the v1 simplification:
// the terminal tab shares scroll state with the hidden focus split, so
// a wheel over the right panel while it's active must not touch the
// (invisible-to-the-user) split terminal scroll.
func TestWorkbench_WheelOverTerminalTabNoOps(t *testing.T) {
	m := newWorkbenchTestHome(t)
	mustAddInstance(t, m, "a")
	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	m.workbench.SetTab(ui.WbTabTerminal)
	m.wbLeftWidth = 40
	m.workbench.SetSize(40, 10)

	_, cmd := m.Update(tea.MouseWheelMsg{X: 50, Y: 5, Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
	assert.Equal(t, viewWorkbench, m.viewMode, "wheel over the terminal tab must not disturb workbench mode")
}

// TestWorkbench_EnterWithNoInstanceNoOps pins the empty-list guard:
// with nothing selected there is no session to deep-dive, so enter
// stays in focus mode.
func TestWorkbench_EnterWithNoInstanceNoOps(t *testing.T) {
	m := newWorkbenchTestHome(t)
	require.Nil(t, m.list.GetSelectedInstance())

	_, cmd := handleStateDefaultKey(m, wbKey("enter"))
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Nil(t, cmd)
}
