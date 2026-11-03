package buildsweep

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// supported is whether this OS lets the sweep check access times and
// cargo's processes: Linux, through /proc.
const supported = true

// fileUse is fi's last use (its access or modification time, the later),
// inode and link count; ok is false when fi carries no stat data.
func fileUse(fi os.FileInfo) (lastUsed time.Time, ino, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, 0, 0, false
	}
	lastUsed = time.Unix(st.Atim.Unix())
	if mt := fi.ModTime(); mt.After(lastUsed) {
		lastUsed = mt
	}
	return lastUsed, st.Ino, uint64(st.Nlink), true
}

// procOwner is the uid owning path, a /proc/<pid> dir: the process's.
func procOwner(path string) (uint32, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no owner", path)
	}
	return st.Uid, nil
}
