// Package vt defines Loom's pane-display abstraction. An Emulator consumes
// raw PTY bytes from the tmux-client stream and renders the current visible
// screen as a string the UI prints verbatim. tmux remains the session owner;
// this is display only.
package vt

// Cursor is the visible cursor position in cells, 0-based, origin top-left.
type Cursor struct {
	X, Y    int
	Visible bool
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

	// Close releases emulator resources. Safe to call multiple times.
	Close() error

	// ScrollbackLen returns the number of lines in the emulator's scrollback
	// (history above the visible screen). Grows as output scrolls; capped at
	// the configured max (x/vt default 10000).
	ScrollbackLen() int

	// RenderWindow renders `rows` lines as an ANSI-styled string. The window's
	// bottom sits `fromBottom` lines above the bottom of the combined
	// [scrollback | visible-screen] buffer: fromBottom 0 = the bottom `rows`
	// lines (live tail). Indices outside the buffer render as blank lines.
	RenderWindow(fromBottom, rows int) string
}
