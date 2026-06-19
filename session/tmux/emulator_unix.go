//go:build !windows

package tmux

import (
	"os"

	"github.com/aidan-bailey/loom/session/vt"
)

// newEmulator builds the pane-display emulator for a new PTY attach. It returns
// nil to select the legacy capture-pane path on the LOOM_PANE_RENDERER=snapshot
// kill-switch (A/B + emergency fallback). A nil emulator makes the output pump
// default to io.Discard and Preview/terminal source from capture-pane.
func newEmulator(cols, rows int) vt.Emulator {
	if os.Getenv("LOOM_PANE_RENDERER") == "snapshot" {
		return nil
	}
	return vt.NewXVT(cols, rows)
}
