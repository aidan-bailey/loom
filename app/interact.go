package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// Mouse event kinds for SGR forwarding in interact mode.
const (
	mousePress   = 0
	mouseRelease = 1
	mouseDrag    = 2
)

// sgrButton maps a tea mouse button to its SGR button code, or -1 if unsupported.
func sgrButton(b tea.MouseButton) int {
	switch b {
	case tea.MouseLeft:
		return 0
	case tea.MouseMiddle:
		return 1
	case tea.MouseRight:
		return 2
	default:
		return -1
	}
}

// forwardMouseToFocused encodes a mouse event as SGR and forwards it into the
// focused pane's agent/terminal (interact mode). kind is mousePress/Release/Drag.
// Only events over the focused pane are forwarded; the agent decodes them as
// real mouse input (so the user can click the agent's own UI).
func (m *home) forwardMouseToFocused(mouse tea.Mouse, kind int) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		return
	}
	pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.tabBar.Height())
	if !ok || pane != m.splitPane.GetFocusedPane() {
		return
	}
	cb := sgrButton(mouse.Button)
	if cb < 0 {
		if kind != mouseRelease {
			return
		}
		cb = 0 // some terminals report no button on release; assume left
	}
	if kind == mouseDrag {
		cb += 32 // SGR motion flag
	}
	press := kind != mouseRelease
	// SGR is 1-indexed; HitTest returns 0-indexed pane-local (row, col).
	if pane == ui.FocusTerminal {
		_ = m.splitPane.ForwardTerminalMouse(cb, col+1, row+1, press)
	} else {
		_ = selected.ForwardMouse(cb, col+1, row+1, press)
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
