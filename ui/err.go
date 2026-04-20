package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ErrBox is the bottom-row error surface. It renders a single-line,
// centered red message and collapses embedded newlines into `//` so
// multi-line errors still fit one row.
type ErrBox struct {
	height, width int
	err           error
}

var errStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
	Light: "#FF0000",
	Dark:  "#FF0000",
})

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

// Clear removes the currently displayed error.
func (e *ErrBox) Clear() {
	e.err = nil
}

// SetSize updates the rendering bounds.
func (e *ErrBox) SetSize(width, height int) {
	e.width = width
	e.height = height
}

func (e *ErrBox) String() string {
	var err string
	if e.err != nil {
		err = e.err.Error()
		lines := strings.Split(err, "\n")
		err = strings.Join(lines, "//")
		if runewidth.StringWidth(err) > e.width-3 && e.width-3 >= 0 {
			err = runewidth.Truncate(err, e.width-3, "...")
		}
	}
	return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center, errStyle.Render(err))
}
