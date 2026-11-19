//go:build unix

package sched

import (
	"errors"
	"os"
	"syscall"
)

// writeControl writes data to a cgroup control file through a plain
// blocking descriptor. os.OpenFile hands a kernfs file to Go's poller (it
// supports poll), which turns a write's EAGAIN into a wait for an edge
// kernfs never sends, so the write parks for good; memory.reclaim answers
// EAGAIN whenever it frees less than asked. Here EAGAIN comes back as the
// error it is.
func writeControl(path, data string) error {
	var fd int
	err := retryEINTR(func() (err error) {
		fd, err = syscall.Open(path, syscall.O_WRONLY|syscall.O_CLOEXEC, 0)
		return err
	})
	if err != nil {
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
	var n int
	werr := retryEINTR(func() (err error) {
		n, err = syscall.Write(fd, []byte(data))
		return err
	})
	if werr == nil && n < len(data) {
		werr = errors.New("short write")
	}
	if cerr := syscall.Close(fd); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return &os.PathError{Op: "write", Path: path, Err: werr}
	}
	return nil
}

// retryEINTR runs f until it returns anything but EINTR.
func retryEINTR(f func() error) error {
	for {
		if err := f(); !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
