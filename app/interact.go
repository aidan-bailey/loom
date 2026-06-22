package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// interactMouseClick handles a left-button press while interacting. The left
// button is deferred: it becomes a Loom drag-selection if the mouse moves, or a
// forwarded click into the agent if it doesn't. Non-left buttons are ignored.
func (m *home) interactMouseClick(mouse tea.Mouse) {
	if mouse.Button != tea.MouseLeft {
		return
	}
	m.splitPane.ClearSelections()
	m.dragging = false
	m.interactLeftDown = false
	if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.tabBar.Height()); ok && pane == m.splitPane.GetFocusedPane() {
		m.dragPane = pane
		m.interactAnchorRow, m.interactAnchorCol = row, col
		m.interactLeftDown = true
	}
}

// interactMouseMotion turns a left-drag into a Loom selection (so it never
// reaches tmux's copy-mode); other motion is ignored.
func (m *home) interactMouseMotion(mouse tea.Mouse) {
	if !m.interactLeftDown {
		return
	}
	if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.tabBar.Height()); ok && pane == m.dragPane {
		if !m.dragging {
			m.splitPane.BeginSelection(m.dragPane, m.interactAnchorRow, m.interactAnchorCol)
			m.dragging = true
		}
		m.splitPane.ExtendSelection(m.dragPane, row, col)
	}
}

// interactMouseRelease finalizes a left drag-selection (copy to clipboard) or,
// for a plain click (no drag), forwards a click into the agent.
func (m *home) interactMouseRelease() tea.Cmd {
	if !m.interactLeftDown {
		return nil
	}
	m.interactLeftDown = false
	if m.dragging {
		m.dragging = false
		text := m.splitPane.SelectedText(m.dragPane)
		if text == "" {
			m.splitPane.ClearSelections()
			return nil
		}
		return copyToClipboard(text)
	}
	// Plain click — forward a press+release into the agent at the anchor cell.
	m.forwardClickToFocused(m.dragPane, m.interactAnchorRow, m.interactAnchorCol)
	return nil
}

// forwardClickToFocused sends a left press+release at (row,col) to the focused
// pane's agent (SGR is 1-indexed; HitTest returns 0-indexed pane-local), so a
// TUI agent registers a click on its own UI.
func (m *home) forwardClickToFocused(pane, row, col int) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		return
	}
	if pane != m.splitPane.GetFocusedPane() {
		return
	}
	if pane == ui.FocusTerminal {
		_ = m.splitPane.ForwardTerminalMouse(0, col+1, row+1, true)
		_ = m.splitPane.ForwardTerminalMouse(0, col+1, row+1, false)
	} else {
		_ = selected.ForwardMouse(0, col+1, row+1, true)
		_ = selected.ForwardMouse(0, col+1, row+1, false)
	}
}

// pasteToFocused sends pasted text into the focused pane as a bracketed paste.
func (m *home) pasteToFocused(text string) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		return
	}
	if m.splitPane.GetFocusedPane() == ui.FocusTerminal {
		_ = m.splitPane.PasteTerminal(text)
	} else {
		_ = selected.Paste(text)
	}
}
