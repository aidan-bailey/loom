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

	// Poll for the command's output instead of trusting fixed sleeps, and
	// re-issue it if nothing has shown up yet: on a loaded CI runner the
	// attach can still be settling when the first SendKeys/TapEnter land,
	// silently swallowing them. That race is what made this test flake in
	// CI (see race.yml's 4-core runner) while always passing locally,
	// where the attach settles near-instantly.
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
