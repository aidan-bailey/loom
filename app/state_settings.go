package app

import (
	tea "charm.land/bubbletea/v2"
)

// handleStateSettingsKey drives the settings overlay. Every key press
// may report a field change; when it does, the overlay's copy of the
// settings goes to the model (core.Model.SaveSettings), which persists it,
// and this TUI's drafts default to the saved program from then on
// (m.program), so new-instance creation picks up the new value immediately
// instead of using a stale cached copy.
func handleStateSettingsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	so := m.settingsOverlay()
	if so == nil {
		return m, nil
	}

	closed, changed := so.HandleKeyPress(msg)
	if err := so.TakeError(); err != nil {
		return m, m.handleError(err)
	}
	var cmds []tea.Cmd
	if req, ok := so.TakeAccountRequest(); ok {
		cmds = append(cmds, m.handleAccountRequest(req))
	}

	if changed {
		// The model saves config.json beside the workspace's state (the
		// global dir in global mode) and applies the change at once: the
		// launch toggles (core.Model.SaveSettings).
		settings := m.settingsEdit.Snapshot()
		if err := m.core.SaveSettings(m.id, settings); err != nil {
			return m, m.handleError(err)
		}
		m.program = settings.GetProgram()
		m.syncWorkspaces()
	}

	if closed {
		m.settingsEdit = nil
		m.dismissOverlay()
		m.state = stateDefault
	}
	return m, tea.Batch(cmds...)
}
