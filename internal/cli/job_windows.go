//go:build windows

package cli

// signalExit is the Windows stub: the work queue is unavailable there, so no
// job records a signal. Every name is one it cannot map.
func signalExit(string) int { return exitNotRun }
