package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/log"
)

// legacyHomeDirName is the pre-rename directory name we migrate from.
// Kept as a named constant so the migration logic has exactly one place
// referencing the legacy string.
const legacyHomeDirName = ".claude-squad"

// MigrateLegacyHome performs a one-time rename of the legacy config
// directory (~/.claude-squad) to its new location (~/.loom) so that
// in-flight instances, worktrees, and scripts survive the rename.
//
// Behavior:
//   - If ~/.loom already exists, return (idempotent; nothing to do).
//   - If ~/.claude-squad is absent, return (fresh install).
//   - Otherwise os.Rename the directory and print a one-line notice
//     to stderr so the operator knows what happened.
//
// When LOOM_HOME or CLAUDE_SQUAD_HOME is set (i.e. the user has
// overridden the default path), migration is skipped — the user has
// explicitly chosen a location and is assumed to have already placed
// their state there.
//
// Errors from os.Rename (e.g. cross-device) are returned; the caller
// should fall through to normal startup on failure since the legacy
// dir is still readable and GetConfigDir will honor CLAUDE_SQUAD_HOME
// as a deprecated fallback if the user points at it explicitly.
func MigrateLegacyHome() error {
	if os.Getenv(EnvHome) != "" || os.Getenv(legacyEnvHome) != "" {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to resolve user home: %w", err)
	}

	newDir := filepath.Join(homeDir, ".loom")
	legacyDir := filepath.Join(homeDir, legacyHomeDirName)

	if _, err := os.Stat(newDir); err == nil {
		if _, legacyErr := os.Stat(legacyDir); legacyErr == nil {
			log.For("config").Warn(
				"both_loom_and_legacy_home_present",
				"new", newDir,
				"legacy", legacyDir,
				"action", "using_loom",
			)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", newDir, err)
	}

	if _, err := os.Stat(legacyDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", legacyDir, err)
	}

	if err := os.Rename(legacyDir, newDir); err != nil {
		return fmt.Errorf("rename %s → %s: %w", legacyDir, newDir, err)
	}

	fmt.Fprintf(os.Stderr, "loom: migrated %s → %s\n", legacyDir, newDir)
	return nil
}
