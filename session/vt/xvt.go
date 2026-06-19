package vt

import (
	"sync"

	xvt "github.com/charmbracelet/x/vt"
)

// xvtEmulator wraps charmbracelet/x/vt. x/vt's plain *Emulator is not safe for
// concurrent Write+Render, so we guard with our own RWMutex: Write/Resize/Close
// take the write lock; Render/Cursor take the read lock. The tmux output pump
// is the sole writer; the Bubble Tea Update goroutine is the reader.
type xvtEmulator struct {
	mu   sync.RWMutex
	term *xvt.Emulator
}

// NewXVT constructs a real terminal emulator sized to cols x rows.
func NewXVT(cols, rows int) Emulator {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return &xvtEmulator{term: xvt.NewEmulator(cols, rows)}
}

func (e *xvtEmulator) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.term.Write(p)
}

func (e *xvtEmulator) Resize(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.term.Resize(cols, rows)
}

func (e *xvtEmulator) Render() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.term.Render()
}

func (e *xvtEmulator) Cursor() Cursor {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p := e.term.CursorPosition()
	return Cursor{X: p.X, Y: p.Y, Visible: true}
}

func (e *xvtEmulator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.term.Close()
}
