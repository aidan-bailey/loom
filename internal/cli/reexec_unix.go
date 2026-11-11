//go:build !windows

package cli

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reexecSupported: a Unix process can replace itself in place.
const reexecSupported = true

// execSelf replaces this process with path (execve), keeping its PID.
func execSelf(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}

// executable is nil when this user may execute path (access X_OK).
func executable(path string) error {
	return unix.Access(path, unix.X_OK)
}
