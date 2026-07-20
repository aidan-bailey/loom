package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToggleOverview_ScriptHostPersistsViewMode drives the real
// scriptHost.ToggleOverview through the deferModelMutation → drain →
// handleScriptDone pipeline (the same path a tab press takes after the
// engine dispatch) against a real appState, asserting the mode flips
// and the ViewMode pref persists — then flips back to focus and
// persists the empty string, matching the enter/esc paths in
// state_default.go (persistence symmetry).
func TestToggleOverview_ScriptHostPersistsViewMode(t *testing.T) {
	m := newTestHome(t)
	require.Equal(t, viewFocus, m.viewMode)

	toggle := func() {
		host := &scriptHost{m: m}
		host.ToggleOverview()
		_, _, _, actions := host.drain()
		require.Len(t, actions, 1, "ToggleOverview must defer exactly one model mutation")
		_ = m.handleScriptDone(scriptDoneMsg{pendingActions: actions})
	}

	toggle()
	assert.Equal(t, viewOverview, m.viewMode)
	assert.Equal(t, "overview", m.appState.GetUIPrefs().ViewMode,
		"entering overview must persist the mode")

	toggle()
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Equal(t, "", m.appState.GetUIPrefs().ViewMode,
		"returning to focus must persist the empty mode (same value enter/esc write)")
}

// TestOverviewEnterEsc_ReturnToFocusAndPersist pins the state_default
// routing: enter and esc both drop overview back to focus mode and
// persist ViewMode="" — identical to what ToggleOverview writes when
// leaving overview, so the two exit paths cannot drift apart.
func TestOverviewEnterEsc_ReturnToFocusAndPersist(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestHome(t)
			m.viewMode = viewOverview
			require.NoError(t, m.appState.SetUIPrefs(config.UIPrefs{ViewMode: "overview"}))

			_, _ = handleStateDefaultKey(m, tc.msg)
			assert.Equal(t, viewFocus, m.viewMode)
			assert.Equal(t, "", m.appState.GetUIPrefs().ViewMode)
		})
	}
}

// TestOverviewKeyWhitelist_BlocksFocusOnlyKeys verifies that keys
// outside overviewKeyAllowed never reach the script engine in overview
// mode (they would act on the hidden focus layout), while whitelisted
// keys still dispatch.
func TestOverviewKeyWhitelist_BlocksFocusOnlyKeys(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewOverview

	// "d" (toggle diff) is bound in defaults.lua but not whitelisted.
	_, cmd := handleStateDefaultKey(m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	assert.Nil(t, cmd, "non-whitelisted key must not dispatch in overview")

	// "j" (cursor down) is whitelisted and bound.
	_, cmd = handleStateDefaultKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.NotNil(t, cmd, "whitelisted key must dispatch in overview")
}

// TestMoveCursor_OverviewWalksSortedOrder builds a list whose
// attention-sorted overview order differs from list order and asserts
// moveCursor walks the sorted order (skipping Deleting) without
// wrapping at either end. List order: a(Ready), b(Prompting),
// c(Deleting), d(Running). Overview order (see ui.SortForOverview):
// b(attention) → d(running) → a(ready) → c(deleting, skipped).
func TestMoveCursor_OverviewWalksSortedOrder(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewOverview

	a := mustAddInstance(t, m, "a")
	b := mustAddInstance(t, m, "b")
	c := mustAddInstance(t, m, "c")
	d := mustAddInstance(t, m, "d")
	require.NoError(t, b.TransitionTo(session.Prompting))
	require.NoError(t, c.TransitionTo(session.Deleting))
	require.NoError(t, d.TransitionTo(session.Running))

	items := m.list.GetInstances()
	require.Equal(t, []int{1, 3, 0, 2}, ui.SortForOverview(items),
		"precondition: sorted order must differ from list order")

	// Selection starts at list index 0 (= "a", overview position 2).
	require.Equal(t, 0, m.list.SelectedIdx())

	// Down from "a": next overview position is "c" (Deleting) — skipped,
	// and there is nothing after it, so no wrap: selection stays.
	m.moveCursor(1)
	assert.Same(t, a, m.list.GetSelectedInstance(), "Deleting is skipped and the grid does not wrap")

	// Up from "a" walks the sorted order backwards: d, then b.
	m.moveCursor(-1)
	assert.Same(t, d, m.list.GetSelectedInstance())
	m.moveCursor(-1)
	assert.Same(t, b, m.list.GetSelectedInstance())

	// Up from the first overview position: no wrap.
	m.moveCursor(-1)
	assert.Same(t, b, m.list.GetSelectedInstance())

	// Focus mode keeps plain list-order navigation (List.Down also
	// skips Deleting, so b → d, passing over c).
	m.viewMode = viewFocus
	m.list.SetSelectedInstance(0)
	m.moveCursor(1)
	assert.Same(t, b, m.list.GetSelectedInstance(), "focus mode walks list order")
	m.moveCursor(1)
	assert.Same(t, d, m.list.GetSelectedInstance(), "focus mode walks list order past Deleting")
}
