//go:build !windows

package cli

import "syscall"

// freeBytes is the space an unprivileged process can still write on the
// filesystem holding path, 0 when it can't be read. A flow run's record
// notes it at each gate run's end: a full disk has killed gates, truncated
// a state file and stopped supervisors (the 2026-11-03 and 2026-11-11
// retros).
func freeBytes(path string) uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize) //nolint:gosec,unconvert // the field types differ by platform
}
