//go:build !windows

package tmux

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// pollable returns ptmx reopened in non-blocking mode, so Go's poller owns
// it: closing it, or a read deadline, then ends a read in progress. creack/pty
// hands the master back in blocking mode (it takes the fd through
// os.File.Fd, which sets blocking for good), where neither does: an idle
// pane's output pump stayed in its read, and every stop of its client waited
// out pumpWaitTimeout. It closes ptmx; the copy is close-on-exec, so no
// child loom starts later holds the master open.
func pollable(ptmx *os.File) (*os.File, error) {
	rc, err := ptmx.SyscallConn()
	if err != nil {
		return nil, err
	}
	dup, dupErr := -1, error(nil)
	if err := rc.Control(func(fd uintptr) { dup, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0) }); err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, fmt.Errorf("copy the PTY: %w", dupErr)
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return nil, fmt.Errorf("make the PTY non-blocking: %w", err)
	}
	name := ptmx.Name()
	_ = ptmx.Close()
	return os.NewFile(uintptr(dup), name), nil
}

// setPtySize sets ptmx's window size through its SyscallConn rather than
// creack/pty's Setsize, which takes the fd through os.File.Fd. Today Fd
// leaves pollable's file alone (it restores blocking mode only on a file
// the os package itself made non-blocking), but that is a detail of os,
// and a resize must never cost the pump its interruptible read.
func setPtySize(ptmx *os.File, cols, rows int) error {
	rc, err := ptmx.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		ioErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	}); err != nil {
		return err
	}
	return ioErr
}
