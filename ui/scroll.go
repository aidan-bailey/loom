package ui

import (
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session"
)

// scrollSourceFor adapts an instance's tmux session to scrollSource.
// ok=false when the instance has no live session.
func scrollSourceFor(instance *session.Instance) (scrollSource, bool) {
	if instance == nil {
		return nil, false
	}
	ts := instance.TmuxSession()
	if ts == nil {
		return nil, false
	}
	return ts, true
}

// scrollSource is the pane-agnostic view of one session's scroll data: an
// immutable pre-attach seed, the emulator's growing scrollback + screen,
// the tmux alternate-screen probe, and wheel forwarding for alt-screen
// TUIs. Implemented by *tmux.TmuxSession. ok=false from the emulator-backed
// accessors means "no emulator" — the caller falls back to snapshot
// capture-pane windowing.
type scrollSource interface {
	SeedHistory() []string
	ScrollbackLen() (int, bool)
	RenderWindow(offset, rows int) (string, bool)
	// IsAlternateScreen is a tmux subprocess probe (NOT emulator state —
	// the emulator sees tmux's client stream, never the inner app's
	// screen mode); ScrollModel caches it for agentScrollTTL.
	IsAlternateScreen() bool
	ForwardWheel(up bool, n int) error
}

// ScrollModel owns pane scroll state for the emulator path: the
// lines-from-bottom offset, output anchoring, the new-lines-below footer
// count, wheel damping, and alt-screen routing. Both panes embed one.
// All methods run on the Update goroutine (no locking; TerminalPane's own
// mutex wraps its calls).
type ScrollModel struct {
	// offset is lines-from-bottom into seed+scrollback+screen; 0 = live tail.
	offset int
	// lastSb anchors the view: scrollback growth while scrolled bumps
	// offset by the same amount so content stays put. Cell-line counts are
	// width-stable, so there is no re-wrap drift (unlike capture-pane rows).
	lastSb int
	// baselineSb is the scrollback length when the gesture started; the
	// footer's new-lines count is the growth since then.
	baselineSb int
	// wheelAccum dampens forwarded wheel notches on alt-screen TUIs.
	wheelAccum int
	// altScreen caches the tmux alternate_on probe for agentScrollTTL —
	// a subprocess per wheel tick would be worse than 750ms staleness.
	altScreen        bool
	altScreenChecked time.Time
}

// AdvanceAndRender renders `rows` lines at the current offset. live=true
// means the pane is at the live tail (offset 0) and the caller should
// render its normal live view instead. ok=false means no emulator
// (snapshot fallback).
//
// It also performs the per-render anchoring bump and clamping (hence the
// name — it advances the anchor state AND renders), so it must be called
// exactly once per render pass. A repeat call with no intervening output
// is idempotent: sbLen is unchanged so the anchor bump is a no-op and the
// window is identical. The only failure mode of an extra call is
// NewLinesBelow drift if output *did* arrive between the two calls — never
// corruption of the rendered window.
//
// Limitation: at the emulator's scrollback cap (x/vt default 10k lines)
// sbLen plateaus, so the anchor bump stops compensating for further
// scroll-off and the view drifts up by at most the overflow. Bounded,
// graceful degradation — accepted.
func (m *ScrollModel) AdvanceAndRender(src scrollSource, rows int) (window string, live bool, ok bool) {
	sbLen, sbOK := src.ScrollbackLen()
	if !sbOK {
		return "", false, false
	}
	if m.offset > 0 {
		// Anchor: new scroll-off lines appeared below the window; bump the
		// offset so content under the cursor stays put.
		if grown := sbLen - m.lastSb; grown > 0 {
			m.offset += grown
		}
	}
	m.lastSb = sbLen

	if rows < 1 {
		rows = 1
	}
	seed := src.SeedHistory()
	// The emulator screen is one row taller than the scrolled view: panes
	// reserve the footer line, so rows == paneHeight-1 by contract on both
	// panes while RenderWindow's coordinate space uses the true screen height
	// H = rows+1. total must live in that coordinate space, or the composition
	// math falls one line short of RenderWindow and drops the oldest
	// post-attach scrollback line at the seed↔scrollback junction.
	total := len(seed) + sbLen + rows + 1
	if maxOff := total - rows; m.offset > maxOff {
		m.offset = maxOff
	}
	if m.offset <= 0 {
		m.offset = 0
		m.baselineSb = 0
		return "", true, true
	}

	// Compose over the logical buffer [seed | scrollback+screen]:
	// idx 0 = oldest seed line, total-1 = bottom screen row. A window has
	// at most one seed→emulator boundary, so emit seed rows until the
	// boundary, then take the rest in one RenderWindow call.
	top := total - m.offset - rows
	seedLen := len(seed)
	lines := make([]string, 0, rows)
	for i := 0; i < rows; {
		idx := top + i
		if idx < seedLen {
			if idx >= 0 {
				lines = append(lines, seed[idx])
			} else {
				lines = append(lines, "") // blank-pad above the top
			}
			i++
			continue
		}
		// Emulator region: the composed window's bottom sits m.offset
		// lines above live, and RenderWindow shares that coordinate space
		// (total - top - rows == m.offset), so pass it through.
		w, wok := src.RenderWindow(m.offset, rows-i)
		if !wok {
			return "", false, false
		}
		lines = append(lines, strings.Split(w, "\n")...)
		break
	}
	return strings.Join(lines, "\n"), false, true
}

// ScrollBy moves the offset by delta (positive = up into history).
// The gesture baseline is captured on the first move away from the tail.
func (m *ScrollModel) ScrollBy(src scrollSource, delta int) {
	sbLen, ok := src.ScrollbackLen()
	if !ok {
		return
	}
	if m.offset == 0 && delta > 0 {
		m.baselineSb = sbLen
		m.lastSb = sbLen
	}
	m.offset += delta
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset == 0 {
		m.baselineSb = 0
	}
}

