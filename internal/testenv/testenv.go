// Package testenv keeps test binaries off the developer's real loom state.
//
// Every package whose tests can reach the config package's directory
// resolution (config.GetConfigDir, config.GetGlobalConfigDir,
// LoadStateFrom(""), LoadConfigFrom(""), the workspace registry, or a
// worktree created with an empty ConfigDir) calls IsolateLoomDirs from its
// TestMain. Without it an unmarked test resolves ~/.loom and reads or
// writes the developer's live workspaces.json, state.json and worktrees/.
//
// Only test code imports this package.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// The variables IsolateLoomDirs sets. EnvHome and EnvGlobalDir mirror
// config.EnvHome and config.EnvGlobalDir, and EnvClaudeTmpDir mirrors
// claudetmp.EnvTmpDir: this package cannot import those, so their tests
// assert the names agree.
const (
	EnvHome      = "LOOM_HOME"
	EnvGlobalDir = "LOOM_GLOBAL_DIR"
	// EnvClaudeTmpDir is Claude Code's temp root override. loom archives
	// and sweeps the per-session dirs under that root, so no test may see
	// the developer's real one, which holds live sessions' scratchpads.
	EnvClaudeTmpDir = "CLAUDE_CODE_TMPDIR"
)

// IsolateLoomDirs points LOOM_HOME, LOOM_GLOBAL_DIR and CLAUDE_CODE_TMPDIR
// at fresh paths under one new temporary directory, for the rest of the
// process. Only the CLAUDE_CODE_TMPDIR one is created, empty, so a real
// Claude launched by an opt-in test finds its root's parent; Claude's own
// root (claude-<uid> inside it) is not, so it reads as absent and every
// archive operation is a no-op unless a test makes one. Call it from
// TestMain before m.Run and run the returned cleanup after it. Tests that
// need directories of their own still t.Setenv over these; the rest are
// isolated by default.
func IsolateLoomDirs() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "loom-test-dirs")
	if err != nil {
		return nil, fmt.Errorf("create isolated loom dirs: %w", err)
	}
	for name, sub := range map[string]string{EnvHome: "home", EnvGlobalDir: "global", EnvClaudeTmpDir: "claude-tmp"} {
		if err := os.Setenv(name, filepath.Join(dir, sub)); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	if err := os.Mkdir(os.Getenv(EnvClaudeTmpDir), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("create %s's dir: %w", EnvClaudeTmpDir, err)
	}
	return func() { _ = os.RemoveAll(dir) }, nil
}

// MustIsolateLoomDirs is IsolateLoomDirs for a TestMain that has no other
// setup to unwind: on failure it prints the error and exits with status 1,
// before any test can run against the real directories.
func MustIsolateLoomDirs() (cleanup func()) {
	cleanup, err := IsolateLoomDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: %v\n", err)
		os.Exit(1)
	}
	return cleanup
}
