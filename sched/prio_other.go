//go:build unix && !linux

package sched

import (
	"fmt"
	"syscall"
)

// lowerPriority sets the process's nice value to niceValue. These platforms
// have no I/O class to set.
func lowerPriority() error {
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceValue); err != nil {
		return fmt.Errorf("setpriority: %w", err)
	}
	return nil
}
