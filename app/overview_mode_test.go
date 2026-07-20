package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/script"
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
// keys still dispatch. List-index jumps (K/J/g/G) are blocked too:
// they address list order, which is incoherent over the sorted grid.
func TestOverviewKeyWhitelist_BlocksFocusOnlyKeys(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewOverview

	// Bound in defaults.lua but focus-only: d (diff), g/G/K/J (list jumps).
	for _, k := range []string{"d", "g", "G", "K", "J"} {
		_, cmd := handleStateDefaultKey(m, tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		assert.Nil(t, cmd, "non-whitelisted key %q must not dispatch in overview", k)
	}

	// "j" (cursor down) is whitelisted and bound.
	_, cmd := handleStateDefaultKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.NotNil(t, cmd, "whitelisted key must dispatch in overview")
}

// flattenCmdMsgs executes cmd and recursively flattens tea.BatchMsg so
// tests can assert on the leaf messages regardless of batching shape.
func flattenCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenCmdMsgs(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestOverviewNewInstance_DropsToFocusFirst pins the n/N interception:
// creating a session from the grid would collect the title blind (the
// inline title affordance lives in the focus layout), so n/N flip to
// focus mode — persisted — and then still dispatch the create flow
// through the script engine.
func TestOverviewNewInstance_DropsToFocusFirst(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewOverview
	require.NoError(t, m.appState.SetUIPrefs(config.UIPrefs{ViewMode: "overview"}))

	_, cmd := handleStateDefaultKey(m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	assert.Equal(t, viewFocus, m.viewMode, "n must drop overview back to focus")
	assert.Equal(t, "", m.appState.GetUIPrefs().ViewMode, "the focus drop must persist")

	require.NotNil(t, cmd)
	var done *scriptDoneMsg
	for _, msg := range flattenCmdMsgs(cmd) {
		if d, ok := msg.(scriptDoneMsg); ok {
			done = &d
			break
		}
	}
	require.NotNil(t, done, "the create flow must still dispatch through the script engine")
	require.NoError(t, done.err)
	require.Len(t, done.pendingIntents, 1)
	_, ok := done.pendingIntents[0].intent.(script.NewInstanceIntent)
	assert.True(t, ok, "expected NewInstanceIntent, got %T", done.pendingIntents[0].intent)
}

// TestOverviewBell_ClearedOnFocusNotGridCursor pins the "seen in the
// grid ≠ attended" semantic: walking the overview cursor onto a
// bell-pending card (instanceChanged fires after every script dispatch)
// must not eat the attention signal — the sort order would reshuffle
// under the cursor. Dropping into focus on it (enter) is what clears it.
func TestOverviewBell_ClearedOnFocusNotGridCursor(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewOverview

	a := mustAddInstance(t, m, "a")
	b := mustAddInstance(t, m, "b")
	b.SetBellPending(true)

	// Selection starts on "a"; walk onto "b" (bell → attention tier,
	// overview position 0) the way a j/k dispatch does: moveCursor,
	// then the unconditional instanceChanged from handleScriptDone.
	require.Same(t, a, m.list.GetSelectedInstance())
	m.moveCursor(-1)
	require.Same(t, b, m.list.GetSelectedInstance())
	_ = m.instanceChanged()
	assert.True(t, b.BellPending(), "overview cursor landing must not clear the bell")

	// Enter drops to focus on the card — now it is attended.
	_, _ = handleStateDefaultKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, viewFocus, m.viewMode)
	assert.False(t, b.BellPending(), "entering focus on the card must clear the bell")
}

// TestOverviewMouse_Ignored pins the v1 no-mouse-in-overview rule: a
// click that would begin a drag-selection over the (hidden) focus
// layout is swallowed without touching drag state.
func TestOverviewMouse_Ignored(t *testing.T) {
	m := newTestHome(t)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.viewMode = viewOverview
	mustAddInstance(t, m, "a")

	_, cmd := m.Update(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	assert.False(t, m.dragging, "click in overview must not begin a drag")

	_, _ = m.Update(tea.MouseMotionMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	assert.Nil(t, cmd, "release in overview must not copy a selection")
	assert.False(t, m.dragging)
}

// TestApplyUIPrefs_RestoresViewMode pins the startup restore: a
// persisted "overview" pref lands the app in overview mode, anything
// else in focus mode.
func TestApplyUIPrefs_RestoresViewMode(t *testing.T) {
	m := newTestHome(t)

	require.NoError(t, m.appState.SetUIPrefs(config.UIPrefs{ViewMode: "overview"}))
	m.applyUIPrefs()
	assert.Equal(t, viewOverview, m.viewMode)

	require.NoError(t, m.appState.SetUIPrefs(config.UIPrefs{ViewMode: ""}))
	m.applyUIPrefs()
	assert.Equal(t, viewFocus, m.viewMode)
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
