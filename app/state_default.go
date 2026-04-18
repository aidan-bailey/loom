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

	// q routes through handleQuit for the save-then-exit path. ctrl+c
	// was already handled at the top of this function as a panic-exit
	// backstop; Task 15 will migrate q into defaults.lua so this
	// short-circuit can disappear entirely.
	if msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		// Built-in keymap miss: give the script engine a chance to
		// claim the key. Scripts dispatch on the raw key string and
		// bypass ActionRegistry entirely — see app/app_scripts.go.
		if cmd, handled := m.dispatchScript(msg.String()); handled {
			return m, cmd
		}
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
