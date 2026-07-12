package app

import (
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// doubleEscWindow is how quickly two Esc presses must occur to exit interact
// mode. A single Esc is forwarded to the agent (Claude uses Esc heavily); a
// quick second Esc returns to nav.
const doubleEscWindow = 500 * time.Millisecond

// exitInteract leaves interact (focus-to-interact) mode and returns to nav.
func exitInteract(m *home) (tea.Model, tea.Cmd) {
	m.splitPane.SetInlineAttach(false)
	m.setPaneFocus(ui.FocusAgent)
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)
	m.lastEscAt = time.Time{}
	return m, tea.RequestWindowSize
}

// focusedPaneAlive reports whether the tmux session backing the currently
// focused pane is still alive. Inline attach forwards keystrokes to whichever
// pane has focus (agent or terminal) — each pane is backed by its own,
// independent tmux session, so liveness must be checked against THAT
// session. Checking only the agent's session (regardless of focus) misses a
// terminal-pane session that died out from under it (e.g. the user's shell
// exited or crashed): tmux tears the session down as soon as its wrapped
// program exits, printing a final "[exited]" into the pane, and further
// keystrokes would silently vanish into the dead PTY forever instead of
// dropping back to nav.
func focusedPaneAlive(m *home, selected *session.Instance) bool {
	if m.splitPane.GetFocusedPane() == ui.FocusTerminal {
		return m.splitPane.TerminalTmuxSession() != nil
	}
	return selected.TmuxAlive()
}

// handleStateInlineAttachKey forwards raw key bytes to the focused tmux pane
// while interact (focus-to-interact) mode is active. Exit: ctrl+q, or a
// double-Esc. A dead pane or paused instance drops the mode and returns to nav.
func handleStateInlineAttachKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
		return exitInteract(m)
	}

	switch msg.String() {
	case "ctrl+q":
		return exitInteract(m)
	case "esc":
		// Double-Esc exits; a single Esc still reaches the agent (forwarded below).
		if !m.lastEscAt.IsZero() && time.Since(m.lastEscAt) < doubleEscWindow {
			return exitInteract(m)
		}
		m.lastEscAt = time.Now()
	default:
		m.lastEscAt = time.Time{} // any other key resets the pending Esc
	}

	// Convert key to bytes and forward to the focused pane's tmux session.
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
