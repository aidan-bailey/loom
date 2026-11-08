package sched

import (
	"fmt"
	"os"
)

// ReadHostCPU is the host's CPU times from /proc/stat (exploration spec
// §4.3).
func ReadHostCPU() (CPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUTimes{}, fmt.Errorf("read /proc/stat: %w", err)
	}
	return parseHostStat(data)
}
