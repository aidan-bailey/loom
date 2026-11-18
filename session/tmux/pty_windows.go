//go:build windows

package tmux

import (
	"os"

	"github.com/creack/pty"
)

// pollable returns ptmx as it is: the poller and blocking-mode story it
// answers on Unix (pty_unix.go) doesn't apply to Windows PTYs.
func pollable(ptmx *os.File) (*os.File, error) { return ptmx, nil }

// setPtySize sets ptmx's window size.
func setPtySize(ptmx *os.File, cols, rows int) error {
	return pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}
