package ui

import (
	"strings"
	"time"

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
	// expiresAt is when the current message should auto-clear; zero
	// means no message is armed. Checked lazily by ExpireIfDue rather
	// than a per-message timer, so every SetError/SetInfo call site gets
	// the same expiry behavior for free — including ones reached from
	// startup code that runs before anything exists to drive a
	// per-message timer callback.
	expiresAt time.Time
}

var (
	errStyle, infoStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildErrStyles) }

func rebuildErrStyles() {
	errStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	infoStyle = lipgloss.NewStyle().Foreground(Info)
}

// NewErrBox constructs an empty ErrBox; the caller must SetSize before
// the first render.
func NewErrBox() *ErrBox {
	return &ErrBox{}
}

// SetError replaces the currently displayed error and arms its auto-clear
// deadline (see ExpireIfDue). Pass nil to hide the box on the next render;
// prefer Clear for clarity.
func (e *ErrBox) SetError(err error) {
	e.err = err
	e.info = ""
	if err == nil {
		e.expiresAt = time.Time{}
		return
	}
	e.expiresAt = time.Now().Add(toastDuration(err.Error()))
}

// SetInfo sets a non-error status line (e.g. the recovery summary) and arms
// its auto-clear deadline, mirroring SetError. An active error takes
// precedence over info in String().
func (e *ErrBox) SetInfo(msg string) {
	e.info = msg
	e.err = nil
	if msg == "" {
		e.expiresAt = time.Time{}
		return
	}
	e.expiresAt = time.Now().Add(toastDuration(msg))
}

// toastDuration scales visible time with message length so a multi-line
// git error or multi-part recovery summary can be read fully, capped so a
// long message can't pin the box open indefinitely.
func toastDuration(msg string) time.Duration {
	d := 3*time.Second + time.Duration(len(msg)/40)*time.Second
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

// ExpireIfDue clears the box once its armed deadline has passed. Intended
// to be called from an existing periodic tick rather than scheduling a
// timer per message — without this, SetInfo/SetError have no way to expire
// on their own and a toast lingers on screen indefinitely once shown.
func (e *ErrBox) ExpireIfDue(now time.Time) {
	if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
		return
	}
	e.err = nil
	e.info = ""
	e.expiresAt = time.Time{}
}

// Clear removes the currently displayed error and info line immediately.
func (e *ErrBox) Clear() {
	e.err = nil
	e.info = ""
	e.expiresAt = time.Time{}
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
