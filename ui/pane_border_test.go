package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// assertRowsReachRightEdge fails if any row is not exactly width wide or ends in
// a space. The pane's manual top border (buildTopBorder) is always exactly
// s.width and ends in the corner glyph ╮; the lipgloss body must match. A body
// box rendered narrower than s.width gets right-padded with spaces by
// JoinVertical — which is what made the bottom/right border fall short of the
// top border. So "every row ends in a border glyph, never a space" pins the
// right border to the true right edge.
func assertRowsReachRightEdge(t *testing.T, out string, width int) {
	t.Helper()
	for i, ln := range strings.Split(out, "\n") {
		plain := ansi.Strip(ln)
		if w := lipgloss.Width(ln); w != width {
			t.Errorf("row %d width %d != %d: %q", i, w, width, plain)
		}
		if strings.HasSuffix(plain, " ") {
			t.Errorf("row %d ends in space (border short of right edge): %q", i, plain)
		}
	}
}

// TestPaneBorderReachesRightEdge guards the "bottom/right border is too short"
// bug at the renderPane level: lipgloss .Width is the TOTAL box width (border
// included), so the body must be sized to the full pane width, not the width
// minus the border frame.
func TestPaneBorderReachesRightEdge(t *testing.T) {
	sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	sp.SetSize(40, 24)

	cases := []struct {
		name    string
		content string
	}{
		{"narrow", "hello\nworld"},
		{"exact", strings.Repeat("x", 38)}, // a line exactly as wide as the content area
		{"overwide", strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertRowsReachRightEdge(t, sp.renderPane(" Agent ", c.content, 5, false), sp.width)
		})
	}
}

// TestSplitPaneStringBordersReachRightEdge exercises the real composed output
// (agent + terminal panes, and the diff overlay) at several sizes. This is the
// user-facing guarantee: no rendered row's border should fall short of the
// right edge regardless of content width or pane size.
func TestSplitPaneStringBordersReachRightEdge(t *testing.T) {
	sizes := []struct{ w, h int }{{40, 24}, {80, 30}, {120, 50}, {293, 75}}
	contents := []struct {
		name            string
		agent, terminal string
	}{
		{"short", strings.Repeat("agent line\n", 60), strings.Repeat("term line\n", 60)},
		{"overwide", strings.Repeat(strings.Repeat("A", 400)+"\n", 60), strings.Repeat(strings.Repeat("T", 400)+"\n", 60)},
	}
	for _, sz := range sizes {
		for _, c := range contents {
			t.Run(c.name, func(t *testing.T) {
				sp := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
				sp.SetSize(sz.w, sz.h)
				sp.agent.previewState = previewState{text: c.agent}
				sp.terminal.content = c.terminal

				assertRowsReachRightEdge(t, sp.String(), sz.w)

				// And the diff overlay path (the `d` toggle).
				sp.diffVisible = true
				assertRowsReachRightEdge(t, sp.String(), sz.w)
			})
		}
	}
}
