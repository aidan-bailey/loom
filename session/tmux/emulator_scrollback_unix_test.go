//go:build !windows

package tmux

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// A pane's emulator keeps at most paneScrollbackLines of scroll-back: a
// full-width line costs about 15 KB, so x/vt's default of 10,000 let one
// busy pane hold ~150 MB, and the TUI holds an emulator for every pane.
func TestNewEmulator_CapsItsScrollback(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "")
	e := newEmulator(20, 3)
	require.NotNil(t, e)
	t.Cleanup(func() { _ = e.Close() })

	for i := 0; i < paneScrollbackLines+500; i++ {
		_, _ = e.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	require.Equal(t, paneScrollbackLines, e.ScrollbackLen())
}

// The scrolled preview is [seed | emulator scrollback + screen], the seed
// being the pane's history from before the attach. Once the emulator has
// filled its scroll-back it drops its oldest lines, so the seed no longer
// joins on: scrolling up would jump silently from the oldest kept line into
// rows from before the attach. Past that point the seed is dropped (and its
// memory with it), and scroll-back is the emulator's contiguous lines.
func TestSeedHistory_DroppedOnceTheEmulatorsScrollbackIsFull(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "")
	c := NewAttachClientWithDeps("loom_seed", "sh", nil, nil)
	e := newEmulator(20, 3)
	t.Cleanup(func() { _ = e.Close() })
	c.SetEmulatorForTest(e)
	seed := []string{"before attach 1", "before attach 2"}
	c.stateMu.Lock()
	c.seedHistory = seed
	c.stateMu.Unlock()

	write := func(n int) {
		for i := 0; i < n; i++ {
			_, _ = e.Write([]byte(fmt.Sprintf("after %d\r\n", i)))
		}
	}
	write(paneScrollbackLines - 10)
	require.Equal(t, seed, c.SeedHistory(), "below the cap the seed still joins on")

	write(20)
	require.Nil(t, c.SeedHistory(), "at the cap the seed no longer joins on")
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	require.Nil(t, c.seedHistory, "and its memory goes")
}
