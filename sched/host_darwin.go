package sched

import "golang.org/x/sys/unix"

// readRAMBytes reads the sysctl hw.memsize directly, with no subprocess and
// so no PATH to depend on; 0 when it fails.
func readRAMBytes() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
