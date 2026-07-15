package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeScrollSource simulates an emulator-backed session: a seed, a growing
// scrollback, and a fixed-height screen.
type fakeScrollSource struct {
	seed      []string
	sb        []string // scrollback lines, oldest first
	screen    []string // visible rows
	alt       bool
	ok        bool
	forwarded []int // +n per wheel-up burst, -n per wheel-down burst
}

func (f *fakeScrollSource) SeedHistory() []string { return f.seed }
func (f *fakeScrollSource) ScrollbackLen() (int, bool) {
	if !f.ok {
		return 0, false
	}
	return len(f.sb), true
}
func (f *fakeScrollSource) RenderWindow(offset, rows int) (string, bool) {
	if !f.ok {
		return "", false
	}
	buf := append(append([]string{}, f.sb...), f.screen...)
	total := len(buf)
	top := total - offset - rows
	out := make([]string, rows)
	for i := range out {
		if idx := top + i; idx >= 0 && idx < total {
			out[i] = buf[idx]
		}
	}
	return strings.Join(out, "\n"), true
}
func (f *fakeScrollSource) IsAlternateScreen() bool { return f.alt }
func (f *fakeScrollSource) ForwardWheel(up bool, n int) error {
	d := -n
	if up {
		d = n
	}
	f.forwarded = append(f.forwarded, d)
	return nil
}

func newFake() *fakeScrollSource {
	return &fakeScrollSource{
		seed:   []string{"seedA", "seedB", "seedC"},
		sb:     []string{"sb1", "sb2", "sb3", "sb4"},
		screen: []string{"scr1", "scr2", "scr3"},
		ok:     true,
	}
}

// TestPreviewPane_ScrollRoutingPrefersEmulator pins the routing decision:
// with an emulator-backed source the ScrollModel engages and the snapshot
// state stays untouched.
func TestPreviewPane_ScrollRoutingPrefersEmulator(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(40, 10)
	f := newFake()
	p.scroll.ScrollBy(f, 3)
	w, live, ok := p.scroll.AdvanceAndRender(f, 9)
	require.True(t, ok)
	require.False(t, live)
	require.NotEmpty(t, w)
	require.Zero(t, p.snapFallback.offset, "emulator path must not touch snapshot state")
}

func TestScrollModel_LiveTailByDefault(t *testing.T) {
	var m ScrollModel
	require.False(t, m.IsScrolling())
	_, live, ok := m.AdvanceAndRender(newFake(), 3)
	require.True(t, ok)
	require.True(t, live, "offset 0 must report live tail")
}

func TestScrollModel_WindowsScrollbackThenSeed(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2) // 2 above bottom
	w, live, ok := m.AdvanceAndRender(f, 3)
	require.True(t, ok)
	require.False(t, live)
	require.Equal(t, "sb3\nsb4\nscr1", w)

	// Scroll past the emulator span into the seed.
	// Logical buffer: [seedA seedB seedC sb1 sb2 sb3 sb4 scr1 scr2 scr3],
	// offset 6 → window rows are indexes 1..3.
	m.ScrollBy(f, 4)
	w, _, _ = m.AdvanceAndRender(f, 3)
	require.Equal(t, "seedB\nseedC\nsb1", w)
}

