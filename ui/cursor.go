package ui

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/vt"
)

// cursorLocal maps a pane-content cell to split-local coordinates — the
// exact inverse of HitTest's geometry: each pane box is a title row (y=0 /
// y=agentHeight+2) plus a body bordered left/right/bottom, agent stacked
// over terminal.
func cursorLocal(pane int, agentHeight int, c vt.Cursor) (x, y int) {
	x = 1 + c.X // left body border occupies column 0
	if pane == FocusTerminal {
		return x, agentHeight + 3 + c.Y
	}
	return x, 1 + c.Y // title border row occupies row 0
}

// CursorScreenPosition returns the focused pane's live cursor as split-local
// coordinates plus its full state, or ok=false whenever no hardware cursor
// should be shown: diff overlay up, focused pane scrolled off the live tail
// or showing fallback text, no emulator-backed session, cursor hidden
// (DECTCEM), or the cell out of the pane's bounds.
func (s *SplitPane) CursorScreenPosition(instance *session.Instance) (x, y int, cur vt.Cursor, ok bool) {
	if s.diffVisible {
		return 0, 0, vt.Cursor{}, false
	}
	var c vt.Cursor
	var have bool
	var w, h int
	switch s.focusedPane {
	case FocusAgent:
		if instance == nil || s.agent.IsScrolling() || s.agent.ShowingFallback() {
			return 0, 0, vt.Cursor{}, false
		}
		c, have = instance.CursorState()
		w, h = s.agent.width, s.agent.height
	case FocusTerminal:
		if s.terminal.IsScrolling() || s.terminal.ShowingFallback() {
			return 0, 0, vt.Cursor{}, false
		}
		c, have = s.terminal.CursorState()
		w, h = s.terminal.width, s.terminal.height
	default:
		return 0, 0, vt.Cursor{}, false
	}
	if !have || !c.Visible || c.X < 0 || c.X >= w || c.Y < 0 || c.Y >= h {
		return 0, 0, vt.Cursor{}, false
	}
	x, y = cursorLocal(s.focusedPane, s.agent.height, c)
	return x, y, c, true
}
