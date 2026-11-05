//go:build linux

package sched

import "os"

// readIOCounters reads this process's /proc/self/io. The kernel adds a
// reaped child's counters, and those of the descendants it reaped, to its
// parent's, so the shim's reading after it waits for its command covers the
// job's whole tree except processes orphaned to init.
func readIOCounters() (ioCounters, bool) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return ioCounters{}, false
	}
	return parseIOCounters(data)
}
