package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// frameInterval is one frame at Bubble Tea's default 60 fps: the rate its
// renderer flushes at, and the output pump's coalescer interval
// (session/tmux/notify.go).
const frameInterval = 16 * time.Millisecond

// frameTickMsg ends a burst: the trailing frame frameGate.output armed
// for pane output that came within a frame of the last render.
type frameTickMsg struct{}

// frameGate lets View reuse its last frame for pane output, so all panes
// share one frame instead of each event rendering one
// (docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md). Reuse is
// opt-in: only output asks for it, and only for the View that follows its
// Update; every other View renders. The zero value is ready. It is
// touched only on the Update goroutine, where Bubble Tea also calls View.
type frameGate struct {
	now      func() time.Time // nil: time.Now
	last     tea.View
	has      bool
	rendered time.Time
	reuse    bool // the next View may return last
	pending  bool // a frameTickMsg is armed
}

func (g *frameGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// fresh makes the next View render, whatever output asked: Update calls
// it before the message switch, and again when the Update changed more
// than a pane (a drain that applied events, the link offline).
func (g *frameGate) fresh() { g.reuse = false }

// output handles a pane's output; drawn says the screen can show it
// (home.drawsPane). Undrawn output reuses the frame. Drawn output renders
// at once when a frame has passed since the last render, as a keystroke's
// echo does, and otherwise reuses the frame and arms one trailing
// frameTickMsg for the rest of it.
func (g *frameGate) output(drawn bool) tea.Cmd {
	if !drawn {
		g.reuse = true
		return nil
	}
	wait := frameInterval - g.clock().Sub(g.rendered)
	if wait <= 0 { // also with no frame yet: rendered is the zero time
		return nil
	}
	g.reuse = true
	if g.pending {
		return nil
	}
	g.pending = true
	return tea.Tick(wait, func(time.Time) tea.Msg { return frameTickMsg{} })
}

// ticked records that the armed frameTickMsg arrived; its Update renders.
func (g *frameGate) ticked() { g.pending = false }

// view returns the last frame when the Update before asked to reuse it,
// and otherwise renders and keeps the result.
func (g *frameGate) view(render func() tea.View) tea.View {
	if g.reuse && g.has {
		g.reuse = false
		return g.last
	}
	g.reuse = false
	g.last = render()
	g.has = true
	g.rendered = g.clock()
	return g.last
}
