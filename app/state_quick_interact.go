package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStateQuickInteractKey processes keys for the one-shot input
// bar. On Submit the text is sent to the agent or terminal pane (per
// the configured target); Cancel or a dead/paused instance drops the
// bar.
func handleStateQuickInteractKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.quickInputBar == nil {
		m.state = stateDefault
		return m, nil
	}

	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.WindowSize()
	}

	action := m.quickInputBar.HandleKeyPress(msg)
	switch action {
	case ui.QuickInputSubmit:
		text := m.quickInputBar.Value()
		var err error
		switch m.quickInputBar.Target {
		case ui.QuickInputTargetTerminal:
			err = m.splitPane.SendTerminalPrompt(text)
		case ui.QuickInputTargetAgent:
			err = selected.SendPrompt(text)
		}
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		if err != nil {
			return m, tea.Batch(tea.WindowSize(), m.handleError(err))
		}
		return m, tea.WindowSize()
	case ui.QuickInputCancel:
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.WindowSize()
	}
	return m, nil
}
