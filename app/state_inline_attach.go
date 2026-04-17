package app

import (
	"claude-squad/log"
	"claude-squad/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStateInlineAttachKey forwards raw key bytes to the focused tmux
// pane while the inline attach is active. ctrl+q exits; a dead pane or
// paused instance drops attach and returns to the default state.
func handleStateInlineAttachKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		m.splitPane.SetInlineAttach(false)
		m.splitPane.SetFocusedPane(ui.FocusAgent)
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.WindowSize()
	}

	// ctrl+q exits inline attach
	if msg.Type == tea.KeyCtrlQ {
		m.splitPane.SetInlineAttach(false)
		m.splitPane.SetFocusedPane(ui.FocusAgent)
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.WindowSize()
	}

	// Convert key to bytes and forward to the focused pane's tmux session
	b := keyMsgToBytes(msg)
	if b != nil {
		var err error
		if m.splitPane.GetFocusedPane() == ui.FocusTerminal {
			err = m.splitPane.SendTerminalKeysRaw(b)
		} else {
			err = selected.SendKeysRaw(b)
		}
		if err != nil {
			log.ErrorLog.Printf("inline attach send error: %v", err)
		}
	}
	return m, nil
}
