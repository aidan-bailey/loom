package sched

import "os"

// readRAMBytes reads MemTotal from /proc/meminfo; 0 when it cannot be read
// or understood.
func readRAMBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	n, err := parseMemTotal(data)
	if err != nil {
		return 0
	}
	return n
}
