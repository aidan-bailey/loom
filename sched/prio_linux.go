package sched

import (
	"errors"
	"fmt"
	"syscall"
)

// ioprioWhoProcess and ioprioBestEffort7 are ioprio_set's arguments: the
// calling thread (who 0), and (class best-effort = 2) << 13 | level 7.
const (
	ioprioWhoProcess  = 1
	ioprioBestEffort7 = 2<<13 | 7
)

// lowerPriority sets the calling thread's nice value to niceValue and its I/O
// class to best-effort level 7. Both are per thread on Linux and inherited
// by what the thread forks; RunShim calls it on the thread that starts the
// command. A failure of either is returned, and the other still applies.
func lowerPriority() error {
	var errs []error
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceValue); err != nil {
		errs = append(errs, fmt.Errorf("setpriority: %w", err))
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, 0, ioprioBestEffort7); errno != 0 {
		errs = append(errs, fmt.Errorf("ioprio_set: %w", errno))
	}
	return errors.Join(errs...)
}
