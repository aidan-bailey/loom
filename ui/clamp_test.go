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
