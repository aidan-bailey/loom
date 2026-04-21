package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleStateFileExplorerKey forwards keys to the active
// FileExplorerOverlay. When the overlay signals close (Esc or a
// successful Enter), state reverts to stateDefault and the returned
// cmd (typically the $EDITOR tea.ExecProcess) is batched with a
// preview refresh so the UI paints cleanly while the editor runs.
func handleStateFileExplorerKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fe := m.fileExplorer()
	if fe == nil {
		return m, nil
	}
	closed, cmd := fe.HandleKey(msg)
	if !closed {
		return m, cmd
	}
	m.dismissOverlay()
	m.state = stateDefault
	if cmd == nil {
		return m, m.instanceChanged()
	}
	return m, tea.Batch(cmd, m.instanceChanged())
}
