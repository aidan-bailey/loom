package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// Pane events, Sent from pump/timer goroutines via tea.Program.Send and
// handled exclusively on the Update goroutine (no model mutation off it).

// paneDirtyMsg: a session's pane emitted output (coalesced ≤ ~60/s).
type paneDirtyMsg struct{ session string }

// paneQuietMsg: a session's pane settled (500ms with no output).
type paneQuietMsg struct{ session string }

// ptyDeadMsg: a session's output pump exited on a genuine PTY EOF.
type ptyDeadMsg struct{ session string }

// bellMsg: a session's pane rang BEL.
type bellMsg struct{ session string }

// instanceForSession resolves a tmux session name (as carried by pane
// events) to the owning instance across every workspace slot, or nil for
// terminal-pane sessions and unknown names.
func (m *home) instanceForSession(name string) *session.Instance {
	if name == "" {
		return nil
	}
	check := func(l *ui.List) *session.Instance {
		for _, inst := range l.GetInstances() {
			if inst.TmuxSessionName() == name {
				return inst
			}
		}
		return nil
	}
	if len(m.slots) > 0 {
		for i, slot := range m.slots {
			l := slot.list
			if i == m.focusedSlot {
				l = m.list
			}
			if inst := check(l); inst != nil {
				return inst
			}
		}
		return nil
	}
	return check(m.list)
}

// markDirty records that a session produced output since the last health
// tick — the tick uses this to gate diff-stat refreshes.
func (m *home) markDirty(sessionName string) {
	if m.dirtySessions == nil {
		m.dirtySessions = make(map[string]bool)
	}
	m.dirtySessions[sessionName] = true
}

// takeDirty returns the dirty-set and resets it for the next tick window.
func (m *home) takeDirty() map[string]bool {
	d := m.dirtySessions
	m.dirtySessions = make(map[string]bool)
	return d
}
