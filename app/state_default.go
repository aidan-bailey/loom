package app

import (
	"claude-squad/keys"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStateDefaultKey processes keys while the list is in its normal
// (no-overlay) state. Esc gets first chance to dismiss the diff or exit
// scroll mode before falling through to ActionRegistry dispatch; quit
// and unknown keys are handled inline.
func handleStateDefaultKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		// Dismiss diff overlay first
		if m.splitPane.IsDiffVisible() {
			m.splitPane.ToggleDiff()
			return m, m.instanceChanged()
		}
		// Exit agent scroll mode
		if m.splitPane.IsAgentInScrollMode() {
			selected := m.list.GetSelectedInstance()
			err := m.splitPane.ResetAgentToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// Exit terminal scroll mode
		if m.splitPane.IsTerminalInScrollMode() {
			m.splitPane.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	// Route through the action registry. Every TUI keybinding lives in
	// one of the sub-registries merged into defaultActions; an
	// unhandled key here means the press was truly unbound (e.g., an
	// ignored modifier combo).
	if model, cmd, handled := m.actions.Dispatch(name, m); handled {
		return model, cmd
	}
	return m, nil
}
