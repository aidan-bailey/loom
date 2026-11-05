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
