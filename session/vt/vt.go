// Package vt defines Loom's pane-display abstraction. An Emulator consumes
// raw PTY bytes from the tmux-client stream and renders the current visible
// screen as a string the UI prints verbatim. tmux remains the session owner;
// this is display only.
package vt

// CursorShape is the visual form of the cursor, as set by DECSCUSR.
type CursorShape int

const (
	CursorShapeBlock CursorShape = iota
	CursorShapeUnderline
	CursorShapeBar
)

// Cursor is the visible cursor state in cells, 0-based, origin top-left.
// Visible reflects DECTCEM (apps hide the cursor while painting); Shape and
// Blink reflect DECSCUSR. The defaults — visible blinking block — match a
// fresh terminal.
type Cursor struct {
	X, Y    int
	Visible bool
	Shape   CursorShape
	Blink   bool
}

// Emulator is the display surface for one pane. Implementations must be safe
// for one writer goroutine (the tmux output pump calling Write) concurrent
// with reader calls (Render/Cursor) from the Bubble Tea Update goroutine.
type Emulator interface {
	// Write feeds raw PTY bytes (ANSI/CSI/OSC/DCS) into the emulator. It
	// mirrors io.Writer so it can be the output pump's destination directly.
	Write(p []byte) (n int, err error)

	// Resize sets the emulator's screen geometry in cells.
	Resize(cols, rows int)

	// Render returns the current VISIBLE screen as a string with embedded
	// ANSI SGR sequences, sized to the last Resize. The UI prints this verbatim.
	Render() string

	// Cursor returns the current cursor position and visibility.
	Cursor() Cursor

	// Title returns the window title most recently set by the inner app via
	// OSC 0/2, or "" if never set.
	Title() string

	// SetBellFunc installs a handler invoked when the inner app rings BEL.
	// The handler runs inside Write on the pump goroutine — it must be
	// cheap, must not call back into the Emulator, and must be safe to
	// invoke concurrently with readers (tea.Program.Send qualifies).
	SetBellFunc(f func())

	// Close releases emulator resources. Safe to call multiple times.
	Close() error
}
