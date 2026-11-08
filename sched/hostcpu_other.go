//go:build !linux

package sched

// ReadHostCPU answers ErrUnsupported: /proc/stat is Linux's.
func ReadHostCPU() (CPUTimes, error) { return CPUTimes{}, ErrUnsupported }
