//go:build windows

package tmux

import "github.com/aidan-bailey/loom/session/vt"

// newEmulator always returns nil on Windows: there is no usable detached ptmx
// stream to feed, so the pane keeps the capture-pane snapshot path (Phase 1 is
// a no-op on Windows).
func newEmulator(cols, rows int) vt.Emulator { return nil }
