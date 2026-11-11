//go:build windows

package cli

// freeBytes is 0 on Windows, where loom has no work queue and so no flow
// run.
func freeBytes(string) uint64 { return 0 }
