package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// ErrBox is the bottom-row error surface. It renders a single-line,
// centered red message and collapses embedded newlines into `//` so
// multi-line errors still fit one row.
type ErrBox struct {
	height, width int
	err           error
	info          string
}

var errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
var infoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))

// NewErrBox constructs an empty ErrBox; the caller must SetSize before
// the first render.
func NewErrBox() *ErrBox {
	return &ErrBox{}
}

// SetError replaces the currently displayed error. Pass nil to hide the
// box on the next render; prefer Clear for clarity.
func (e *ErrBox) SetError(err error) {
	e.err = err
}

// SetInfo sets a non-error status line (e.g. the recovery summary). An active
// error takes precedence over info in String().
func (e *ErrBox) SetInfo(msg string) {
	e.info = msg
}

// Clear removes the currently displayed error and info line.
func (e *ErrBox) Clear() {
	e.err = nil
	e.info = ""
}

// SetSize updates the rendering bounds.
func (e *ErrBox) SetSize(width, height int) {
	e.width = width
	e.height = height
}

func (e *ErrBox) String() string {
	var msg string
	style := errStyle
	switch {
	case e.err != nil:
		msg = e.err.Error()
	case e.info != "":
		msg = e.info
		style = infoStyle
	}
	if msg != "" {
		lines := strings.Split(msg, "\n")
		msg = strings.Join(lines, "//")
		if runewidth.StringWidth(msg) > e.width-3 && e.width-3 >= 0 {
			msg = runewidth.Truncate(msg, e.width-3, "...")
		}
	}
	return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center, style.Render(msg))
}
