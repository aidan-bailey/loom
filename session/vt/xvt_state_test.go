package vt

import (
	"testing"
	"unicode/utf8"

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

func TestTitle_OSCPassThrough(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	require.Equal(t, "", e.Title())
	_, _ = e.Write([]byte("\x1b]2;claude - working\x07"))
	require.Equal(t, "claude - working", e.Title())
	_, _ = e.Write([]byte("\x1b]0;both-title\x07")) // OSC 0 sets icon+title
	require.Equal(t, "both-title", e.Title())
}

// TestTitle_NonASCIITruncatesInVendoredParser documents a known limitation
// in the vendored charmbracelet/x/vt OSC-string parser (as of the pinned
// commit): it terminates the title early on the lead byte of a multi-byte
// UTF-8 sequence, rather than a mangled test bug on our side. Real agent
// titles (e.g. Claude Code's "✳ ...") hit this — PaneTitle/windowTitle guard
// against it by rejecting invalid UTF-8 (see TestPaneTitle_RejectsInvalidUTF8
// in status_content_test.go), so callers never see the mangled byte(s).
func TestTitle_NonASCIITruncatesInVendoredParser(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b]2;✳ claude\x07"))
	require.False(t, utf8.ValidString(e.Title()),
		"if this starts passing, the vendored x/vt parser was fixed — simplify PaneTitle's guard and update this test")
}
