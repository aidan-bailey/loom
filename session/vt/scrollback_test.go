package vt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// feed writes lines through the emulator as a program would print them.
func feed(t *testing.T, e Emulator, lines ...string) {
	t.Helper()
	for _, l := range lines {
		_, err := e.Write([]byte(l + "\r\n"))
		require.NoError(t, err)
	}
}

func TestScrollbackLen_CountsScrolledOffLines(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	require.Equal(t, 0, e.ScrollbackLen())
	feed(t, e, "l1", "l2", "l3", "l4", "l5", "l6")
	// 4-row screen, 6 lines printed + trailing prompt row: at least 2 scrolled off.
	require.GreaterOrEqual(t, e.ScrollbackLen(), 2)
}

func TestRenderWindow_OffsetZeroEqualsRender(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "a", "b")
	require.Equal(t, e.Render(), e.RenderWindow(0, 4))
}

func TestRenderWindow_WindowsIntoScrollback(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8")
	sb := e.ScrollbackLen()
	require.Greater(t, sb, 0)
	// Oldest line: a 1-row window whose bottom sits total-1 above the live
	// bottom (total = scrollback + 4 screen rows).
	top := e.RenderWindow(sb+4-1, 1)
	require.Contains(t, stripANSI(top), "l1")
	// A window one line up from live keeps exactly `rows` lines.
	w := e.RenderWindow(1, 4)
	require.Equal(t, 4, len(strings.Split(w, "\n")))
	// Its bottom row is the screen's second-to-last row.
	rows := strings.Split(w, "\n")
	scr := strings.Split(e.Render(), "\n")
	require.Equal(t, stripANSI(scr[len(scr)-2]), stripANSI(rows[len(rows)-1]))
}

func TestRenderWindow_ClampsOutOfRange(t *testing.T) {
	e := NewXVT(20, 4)
	defer e.Close()
	feed(t, e, "x")
	require.NotPanics(t, func() {
		_ = e.RenderWindow(1<<30, 4)
		_ = e.RenderWindow(-5, 4)
		_ = e.RenderWindow(0, 0)
	})
	require.Equal(t, "", e.RenderWindow(0, 0))
}

func TestSetScrollbackSize_CapsRetention(t *testing.T) {
	e := NewXVT(20, 2)
	defer e.Close()
	e.SetScrollbackSize(5)
	for i := 0; i < 30; i++ {
		feed(t, e, "line")
	}
	require.LessOrEqual(t, e.ScrollbackLen(), 5)
}
