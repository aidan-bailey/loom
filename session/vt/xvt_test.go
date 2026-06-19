package vt

import (
	"strings"
	"sync"
	"testing"
)

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
