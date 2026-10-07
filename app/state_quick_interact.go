package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// handleStateQuickInteractKey processes keys for the one-shot input
// bar. On Submit the text is sent to the agent or terminal pane (per
// the configured target); Cancel or a dead/paused instance drops the
// bar.
func handleStateQuickInteractKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quickInputBar == nil {
		m.state = stateDefault
		return m, nil
	}

	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !m.tmuxAlive(selected) {
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.RequestWindowSize
	}

	action := m.quickInputBar.HandleKeyPress(msg)
	switch action {
	case ui.QuickInputSubmit:
		text := m.quickInputBar.Value()
		var err error
		var send tea.Cmd
		switch m.quickInputBar.Target {
		case ui.QuickInputTargetTerminal:
			err = m.splitPane.SendTerminalPrompt(text)
		case ui.QuickInputTargetAgent:
			// Off the Update goroutine (three tmux subprocesses and a
			// pause); a failed send comes back as an error.
			// The instance, through the bridge until package C sends by
			// request.
			if inst := m.instOf(selected.ID); inst != nil {
				send = coreCmd(m.core.SendPromptInst(inst, text))
			}
		}
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		if err != nil {
			return m, tea.Batch(tea.RequestWindowSize, m.handleError(err))
		}
		return m, tea.Batch(tea.RequestWindowSize, send)
	case ui.QuickInputCancel:
		m.quickInputBar = nil
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.RequestWindowSize
	}
	return m, nil
}
