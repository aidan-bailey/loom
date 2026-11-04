//go:build !windows

package cli

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// signalExit is 128 plus the number of the signal named name ("SIGKILL"), as
// a shell reports a command killed by it. name is what the runner recorded:
// unix.SignalName's name, whose inverse unix.SignalNum is, or "SIG<n>" for a
// signal unix cannot name (a Linux real-time one), whose n is read from it.
// A name that is neither gives exitNotRun.
func signalExit(name string) int {
	if sig := unix.SignalNum(name); sig != 0 {
		return 128 + int(sig)
	}
	if digits, ok := strings.CutPrefix(name, "SIG"); ok {
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return 128 + n
		}
	}
	return exitNotRun
}
