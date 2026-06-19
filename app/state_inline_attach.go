package app

import (
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// handleStateInlineAttachKey forwards raw key bytes to the focused tmux
// pane while the inline attach is active. ctrl+q exits; a dead pane or
// paused instance drops attach and returns to the default state.
func handleStateInlineAttachKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		m.splitPane.SetInlineAttach(false)
		m.splitPane.SetFocusedPane(ui.FocusAgent)
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.RequestWindowSize
	}

	// ctrl+q exits inline attach
	if msg.String() == "ctrl+q" {
		m.splitPane.SetInlineAttach(false)
		m.splitPane.SetFocusedPane(ui.FocusAgent)
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, tea.RequestWindowSize
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
			log.For("app").Error("inline_attach.send_failed", "err", err)
		}
	}
	return m, nil
}
