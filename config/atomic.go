package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path via a temp file in the same directory,
// fsyncs it, and renames it into place. This guarantees readers see either the
// old contents or the new contents, never a truncated or empty file.
//
// The temp file is created with O_EXCL so concurrent writers don't share it.
// If the write or rename fails, the temp file is removed and the original
// file is left untouched.
//
// After rename we also fsync the parent directory so the directory entry
// update is durable; without it a power loss between rename and the next
// filesystem commit can leave the old contents (or, on data=writeback mode,
// an empty file) visible after reboot. Best-effort — silently a no-op on
// filesystems/platforms (e.g. Windows) that don't support directory fsync.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	// Chmod before Sync so the fsync covers the mode change too.
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	// Durability barrier for the rename itself. Failures here are
	// non-fatal: the data is already on disk and readable; we only lose
	// the durability guarantee if a crash happens in the next few ms.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
