package vt

import (
	"strings"
	"testing"
)

// Every rail card and the preview render their pane's screen on every frame,
// and most frames change no pane: a screen nothing wrote to since is reused.
func TestXVT_RenderReusesTheScreenUntilItChanges(t *testing.T) {
	e := NewXVT(20, 3).(*xvtEmulator)
	defer e.Close()
	_, _ = e.Write([]byte("hello"))

	first := e.Render()
	second := e.Render()
	if first != second {
		t.Fatalf("an unchanged screen rendered differently: %q then %q", first, second)
	}
	if e.renders != 1 {
		t.Fatalf("an unchanged screen was rendered %d times, want 1", e.renders)
	}
}

func TestXVT_RenderAfterAWriteShowsIt(t *testing.T) {
	e := NewXVT(20, 3).(*xvtEmulator)
	defer e.Close()
	_, _ = e.Write([]byte("a"))
	_ = e.Render()
	_, _ = e.Write([]byte("b"))

	if got := e.Render(); !strings.Contains(got, "ab") {
		t.Fatalf("Render after a write = %q, want it to show %q", got, "ab")
	}
}

func TestXVT_RenderAfterAResizeShowsIt(t *testing.T) {
	e := NewXVT(20, 3).(*xvtEmulator)
	defer e.Close()
	_, _ = e.Write([]byte("x"))
	if got := strings.Count(e.Render(), "\n") + 1; got != 3 {
		t.Fatalf("before the resize: %d rows, want 3", got)
	}
	e.Resize(20, 5)

	if got := strings.Count(e.Render(), "\n") + 1; got != 5 {
		t.Fatalf("Render after a resize to 5 rows: %d rows", got)
	}
}
