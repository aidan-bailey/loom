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
