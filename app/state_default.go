package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleStateDefaultKey processes keys while the list is in its normal
// (no-overlay) state. ctrl+c is a hard-reserved panic exit. Esc gets
// first crack at dismissing the diff or exiting scroll mode. Every
// remaining key is routed through the Lua engine via dispatchScript
// — ActionRegistry and the GlobalKeyStringsMap lookup have been
// retired in favor of defaults.lua.
func handleStateDefaultKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c is a panic-exit backstop. Evaluated BEFORE any engine
	// dispatch or handler so a broken or malicious user script that
	// unbinds or shadows ctrl+c still can't trap the user in the
	// TUI. Skips handleQuit's save path intentionally: ctrl+c is for
	// the case where something's gone wrong and the user wants out
	// now, not a clean shutdown.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
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

	if cmd, handled := m.dispatchScript(msg.String()); handled {
		return m, cmd
	}
	return m, nil
}
