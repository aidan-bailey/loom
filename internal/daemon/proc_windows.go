//go:build windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// detachedProcess is Windows' DETACHED_PROCESS creation flag: no console.
const detachedProcess = 0x00000008

// detach starts cmd with no console and in a process group of its own, so
// the daemon outlives the console and the client that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}

// ownedByAnother reports whether info belongs to another user. Windows
// reports no owner here; the directory's ACL, inherited from the user's
// profile or temp dir, keeps others out.
func ownedByAnother(os.FileInfo) bool { return false }

// terminate has no graceful signal to send on Windows, and killing the
// daemon would skip its save.
func terminate(int) error {
	return errors.New("stopping the loom daemon is not supported on Windows: end its loom serve process")
}
