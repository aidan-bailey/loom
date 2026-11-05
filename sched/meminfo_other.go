//go:build !linux

package sched

// ReadMemAvailableKB answers ErrUnsupported: /proc/meminfo is Linux's.
func ReadMemAvailableKB() (int64, error) { return 0, ErrUnsupported }
