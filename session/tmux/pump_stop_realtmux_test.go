package tmux

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stopping or re-attaching a client of an idle pane is prompt: the output
// pump, blocked reading a pane that prints nothing, ends with its PTY. It
// used to wait out pumpWaitTimeout every time, and PausePreview runs on the
// TUI's Update goroutine (a full-screen attach), freezing the UI meanwhile.
func TestPaneClientStops_AreQuickOnAnIdlePane_RealTmux(t *testing.T) {
	privateTmux(t, "ps")
	if !EmulatorEnabled() {
		t.Skip("emulator disabled (LOOM_PANE_RENDERER=snapshot)")
	}
	s := NewSession("ps", "sh -c 'echo ready; exec sleep 60'")
	require.NoError(t, s.Start(t.TempDir()))
	t.Cleanup(func() { _ = s.Close() })

	c := NewAttachClient(s.SessionName(), "sh")
	require.NoError(t, c.Restore())
	t.Cleanup(func() { _ = c.PausePreview() })
	require.Eventually(t, func() bool {
		screen, ok := c.RenderEmulator()
		return ok && strings.Contains(screen, "ready")
	}, 5*time.Second, 20*time.Millisecond, "fixture: the client renders the pane")
	time.Sleep(300 * time.Millisecond) // the pane goes idle

	start := time.Now()
	require.NoError(t, c.Restore())
	assert.Less(t, time.Since(start), pumpWaitTimeout/2, "re-attaching an idle pane waited on its old pump")

	time.Sleep(300 * time.Millisecond)
	start = time.Now()
	require.NoError(t, c.PausePreview())
	assert.Less(t, time.Since(start), pumpWaitTimeout/2, "pausing an idle pane's client waited on its pump")
}
