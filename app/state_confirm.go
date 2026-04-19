package app

import (
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStateConfirmKey dispatches key events to the active
// ConfirmationOverlay. When the overlay closes (y/n/esc), the queued
// ConfirmationTask runs and state returns to default.
func handleStateConfirmKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cf := m.confirmation()
	if cf == nil {
		return m, nil
	}
	shouldClose := cf.HandleKeyPress(msg)
	if !shouldClose {
		return m, nil
	}
	cmd := m.pendingConfirmation.Run()
	m.pendingConfirmation = overlay.ConfirmationTask{}
	m.dismissOverlay()
	m.state = stateDefault
	return m, tea.Batch(cmd, m.instanceChanged())
}
