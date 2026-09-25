package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
)

// handleStateSettingsKey drives the settings overlay. Every key press
// may report a field change; when it does, the change is persisted to
// disk and the home field that shadows appConfig (m.program) is
// refreshed so new-instance creation picks up the new value immediately
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
		// Save beside the config the focused slot loaded: its context's
		// dir, the global dir in global mode. A nil context (bare test
		// homes) loaded from the default dir, so save there — otherwise
		// the change lives only in memory and silently vanishes on
		// restart.
		dir := ""
		if m.wsCtx != nil {
			dir = m.wsCtx.ConfigDir
		} else if globalDir, err := config.GetConfigDir(); err == nil {
			dir = globalDir
		}
		if dir != "" {
			if err := config.SaveConfigTo(m.appConfig, dir); err != nil {
				return m, m.handleError(fmt.Errorf("save settings: %w", err))
			}
		}
		m.program = m.appConfig.GetProgram()
		// Re-sync the loom-context toggle so an in-place change takes
		// effect on the next session launch without a workspace switch.
		session.SetLoomContextEnabled(m.appConfig.LoomContextEnabled())
		session.SetSubagentTrackingEnabled(m.appConfig.SubagentTrackingEnabled())
	}

	if closed {
		m.dismissOverlay()
		m.state = stateDefault
	}
	return m, tea.Batch(cmds...)
}
