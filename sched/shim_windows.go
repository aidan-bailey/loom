package sched

import (
	"fmt"
	"os"
	"syscall"
)

// RunShim has no Windows counterpart: it says so and fails.
func RunShim(string) int {
	fmt.Fprintln(os.Stderr, "loom job-shim:", ErrUnsupported)
	return 1
}

// Start starts nothing on Windows.
func (Runner) Start(string) (pid int, err error) { return 0, ErrUnsupported }

// Probe tests nothing on Windows.
func Probe(string) (alive bool, err error) { return false, ErrUnsupported }

// SignalGroup signals nothing on Windows.
func SignalGroup(int, syscall.Signal) error { return ErrUnsupported }