// GotoTop pins the view to the oldest line (clamped in Window).
func (m *ScrollModel) GotoTop(src scrollSource) { m.ScrollBy(src, scrollToTopOffset) }

// Reset returns to the live tail (resize, instance switch, Esc/End) and
// forces the next alt-screen probe.
func (m *ScrollModel) Reset() {
	m.offset = 0
	m.baselineSb = 0
	m.lastSb = 0
	m.wheelAccum = 0
	m.altScreenChecked = time.Time{}
}

// NewLinesBelow is the footer count: scroll-off lines accrued since the
// gesture began. Limitation: at the scrollback cap (x/vt default 10k
// lines) sbLen plateaus, so the count stops growing even as new output
// arrives — bounded, graceful degradation, accepted.
func (m *ScrollModel) NewLinesBelow() int {
	if m.offset <= 0 {
		return 0
	}
	if n := m.lastSb - m.baselineSb; n > 0 {
		return n
	}
	return 0
}

// IsScrolling reports whether the pane is away from the live tail.
func (m *ScrollModel) IsScrolling() bool { return m.offset > 0 }

// ScrollPercent maps the offset to [0,1]; 1.0 = live tail.
func (m *ScrollModel) ScrollPercent(src scrollSource) float64 {
	if m.offset <= 0 {
		return 1.0
	}
	sbLen, ok := src.ScrollbackLen()
	if !ok {
		return 1.0
	}
	// maxOff here (seed+sbLen) is one less than the clamp's in AdvanceAndRender
	// (seed+sbLen+1), so a GotoTop offset floors this percent slightly negative
	// → 0 below. Intentional: the +1 slack is the emulator's footer row, not a
	// scrollable line, so the top of history is percent 0.
	maxOff := len(src.SeedHistory()) + sbLen
	if maxOff <= 0 {
		return 1.0
	}
	p := 1.0 - float64(m.offset)/float64(maxOff)
	if p < 0 {
		return 0
	}
	return p
}

// ScrollUp/ScrollDown route one wheel/key tick: alt-screen TUIs get damped
// forwarded wheel events; everything else moves the window.
func (m *ScrollModel) ScrollUp(src scrollSource) error   { return m.route(src, true, 1) }
func (m *ScrollModel) ScrollDown(src scrollSource) error { return m.route(src, false, 1) }

// PageUp/PageDown move by half the given pane height (or forward a burst).
func (m *ScrollModel) PageUp(src scrollSource, height int) error {
	if m.isAltScreen(src) {
		return src.ForwardWheel(true, agentPageNotches)
	}
	m.ScrollBy(src, +(height / 2))
	return nil
}
func (m *ScrollModel) PageDown(src scrollSource, height int) error {
	if m.isAltScreen(src) {
		return src.ForwardWheel(false, agentPageNotches)
	}
	m.ScrollBy(src, -(height / 2))
	return nil
}

// NeedsAltProbe reports whether the alt-screen TTL cache is stale, i.e. the
// next isAltScreen would run a subprocess probe. It only reads cache
// timestamps, so a caller holding its own mutex can consult it, drop the
// mutex, run the probe (IsAlternateScreen — a tmux subprocess) off-lock, then
// SetAltProbe the result before invoking a routing method. That keeps the
// subprocess out of the caller's lock while a subsequent route()/PageUp on the
// now-fresh cache never re-probes. Used by TerminalPane, whose t.mu wraps all
// ScrollModel access; PreviewPane is unlocked and calls the routers directly.
func (m *ScrollModel) NeedsAltProbe() bool {
	return m.altScreenChecked.IsZero() || time.Since(m.altScreenChecked) >= agentScrollTTL
}

// SetAltProbe stores a freshly-probed alt-screen result and stamps the TTL, so
// the next isAltScreen within agentScrollTTL is a cache hit. Pairs with
// NeedsAltProbe for off-lock probing (see there).
func (m *ScrollModel) SetAltProbe(alt bool) {
	m.altScreen = alt
	m.altScreenChecked = time.Now()
}

// isAltScreen probes tmux for the inner app's alternate-screen state,
// cached for agentScrollTTL (the probe is a subprocess).
func (m *ScrollModel) isAltScreen(src scrollSource) bool {
	now := time.Now()
	if !m.altScreenChecked.IsZero() && now.Sub(m.altScreenChecked) < agentScrollTTL {
		return m.altScreen
	}
	m.altScreen = src.IsAlternateScreen()
	m.altScreenChecked = now
	return m.altScreen
}

func (m *ScrollModel) route(src scrollSource, up bool, delta int) error {
	if m.isAltScreen(src) {
		return m.forwardWheelDamped(src, up)
	}
	if up {
		m.ScrollBy(src, +delta)
	} else {
		m.ScrollBy(src, -delta)
	}
	return nil
}

// forwardWheelDamped forwards one notch per wheelEventsPerNotch
// same-direction events (accumulator resets on direction change).
func (m *ScrollModel) forwardWheelDamped(src scrollSource, up bool) error {
	if m.wheelAccum != 0 && (m.wheelAccum > 0) != up {
		m.wheelAccum = 0
	}
	if up {
		m.wheelAccum++
		if m.wheelAccum >= wheelEventsPerNotch {
			m.wheelAccum = 0
			return src.ForwardWheel(true, 1)
		}
		return nil
	}
	m.wheelAccum--
	if m.wheelAccum <= -wheelEventsPerNotch {
		m.wheelAccum = 0
		return src.ForwardWheel(false, 1)
	}
	return nil
}
