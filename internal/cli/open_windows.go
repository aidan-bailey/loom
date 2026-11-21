//go:build windows

package cli

import "os/exec"

// openerCommand is the command that opens link in the user's browser. Its
// Stdin, Stdout and Stderr stay nil.
func openerCommand(link string) *exec.Cmd {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
}
