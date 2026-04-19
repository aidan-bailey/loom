package config

import (
	"claude-squad/log"
	"fmt"
	"os"
	"time"
)

// quarantineCorruptFile moves a corrupt config/state file aside so the
// next save cannot silently clobber it with defaults-overwrite, and so
// operators have the original bytes to debug from. On success it
// returns the quarantine path; on failure it logs at ERROR level and
// returns the empty string. Either way the caller is expected to
// proceed with defaults — quarantine is a best-effort forensic step,
// not a correctness gate.
func quarantineCorruptFile(path string) string {
	ts := time.Now().UTC().Format("20060102-150405")
	quarantine := fmt.Sprintf("%s.corrupted-%s", path, ts)
	if err := os.Rename(path, quarantine); err != nil {
		log.For("config").Error("quarantine_failed", "path", path, "err", err)
		return ""
	}
	return quarantine
}
