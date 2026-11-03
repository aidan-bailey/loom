package buildsweep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// cargoIn reports whether a process of uid named cargo or cargo-* (its
// /proc/<pid>/comm) has its working directory inside dir, reading
// procRoot as /proc; detail says which, or what could not be read. A pid
// that vanishes mid-scan is passed over; one of uid's own whose working
// directory can't be read counts as busy, since it might be a test run.
func cargoIn(procRoot string, uid uint32, dir string) (busy bool, detail string) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return true, "can't list processes: " + err.Error()
	}
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		p := filepath.Join(procRoot, e.Name())
		if owner, err := procOwner(p); err != nil || owner != uid {
			continue // gone, or another user's
		}
		comm, err := os.ReadFile(filepath.Join(p, "comm"))
		if err != nil {
			continue // gone
		}
		name := strings.TrimSpace(string(comm))
		if name != "cargo" && !strings.HasPrefix(name, "cargo-") {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(p, "cwd"))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return true, fmt.Sprintf("%s (pid %s): its working directory can't be read: %v", name, e.Name(), err)
		case within(dir, cwd):
			return true, fmt.Sprintf("%s (pid %s) runs in the worktree", name, e.Name())
		}
	}
	return false, ""
}

// isPID reports whether name, an entry of /proc, is a process's.
func isPID(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
