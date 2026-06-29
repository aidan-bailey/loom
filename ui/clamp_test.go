package ui

import (
	"strings"
	"testing"
)

func TestClampHeight(t *testing.T) {
	in := "a\nb\nc\nd"
	if got := clampHeight(in, 2); got != "a\nb" {
		t.Fatalf("clampHeight = %q, want %q", got, "a\nb")
	}
	if got := clampHeight(in, 10); got != in {
		t.Fatalf("clampHeight under-limit changed content: %q", got)
	}
}

// TestSplitPaneNeverOverflowsHeight guards the fix for the "whole loom spills
// over" bug: even with content that is both taller and wider than the panes
// (e.g. mis-sized by a competing tmux client), the split pane must render no
// more rows than its allocated height — otherwise it pushes the status /
// quick-input bar off the bottom of the screen.
func TestSplitPaneNeverOverflowsHeight(t *testing.T) {
	sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	sp.SetSize(40, 24)

	wide := strings.Repeat("x", 200)       // far wider than the 38-col content
	tall := strings.Repeat(wide+"\n", 100) // far more lines than the pane
	sp.agent.previewState = previewState{text: tall}
	sp.terminal.content = tall

	out := sp.String()
	rows := strings.Count(out, "\n") + 1
	if rows > sp.height {
		t.Fatalf("split pane rendered %d rows, exceeds allocated height %d", rows, sp.height)
	}
}

// TestSplitPaneSmallTerminalNoPanic guards the fix for the "strings: negative
// Repeat count" crash. For a small enough terminal, SplitPane.SetSize derives a
// zero content width (width-2 == 0) and a negative pane height
// (int(0.7*(height-4)) < 0). Those degenerate dimensions used to flow into the
// child panes' String(), where `strings.Repeat("\n", height)` panicked on the
// negative count. Sweeping every small size must render without panicking.
func TestSplitPaneSmallTerminalNoPanic(t *testing.T) {
	for w := 0; w <= 10; w++ {
		for h := 0; h <= 10; h++ {
			sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
			// Drive the panes into the fallback state (nil instance) — this is the
			// real startup condition ("No agents running yet…") and the layout that
			// crashed when `loom` was launched in a small window.
			_ = sp.UpdateAgent(nil)
			sp.SetSize(w, h)
			// Must not panic; the result just has to be a string.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("SplitPane.String() panicked at size %dx%d: %v", w, h, r)
					}
				}()
				_ = sp.String()
				// Exercise the diff-overlay branch too — it sizes the diff pane
				// independently and shares the same render path.
				sp.ToggleDiff()
				_ = sp.String()
			}()
		}
	}
}
