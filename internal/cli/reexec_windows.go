//go:build windows

package cli

import "errors"

// reexecSupported: Windows can't replace a process in place, so a command
// refused by a newer daemon fails fast there.
const reexecSupported = false

// errReexecUnsupported is execSelf's and executable's answer on Windows.
var errReexecUnsupported = errors.New("loom can't re-execute on this platform")

// execSelf is unsupported on Windows.
func execSelf(string, []string, []string) error { return errReexecUnsupported }

// executable is unsupported on Windows.
func executable(string) error { return errReexecUnsupported }
