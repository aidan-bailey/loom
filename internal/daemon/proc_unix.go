//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// recordPath is where globalDir's lock record lives: in the lock file
// itself, where a loom TUI from before the daemon wrote its own (flock is
// advisory, so the file stays readable while it is held).
func recordPath(globalDir string) string { return LockPath(globalDir) }

// alive reports whether a process pid exists.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// detach makes cmd a session leader of its own, so the daemon outlives the
// terminal and the client that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// ownedByAnother reports whether info belongs to a user other than this
// process's.
func ownedByAnother(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) != os.Getuid()
}

// terminate asks pid to stop gracefully: `loom serve` turns SIGTERM into a
// stop that waits for jobs in flight and saves.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