func TestScrollModel_ClampsAtSeedTop(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.GotoTop(f)
	w, _, _ := m.AdvanceAndRender(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
	// One more up-tick stays pinned.
	m.ScrollBy(f, 1)
	w, _, _ = m.AdvanceAndRender(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
}

func TestScrollModel_AnchorsWhenScrollbackGrows(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	w1, _, _ := m.AdvanceAndRender(f, 3)
	// Two lines scroll off the screen into scrollback (output arrived).
	f.sb = append(f.sb, "scr1", "scr2")
	f.screen = []string{"scr3", "new1", "new2"}
	w2, _, _ := m.AdvanceAndRender(f, 3)
	require.Equal(t, w1, w2, "content under the cursor must stay put as output accrues")
	require.Equal(t, 2, m.NewLinesBelow(), "footer must count lines accrued below")
}

func TestScrollModel_ScrollDownReturnsToLive(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	m.ScrollBy(f, -2)
	_, live, _ := m.AdvanceAndRender(f, 3)
	require.True(t, live)
	require.Equal(t, 0, m.NewLinesBelow())
}

func TestScrollModel_ResetReturnsToLive(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 3)
	m.Reset()
	require.False(t, m.IsScrolling())
}

func TestScrollModel_AltScreenForwardsWheel(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	// Damped: wheelEventsPerNotch events per forwarded notch.
	for i := 0; i < wheelEventsPerNotch; i++ {
		require.NoError(t, m.ScrollUp(f))
	}
	require.Equal(t, []int{1}, f.forwarded)
	require.False(t, m.IsScrolling(), "alt-screen scroll must not move the window offset")
}

func TestScrollModel_AltScreenProbeIsCached(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	require.NoError(t, m.ScrollUp(f)) // first tick probes and caches
	f.alt = false                     // probe result changes...
	require.NoError(t, m.ScrollUp(f)) // ...but within the TTL the cache holds
	require.False(t, m.IsScrolling(), "cached alt-screen result must keep routing to forwarding within the TTL")
}

func TestScrollModel_NoEmulatorReportsNotOK(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.ok = false
	m.ScrollBy(f, 2)
	_, _, ok := m.AdvanceAndRender(f, 3)
	require.False(t, ok, "no emulator → caller must use the snapshot fallback")
}

func TestScrollModel_ScrollPercent(t *testing.T) {
	var m ScrollModel
	f := newFake()
	require.Equal(t, 1.0, m.ScrollPercent(f))
	m.GotoTop(f)
	_, _, _ = m.AdvanceAndRender(f, 3)
	require.Equal(t, 0.0, m.ScrollPercent(f))
}

func TestScrollModel_ScrollPercentMidRange(t *testing.T) {
	var m ScrollModel
	f := newFake()
	// maxOff = len(seed)+sbLen = 3+4 = 7; offset 7 (top) → 0.0, so a
	// mid-range offset yields a fraction strictly between 0 and 1.
	m.ScrollBy(f, 3)
	// 1 - 3/7 ≈ 0.5714
	require.InDelta(t, 1.0-3.0/7.0, m.ScrollPercent(f), 1e-9)
}

func TestScrollModel_PageUpDownNormalScreen(t *testing.T) {
	var m ScrollModel
	f := newFake()
	require.NoError(t, m.PageUp(f, 10)) // half-page up: +5
	require.Equal(t, 5, m.offset)
	require.Empty(t, f.forwarded, "normal screen must move the window, not forward")
	require.NoError(t, m.PageDown(f, 10)) // half-page down: -5
	require.Equal(t, 0, m.offset)
	require.Empty(t, f.forwarded)
}

func TestScrollModel_PageUpDownAltScreenBurst(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	require.NoError(t, m.PageUp(f, 10))
	require.NoError(t, m.PageDown(f, 10))
	require.Equal(t, []int{agentPageNotches, -agentPageNotches}, f.forwarded)
	require.False(t, m.IsScrolling(), "alt-screen page must not move the window offset")
}

func TestScrollModel_WheelDirectionFlipResetsDamping(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	// One up tick accumulates but doesn't cross the notch threshold (which
	// is wheelEventsPerNotch >= 2), so nothing is forwarded yet.
	require.NoError(t, m.ScrollUp(f))
	require.Empty(t, f.forwarded)
	// Flipping direction resets the accumulator, so a single down tick also
	// doesn't cross the threshold — the partial up progress was discarded.
	require.NoError(t, m.ScrollDown(f))
	require.Empty(t, f.forwarded, "direction flip must reset the damping accumulator")
	// A second down tick now crosses the threshold and forwards one notch.
	require.NoError(t, m.ScrollDown(f))
	require.Equal(t, []int{-1}, f.forwarded)
}

// TestTerminalPane_ScrollModelAltRouting: a full-screen TUI in the
// terminal pane must receive wheel events instead of a frozen window.
func TestTerminalPane_ScrollModelAltRouting(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	for i := 0; i < wheelEventsPerNotch; i++ {
		require.NoError(t, m.ScrollUp(f))
	}
	require.NotEmpty(t, f.forwarded, "alt-screen terminal scroll must forward wheel events")
}

// TestScrollModel_AltProbeSplit pins the off-lock probe entry point used by
// TerminalPane: NeedsAltProbe is true until SetAltProbe stamps the cache,
// after which a route within the TTL is a cache hit (no re-probe).
func TestScrollModel_AltProbeSplit(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	require.True(t, m.NeedsAltProbe(), "a fresh model must want a probe")

	// Caller probes off-lock and stores the result.
	m.SetAltProbe(f.alt)
	require.False(t, m.NeedsAltProbe(), "a freshly stamped cache must not want a re-probe")

	// A route on the fresh cache uses the stored alt result: wheel events are
	// forwarded, and the source's own probe is never consulted (it would flip
	// the cached decision if it were).
	f.alt = false // if the route re-probed, it would move the window instead
	for i := 0; i < wheelEventsPerNotch; i++ {
		require.NoError(t, m.ScrollUp(f))
	}
	require.NotEmpty(t, f.forwarded, "route on a fresh cache must use the stored alt result")
	require.False(t, m.IsScrolling(), "alt route must not move the window offset")

	// Once the TTL lapses, NeedsAltProbe reports staleness again.
	m.altScreenChecked = time.Now().Add(-2 * agentScrollTTL)
	require.True(t, m.NeedsAltProbe(), "a lapsed cache must want a fresh probe")
}

func TestScrollModel_AltScreenReprobesAfterTTL(t *testing.T) {
	var m ScrollModel
	f := newFake()
	f.alt = true
	require.NoError(t, m.ScrollUp(f)) // probes and caches alt=true
	require.False(t, m.IsScrolling())
	// Force the cache stale (can't sleep 750ms tastefully in a unit test).
	m.altScreenChecked = time.Now().Add(-2 * agentScrollTTL)
	f.alt = false                     // inner app left the alternate screen
	require.NoError(t, m.ScrollUp(f)) // re-probes: now moves the window
	require.True(t, m.IsScrolling(), "post-TTL re-probe must pick up the new alt-screen state")
}
