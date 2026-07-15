package ui

import (
	"strings"
	"testing"

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

func TestScrollModel_LiveTailByDefault(t *testing.T) {
	var m ScrollModel
	require.False(t, m.IsScrolling())
	_, live, ok := m.Window(newFake(), 3)
	require.True(t, ok)
	require.True(t, live, "offset 0 must report live tail")
}

func TestScrollModel_WindowsScrollbackThenSeed(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2) // 2 above bottom
	w, live, ok := m.Window(f, 3)
	require.True(t, ok)
	require.False(t, live)
	require.Equal(t, "sb3\nsb4\nscr1", w)

	// Scroll past the emulator span into the seed.
	// Logical buffer: [seedA seedB seedC sb1 sb2 sb3 sb4 scr1 scr2 scr3],
	// offset 6 → window rows are indexes 1..3.
	m.ScrollBy(f, 4)
	w, _, _ = m.Window(f, 3)
	require.Equal(t, "seedB\nseedC\nsb1", w)
}

func TestScrollModel_ClampsAtSeedTop(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.GotoTop(f)
	w, _, _ := m.Window(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
	// One more up-tick stays pinned.
	m.ScrollBy(f, 1)
	w, _, _ = m.Window(f, 3)
	require.Equal(t, "seedA\nseedB\nseedC", w)
}

func TestScrollModel_AnchorsWhenScrollbackGrows(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	w1, _, _ := m.Window(f, 3)
	// Two lines scroll off the screen into scrollback (output arrived).
	f.sb = append(f.sb, "scr1", "scr2")
	f.screen = []string{"scr3", "new1", "new2"}
	w2, _, _ := m.Window(f, 3)
	require.Equal(t, w1, w2, "content under the cursor must stay put as output accrues")
	require.Equal(t, 2, m.NewLinesBelow(), "footer must count lines accrued below")
}

func TestScrollModel_ScrollDownReturnsToLive(t *testing.T) {
	var m ScrollModel
	f := newFake()
	m.ScrollBy(f, 2)
	m.ScrollBy(f, -2)
	_, live, _ := m.Window(f, 3)
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
	_, _, ok := m.Window(f, 3)
	require.False(t, ok, "no emulator → caller must use the snapshot fallback")
}

func TestScrollModel_ScrollPercent(t *testing.T) {
	var m ScrollModel
	f := newFake()
	require.Equal(t, 1.0, m.ScrollPercent(f, 3))
	m.GotoTop(f)
	_, _, _ = m.Window(f, 3)
	require.Equal(t, 0.0, m.ScrollPercent(f, 3))
}
