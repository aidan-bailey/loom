//go:build !windows

package cli

import (
	"os/exec"
	"runtime"
	"syscall"
)

// openerCommand is the command that opens link in the user's browser: open on
// macOS, xdg-open on Linux and the BSDs. Its Stdin, Stdout and Stderr stay
// nil (/dev/null), so a console browser never takes the terminal. It runs in
// its own session (Setsid), so a Ctrl-C in the CLI's foreground process group
// during the wait does not kill it or a browser it started.
func openerCommand(link string) *exec.Cmd {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, link)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
