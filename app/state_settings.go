package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// handleStateSettingsKey drives the settings overlay. Every key press
// may report a field change; when it does, the change is persisted to
// disk and the two home fields that shadow appConfig (m.program,
// m.autoYes) are refreshed so new-instance creation picks up the new
// values immediately instead of using a stale cached copy.
func handleStateSettingsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	so := m.settingsOverlay()
	if so == nil {
		return m, nil
	}

	closed, changed := so.HandleKeyPress(msg)
	if err := so.TakeError(); err != nil {
		return m, m.handleError(err)
	}

	if changed {
		if m.activeCtx != nil {
			if err := config.SaveConfigTo(m.appConfig, m.activeCtx.ConfigDir); err != nil {
				return m, m.handleError(fmt.Errorf("save settings: %w", err))
			}
		}
		m.program = m.appConfig.GetProgram()
		m.autoYes = m.appConfig.AutoYes
	}

	if closed {
		m.dismissOverlay()
		m.state = stateDefault
	}
	return m, nil
}
