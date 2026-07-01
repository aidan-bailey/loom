package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCaptureHistoryRealTmux is the regression guard for the live-scroll fix.
// It runs a line-printing command in a REAL tmux session and asserts that
// CaptureHistory() (capture-pane -S -) returns the scrolled-off history — far
// more than the visible 24-row screen. The in-process emulator does NOT
// accumulate this history (tmux paints its clients with redraws, not
// scroll-through), so the scroll-back window must source from CaptureHistory
// rather than the emulator's own (empty) scrollback. This is exactly the case
// the original Phase 2 model got wrong ("scrolling does nothing").
func TestCaptureHistoryRealTmux(t *testing.T) {
	// Needs a real tmux binary; skip where it is unavailable (e.g. the Nix
	// build sandbox), like any test that shells out to an external tool.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH; skipping real-tmux scroll-history test")
	}

	// `tmux attach-session` is a real terminal client and needs a usable
	// TERM to negotiate input/output — CI runners execute steps without a
	// tty, so TERM is unset there (unlike a dev box, which inherits one
	// from the real terminal running `go test`). An unset or "dumb" TERM
	// makes the attach client silently stop forwarding keystrokes to the
	// pane, which is what made this test hang for its full timeout and
	// fail in CI while always passing locally.
	t.Setenv("TERM", "xterm-256color")

	ts := NewTmuxSession("caphist", "sh")
	if err := ts.Start(t.TempDir()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = ts.Close() }()

	if err := ts.SetDetachedSize(80, 24); err != nil {
		t.Fatalf("setsize: %v", err)
	}

	// Still poll for the command's output instead of trusting a fixed
	// sleep, and re-issue it if nothing has shown up yet, since a loaded
	// CI runner can leave the attach settling when the first
	// SendKeys/TapEnter land.
	const cmd = "for i in $(seq 200); do echo histline$i; done"
	const marker = "histline200"
	deadline := time.Now().Add(10 * time.Second)
	for {
		content, err := ts.CapturePaneContent()
		if err == nil && strings.Contains(content, marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell never produced %q after retried SendKeys/TapEnter", marker)
		}
		if err := ts.SendKeys(cmd); err != nil {
			t.Fatalf("sendkeys: %v", err)
		}
		if err := ts.TapEnter(); err != nil {
			t.Fatalf("enter: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	hist, ok := ts.CaptureHistory()
	if !ok {
		t.Fatal("CaptureHistory should succeed for a live session")
	}
	lines := strings.Split(strings.TrimRight(hist, "\n"), "\n")
	if len(lines) <= 24 {
		t.Fatalf("CaptureHistory must include scrolled-off history; got only %d lines", len(lines))
	}
	if !strings.Contains(hist, "histline1") {
		t.Fatal("CaptureHistory should include the earliest scrolled-off line (histline1)")
	}
}
