//go:build unix

package buildsweep

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock on path without waiting, the kind of
// lock cargo takes (flock(2)); held is true when another holder has it.
// Closing the returned file releases the lock.
func tryLock(path string) (f *os.File, held bool, err error) {
	f, err = os.Open(path)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return f, false, nil
}
