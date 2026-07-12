package vt

import (
	"io"
	"sync"

	xvt "github.com/charmbracelet/x/vt"
)

// xvtEmulator wraps charmbracelet/x/vt. x/vt's plain *Emulator is not safe for
// concurrent Write+Render, so we guard with our own RWMutex: Write/Resize/Close
// take the write lock; Render/Cursor take the read lock. The tmux output pump
// is the sole writer; the Bubble Tea Update goroutine is the reader.
type xvtEmulator struct {
	mu        sync.RWMutex
	term      *xvt.Emulator
	drainDone chan struct{}

	// Callback-fed state. x/vt invokes Callbacks inside term.Write/Resize,
	// while THIS goroutine already holds e.mu's write lock — so callbacks
	// must only assign these fields (never re-lock e.mu: RWMutex is not
	// reentrant; never call out of the package). Readers take RLock.
	cursorVisible bool
	cursorShape   CursorShape
	cursorBlink   bool
}

// NewXVT constructs a real terminal emulator sized to cols x rows.
func NewXVT(cols, rows int) Emulator {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	e := &xvtEmulator{
		term:          xvt.NewEmulator(cols, rows),
		drainDone:     make(chan struct{}),
		cursorVisible: true,
		cursorShape:   CursorShapeBlock,
		cursorBlink:   true,
	}
	e.term.SetCallbacks(xvt.Callbacks{
		CursorVisibility: func(visible bool) {
			e.cursorVisible = visible
		},
		CursorStyle: func(style xvt.CursorStyle, steady bool) {
			switch style {
			case xvt.CursorUnderline:
				e.cursorShape = CursorShapeUnderline
			case xvt.CursorBar:
				e.cursorShape = CursorShapeBar
			default:
				e.cursorShape = CursorShapeBlock
			}
			// x/vt's Callbacks.CursorStyle signature names this param
			// "blink", but screen.go's setCursorStyle invokes it as
			// s.cb.CursorStyle(style, !blink) — the value delivered is
			// actually Cursor.Steady. Negate to get blink (DECSCUSR odd
			// codes blink, even codes are steady).
			e.cursorBlink = !steady
		},
	})
	// x/vt answers terminal queries (Device Attributes, Device Status / cursor
	// position, foreground/background/cursor color, mode reports) by writing the
	// reply to an UNBUFFERED io.Pipe. A write to that pipe blocks until the reply
	// is Read. Loom only mirrors tmux's already-emulated client stream for
	// DISPLAY and never needs these replies — but tmux sends such queries when a
	// client attaches, so without a reader the first query blocks emu.Write
	// forever, wedging the output pump (which holds our write lock) and freezing
	// the UI. Drain and discard the replies; the copy ends when Close() closes
	// the pipe and Read returns EOF.
	go func() {
		defer close(e.drainDone)
		_, _ = io.Copy(io.Discard, e.term)
	}()
	return e
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
	return Cursor{
		X:       p.X,
		Y:       p.Y,
		Visible: e.cursorVisible,
		Shape:   e.cursorShape,
		Blink:   e.cursorBlink,
	}
}

func (e *xvtEmulator) Close() error {
	// Stop the drain goroutine by closing the emulator's reply pipe directly,
	// via the *io.PipeWriter that InputPipe() returns. We deliberately do NOT
	// call e.term.Close(): it writes an unsynchronized `closed` flag that the
	// race detector flags against the drain goroutine's Read of the same flag
	// (an x/vt-internal data race). Closing the pipe's write end makes that
	// Read return EOF so the drain exits, and `closed` is never written — so
	// nothing races on it. The pump that feeds Write has already stopped before
	// callers invoke Close, so no concurrent Write remains.
	if c, ok := e.term.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
	// Wait for the drain goroutine to observe the closed pipe and exit, so a
	// Close never leaks the goroutine.
	<-e.drainDone
	return nil
}
