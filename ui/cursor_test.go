package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

// cursorLocal is the inverse of HitTest for a single cell: pane content
// (x, y) → split-local coordinates. Keep this table in sync with HitTest's
// geometry comments (title row at y=0, left border at x=0, terminal content
// starting at agentHeight+3).
func TestCursorLocal_Geometry(t *testing.T) {
	const agentHeight = 20
	cases := []struct {
		name   string
		pane   int
		cx, cy int
		wantX  int
		wantY  int
	}{
		{"agent origin", FocusAgent, 0, 0, 1, 1},
		{"agent interior", FocusAgent, 12, 5, 13, 6},
		{"terminal origin", FocusTerminal, 0, 0, 1, agentHeight + 3},
		{"terminal interior", FocusTerminal, 7, 2, 8, agentHeight + 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, y := cursorLocal(tc.pane, agentHeight, vt.Cursor{X: tc.cx, Y: tc.cy})
			require.Equal(t, tc.wantX, x)
			require.Equal(t, tc.wantY, y)
		})
	}
}

// TestCursorLocal_RoundTripsHitTest: mapping a cursor cell to split-local
// coordinates and hit-testing those coordinates must land on the same pane
// and cell — the two geometries may never drift apart.
func TestCursorLocal_RoundTripsHitTest(t *testing.T) {
	s := NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
	s.SetSize(100, 40)

	for _, pane := range []int{FocusAgent, FocusTerminal} {
		x, y := cursorLocal(pane, s.agent.height, vt.Cursor{X: 3, Y: 2})
		gotPane, row, col, ok := s.HitTest(x, y)
		require.True(t, ok, "cursor cell must be hit-testable (pane %d)", pane)
		require.Equal(t, pane, gotPane)
		require.Equal(t, 2, row)
		require.Equal(t, 3, col)
	}
}
