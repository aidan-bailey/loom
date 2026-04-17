package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// Compile-time assertions that the four app-level overlay types
// satisfy the Overlay interface. If any of these start failing,
// adapter methods need updating — pinpoints the regression to a
// single overlay rather than the app package that consumes them.
var (
	_ Overlay = (*ConfirmationOverlay)(nil)
	_ Overlay = (*TextOverlay)(nil)
	_ Overlay = (*TextInputOverlay)(nil)
	_ Overlay = (*WorkspacePicker)(nil)
)

func TestConfirmationTaskRunSyncBeforeAsync(t *testing.T) {
	order := []string{}
	task := ConfirmationTask{
		Sync: func() { order = append(order, "sync") },
		Async: func() tea.Msg {
			order = append(order, "async")
			return nil
		},
	}

	cmd := task.Run()
	assert.Equal(t, []string{"sync"}, order, "Sync should run immediately")

	if cmd != nil {
		cmd()
	}
	assert.Equal(t, []string{"sync", "async"}, order, "Async runs only when the returned cmd is dispatched")
}

func TestConfirmationTaskZeroValueIsNoOp(t *testing.T) {
	var task ConfirmationTask
	cmd := task.Run()
	assert.Nil(t, cmd, "Zero-value task emits no cmd")
}

func TestConfirmationTaskNilSyncStillDispatchesAsync(t *testing.T) {
	called := false
	task := ConfirmationTask{
		Async: func() tea.Msg {
			called = true
			return nil
		},
	}
	cmd := task.Run()
	assert.NotNil(t, cmd)
	cmd()
	assert.True(t, called, "Async still fires when Sync is nil")
}
