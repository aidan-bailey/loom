//go:build !linux && !darwin

package sched

// readRAMBytes has no source of RAM here; 0 is "could not be read".
func readRAMBytes() uint64 { return 0 }
