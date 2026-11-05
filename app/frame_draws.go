package app

import "github.com/aidan-bailey/loom/core"

// drawsPane reports whether the layout render draws can show session's
// pane output; v is the session's row, nil for a terminal session
// (docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md §5). It
// leans toward drawing: a wrong true costs a shared frame, a wrong false
// a pane frozen on screen.
func (m *home) drawsPane(session string, v *core.InstanceView) bool {
	layout := m.layout()
	if layout == viewWork || layout == viewFeedback {
		return false // full-screen views with no pane
	}
	if v == nil {
		// Only the current terminal is drawn: by the split in focus, and
		// by the workbench's terminal tab.
		return layout != viewOverview && session != "" && session == m.splitPane.CurrentTerminalSessionName()
	}
	if layout == viewOverview {
		return true // every open workspace's row has a card
	}
	if selected := m.list.GetSelectedInstance(); selected != nil && v.ID == selected.ID {
		return true
	}
	return layout == viewFocus && !m.railHidden && m.listShows(v.ID)
}

// listShows reports whether the focused slot's list has the row id: the
// rail draws its rows, scrolled out of its window or not.
func (m *home) listShows(id core.InstanceID) bool {
	for _, row := range m.list.GetInstances() {
		if row.ID == id {
			return true
		}
	}
	return false
}
