//go:build !linux

package buildsweep

import (
	"errors"
	"os"
	"time"
)

// supported is false: access times and cargo's processes are checked
// through /proc, which only Linux has, so every worktree is skipped.
const supported = false

func fileUse(os.FileInfo) (time.Time, uint64, uint64, bool) { return time.Time{}, 0, 0, false }

func procOwner(string) (uint32, error) { return 0, errors.New("no /proc on this OS") }
