package app

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/ui"

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

// applySettingsChange applies to this TUI a change to the focused
// workspace's settings that reached it from the model (another TUI saved
// them): a new theme is painted at once, as the settings overlay paints
// its own, and a new default program becomes the one this TUI's drafts
// default to (m.program), as this TUI's own save makes it. Only a change
// does anything: this TUI's own save rereads the view (syncWorkspaces), so
// the WorkspacesChanged it brings finds none.
func (m *home) applySettingsChange(old, cur config.Settings) {
	if theme := cur.GetTheme(); theme != old.GetTheme() {
		if !ui.ApplyTheme(theme) && theme != "" {
			log.For("ui").Warn("unknown_theme", "name", theme, "fallback", ui.DefaultThemeName)
		}
	}
	if program := cur.GetProgram(); program != old.GetProgram() {
		m.program = program
	}
}
