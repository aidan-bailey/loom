package vt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursor_DefaultVisibleBlinkingBlock(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	c := e.Cursor()
	require.True(t, c.Visible)
	require.Equal(t, CursorShapeBlock, c.Shape)
	require.True(t, c.Blink)
}

func TestCursor_DECTCEMHideShow(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[?25l"))
	require.False(t, e.Cursor().Visible, "DECTCEM reset must hide the cursor")
	_, _ = e.Write([]byte("\x1b[?25h"))
	require.True(t, e.Cursor().Visible, "DECTCEM set must show the cursor")
}

func TestCursor_DECSCUSRShapes(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	cases := []struct {
		seq   string
		shape CursorShape
		blink bool
	}{
		{"\x1b[1 q", CursorShapeBlock, true},
		{"\x1b[2 q", CursorShapeBlock, false},
		{"\x1b[3 q", CursorShapeUnderline, true},
		{"\x1b[4 q", CursorShapeUnderline, false},
		{"\x1b[5 q", CursorShapeBar, true},
		{"\x1b[6 q", CursorShapeBar, false},
	}
	for _, tc := range cases {
		_, _ = e.Write([]byte(tc.seq))
		c := e.Cursor()
		require.Equal(t, tc.shape, c.Shape, "seq %q", tc.seq)
		require.Equal(t, tc.blink, c.Blink, "seq %q blink", tc.seq)
	}
}

func TestCursor_TracksPosition(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[5;10H")) // row 5, col 10 (1-based)
	c := e.Cursor()
	require.Equal(t, 9, c.X)
	require.Equal(t, 4, c.Y)
}
