package app

import (
	"context"
	"testing"

	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/session"
	"claude-squad/ui"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHome(t *testing.T) *home {
	t.Helper()
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)
	return &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		actions:   defaultActions(),
	}
}

func TestDispatchReturnsFalseForUnregisteredKey(t *testing.T) {
	h := newTestHome(t)
	_, _, handled := h.actions.Dispatch(keys.KeyHelp, h)
	assert.False(t, handled,
		"KeyHelp is not yet migrated — the legacy switch should see it")
}

func TestDispatchPreconditionBlocksRunWhenFalse(t *testing.T) {
	h := newTestHome(t)
	// Empty list: no instance selected → KeyUp's precondition rejects.
	_, cmd, handled := h.actions.Dispatch(keys.KeyUp, h)
	assert.True(t, handled, "registered keys are always handled — precondition only gates Run")
	assert.Nil(t, cmd, "precondition failure yields no cmd")
}

func TestDispatchRunsWhenPreconditionPasses(t *testing.T) {
	h := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = h.list.AddInstance(instance)

	_, _, handled := h.actions.Dispatch(keys.KeyDiff, h)
	assert.True(t, handled)
}

func TestSelectedNotBusyRejectsLoadingAndDeleting(t *testing.T) {
	h := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = h.list.AddInstance(instance)

	instance.SetStatus(session.Loading)
	assert.False(t, selectedNotBusy(h), "Loading should block")

	instance.SetStatus(session.Deleting)
	assert.False(t, selectedNotBusy(h), "Deleting should block")

	instance.SetStatus(session.Running)
	assert.True(t, selectedNotBusy(h), "Running is a normal state")
}

func TestDefaultActionsCoversExpectedKeys(t *testing.T) {
	reg := defaultActions()
	for _, k := range []keys.KeyName{keys.KeyUp, keys.KeyDown, keys.KeyDiff} {
		_, ok := reg[k]
		assert.True(t, ok, "expected key %v to be in the registry", k)
	}
}

// TestActionDispatchDoesNotRegressNavigation is a smoke test: the nav
// keys migrated away from the switch should still move the list
// cursor end-to-end via handleKeyPress.
//
// Note: handleKeyPress runs the menu-highlighting protocol first,
// which swallows the first call (sets keySent=true) and handles the
// actual keypress on the second. The test replays each press twice
// to match the real runtime loop.
func TestActionDispatchDoesNotRegressNavigation(t *testing.T) {
	h := newTestHome(t)
	a, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	b, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	_ = h.list.AddInstance(a)
	_ = h.list.AddInstance(b)
	h.list.SetSelectedInstance(0)

	press := func(msg tea.KeyMsg) {
		_, _ = h.handleKeyPress(msg)
		_, _ = h.handleKeyPress(msg)
	}

	press(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, "b", h.list.GetSelectedInstance().Title, "KeyDown should advance the cursor")

	press(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, "a", h.list.GetSelectedInstance().Title, "KeyUp should retreat the cursor")
}
