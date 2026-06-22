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

	ts := NewTmuxSession("caphist", "sh")
	if err := ts.Start(t.TempDir()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = ts.Close() }()

	if err := ts.SetDetachedSize(80, 24); err != nil {
		t.Fatalf("setsize: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let the attach settle

	if err := ts.SendKeys("for i in $(seq 200); do echo histline$i; done"); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	if err := ts.TapEnter(); err != nil {
		t.Fatalf("enter: %v", err)
	}
	time.Sleep(1500 * time.Millisecond) // let output land in tmux history

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
