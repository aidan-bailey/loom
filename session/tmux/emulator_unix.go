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
	if !EmulatorEnabled() {
		return nil
	}
	e := vt.NewXVT(cols, rows)
	e.SetScrollbackSize(paneScrollbackLines)
	return e
}

// paneScrollbackLines is how many lines of scroll-back each pane's emulator
// keeps, the depth the TUI's preview scrolls to (tmux keeps its own history,
// history-limit, for a full-screen attach). x/vt stores a line as cells of
// about 118 bytes, some 15 KB for a full-width one, so its default of 10,000
// let one busy pane hold ~150 MB, and the TUI holds an emulator for every
// pane of every open workspace. 3,000 is the owner's choice (2026-11-18).
const paneScrollbackLines = 3000

// EmulatorEnabled reports whether panes render from the in-process emulator —
// i.e. whether the event-driven update path (pane dirty/quiet/dead events)
// is available. False selects the legacy tick-polled snapshot path. It reads
// this process's LOOM_PANE_RENDERER: a TUI's for its own panes, the
// daemon's for the model's tick (core's tickInterval).
func EmulatorEnabled() bool {
	return os.Getenv("LOOM_PANE_RENDERER") != "snapshot"
}
