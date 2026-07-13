package vt

import (
	"testing"
	"time"
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

func TestFocusReporting_Mode1004Tracking(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	require.False(t, e.FocusReportingEnabled(), "off until the app enables mode 1004")
	_, _ = e.Write([]byte("\x1b[?1004h"))
	require.True(t, e.FocusReportingEnabled())
	_, _ = e.Write([]byte("\x1b[?1004l"))
	require.False(t, e.FocusReportingEnabled())
}

func TestBell_InvokesBellFunc(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	rang := 0
	e.SetBellFunc(func() { rang++ })
	_, _ = e.Write([]byte("ding\x07dong"))
	require.Equal(t, 1, rang)
	require.NotPanics(t, func() {
		e2 := NewXVT(10, 5)
		defer e2.Close()
		_, _ = e2.Write([]byte("\x07")) // no func set → no-op
	})
}

// TestBell_FiresOutsideWriteLock pins the fix for a real-world total-UI
// deadlock: the production bellFunc forwards to tea.Program.Send, which
// blocks until the Update goroutine receives the message — and the Update
// goroutine routinely blocks on this emulator's lock (Render/Resize/Cursor).
// If Write invokes bellFunc while still holding the write lock, those two
// goroutines wait on each other forever (observed live: goroutine 1 in
// Resize→Lock, pump goroutine in Write→Bell→Send). Bell must therefore be
// delivered only after Write has released the lock.
func TestBell_FiresOutsideWriteLock(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()

	inBell := make(chan struct{})
	release := make(chan struct{})
	e.SetBellFunc(func() {
		close(inBell)
		<-release // models p.Send blocking on a busy Update goroutine
	})

	writeDone := make(chan struct{})
	go func() {
		_, _ = e.Write([]byte("\x07"))
		close(writeDone)
	}()

	select {
	case <-inBell:
	case <-time.After(2 * time.Second):
		t.Fatal("bellFunc never invoked")
	}

	// With bellFunc still blocked, every lock-taking method must proceed —
	// this is exactly what the Update goroutine does while the pump delivers
	// a bell. A timeout here means the bell is being fired under the lock.
	proceeded := make(chan struct{})
	go func() {
		e.Resize(100, 30)
		_ = e.Render()
		_ = e.Cursor()
		close(proceeded)
	}()
	select {
	case <-proceeded:
	case <-time.After(2 * time.Second):
		t.Fatal("emulator lock held while bell callback was blocked — deadlock with tea.Program.Send")
	}

	close(release)
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Write did not return after bell callback unblocked")
	}
}
