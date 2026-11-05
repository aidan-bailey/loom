//go:build !linux

package sched

// readIOCounters has nothing to read off Linux: the job's I/O goes
// unmeasured.
func readIOCounters() (ioCounters, bool) { return ioCounters{}, false }
