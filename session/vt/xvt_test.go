package vt

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeLines(e Emulator, n int) {
	for i := 1; i <= n; i++ {
		_, _ = e.Write([]byte(fmt.Sprintf("line%d\r\n", i)))
	}
}

func TestXVT_ScrollbackGrows(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 30) // > height, forces lines into scrollback
	if e.ScrollbackLen() == 0 {
		t.Fatal("scrollback should accrue after writing more lines than the screen height")
	}
}

func TestXVT_RenderWindow_Content(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 30)
	// fromBottom=0 -> the bottom `rows` lines (live tail region).
	bottom := stripSGR(e.RenderWindow(0, 3))
	if !strings.Contains(bottom, "line30") {
		t.Fatalf("window at bottom should include the newest line; got %q", bottom)
	}
	// Scroll up into history: a window further from the bottom shows older lines.
	up := stripSGR(e.RenderWindow(10, 3))
	if strings.Contains(up, "line30") {
		t.Fatalf("a scrolled-up window should not show the newest line; got %q", up)
	}
}

func TestXVT_RenderWindow_BlankPadding(t *testing.T) {
	e := NewXVT(20, 5)
	defer e.Close()
	writeLines(e, 3) // tiny buffer
	// Far past the top -> leading blanks, never panics.
	got := e.RenderWindow(1000, 4)
	if strings.Count(got, "\n") > 4 {
		t.Fatalf("window must be at most `rows` lines; got %q", got)
	}
	// rows < 1 -> empty.
	if e.RenderWindow(0, 0) != "" {
		t.Fatal("RenderWindow(_,0) must return empty string")
	}
}

func TestXVT_QueryReplyDoesNotBlock(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	done := make(chan struct{})
	go func() {
		// A DSR cursor-position query (\x1b[6n) makes x/vt generate a reply on
		// its internal unbuffered io.Pipe; without a reader draining that pipe,
		// this Write blocks forever — exactly what wedges the tmux output pump
		// (holding the emulator write-lock) and freezes the UI on startup.
		_, _ = e.Write([]byte("\x1b[6n"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("xvtEmulator.Write blocked on a query reply (reply pipe not drained)")
	}
}

func TestXVT_PlainText(t *testing.T) {
	e := NewXVT(20, 3)
	defer e.Close()
	_, _ = e.Write([]byte("hello"))
	got := stripSGR(e.Render())
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected screen to start with %q, got %q", "hello", got)
	}
}

func TestXVT_SGRColor(t *testing.T) {
	e := NewXVT(20, 1)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[1;32mhi\x1b[0m"))
	r := e.Render()
	if !strings.Contains(r, "hi") {
		t.Fatalf("rendered screen missing text: %q", r)
	}
	if !strings.Contains(r, "\x1b[") {
		t.Fatalf("rendered screen should carry SGR sequences for colored text: %q", r)
	}
}

func TestXVT_ClearAndHome(t *testing.T) {
	e := NewXVT(20, 3)
	defer e.Close()
	_, _ = e.Write([]byte("garbage\x1b[2J\x1b[Habc"))
	got := stripSGR(e.Render())
	firstLine := strings.SplitN(got, "\n", 2)[0]
	if !strings.HasPrefix(strings.TrimRight(firstLine, " "), "abc") {
		t.Fatalf("after clear+home, first line should be %q, got %q", "abc", firstLine)
	}
}

func TestXVT_ResizeRowCount(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	for i := 0; i < 20; i++ {
		_, _ = e.Write([]byte("line\r\n"))
	}
	e.Resize(80, 10)
	rows := strings.Split(strings.TrimRight(e.Render(), "\n"), "\n")
	if len(rows) > 10 {
		t.Fatalf("after Resize(80,10) visible screen should be <=10 rows, got %d", len(rows))
	}
}

func TestXVT_Deterministic(t *testing.T) {
	render := func() string {
		e := NewXVT(20, 2)
		defer e.Close()
		_, _ = e.Write([]byte("\x1b[33mwarn\x1b[0m\r\nok"))
		return e.Render()
	}
	if render() != render() {
		t.Fatal("same byte stream must produce identical Render() output")
	}
}

func TestXVT_ConcurrentWriteRender(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = e.Write([]byte("x"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = e.Render()
			_ = e.Cursor()
		}
	}()
	wg.Wait()
}

// stripSGR removes CSI SGR sequences so tests can assert on visible text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
