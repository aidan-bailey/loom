package sched

import (
	"fmt"
	"os"
)

// ReadMemAvailableKB is the host's MemAvailable, in KB.
func ReadMemAvailableKB() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return parseMemAvailable(data)
}

// ReadSwapFreeKB is the host's SwapFree, in KB: what swap can still take,
// uncompressed (memory borrowing spec §7.2).
func ReadSwapFreeKB() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return meminfoKB(data, "SwapFree")
}
