//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

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
