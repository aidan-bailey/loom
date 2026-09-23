package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typeTitle types title into the stateNew title entry.
func typeTitle(t *testing.T, m *home, title string) {
	t.Helper()
	for _, r := range title {
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// assertMenuResetOnUpdate: the flow must leave the menu out of its
// creation/prompt state by the time the handler returns — i.e. on the
// Update goroutine — not in a Cmd that races View on ui.Menu. The Cmd is
// then run concurrently with a render so -race sees any leftover write.
func assertMenuResetOnUpdate(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	st := m.menu.State()
	assert.NotEqual(t, ui.StateNewInstance, st, "menu still in the new-instance state after the handler returned")
	assert.NotEqual(t, ui.StatePrompt, st, "menu still in the prompt state after the handler returned")
	done := make(chan struct{})
	go func() { drainCmd(cmd); close(done) }()
	_ = m.menu.String()
	<-done
}

// TestCancelPaths_SetMenuStateOnUpdate: the new-instance cancel paths and
// the help dismissal reset the menu inside a tea.Sequence closure, which
// Bubble Tea runs on its own goroutine, concurrently with View.
func TestCancelPaths_SetMenuStateOnUpdate(t *testing.T) {
	t.Run("ctrl+c while naming", func(t *testing.T) {
		m := newTestHome(t)
		_, _ = runNewInstance(m)
		_, cmd := handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		assertMenuResetOnUpdate(t, m, cmd)
	})
	t.Run("esc while naming", func(t *testing.T) {
		m := newTestHome(t)
		_, _ = runNewInstance(m)
		_, cmd := handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
		assertMenuResetOnUpdate(t, m, cmd)
	})
	t.Run("launch options cancelled", func(t *testing.T) {
		m := newTestHome(t)
		_, _ = runNewInstance(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, stateLaunchOptions, m.state)
		_, cmd := m.cancelLaunchOptions()
		assertMenuResetOnUpdate(t, m, cmd)
	})
	t.Run("prompt overlay cancelled", func(t *testing.T) {
		m := newTestHome(t)
		_, _ = runPromptNewInstance(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, statePrompt, m.state)
		cmd := m.cancelPromptOverlay()
		assertMenuResetOnUpdate(t, m, cmd)
	})
	t.Run("help dismissed", func(t *testing.T) {
		m := newTestHome(t)
		_, _ = runShowHelp(m)
		require.Equal(t, stateHelp, m.state)
		m.menu.SetState(ui.StatePrompt) // make a stale state observable
		_, cmd := m.handleHelpState(tea.KeyPressMsg{Code: tea.KeyEsc})
		assertMenuResetOnUpdate(t, m, cmd)
	})
}
