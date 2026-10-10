// Package testenv keeps test binaries off the developer's real loom and
// Claude state.
//
// Every package whose tests can reach the config package's directory
// resolution (config.GetConfigDir, config.GetGlobalConfigDir,
// LoadStateFrom(""), LoadConfigFrom(""), the workspace registry, or a
// worktree created with an empty ConfigDir) calls IsolateLoomDirs from its
// TestMain. Without it an unmarked test resolves ~/.loom and reads or
// writes the developer's live workspaces.json, state.json and worktrees/,
// and a model that never learned the main Claude config dir links test
// accounts into ~/.claude, where a test's write through a link lands.
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
// assert the names agree. EnvClaudeConfigDir is the one account.MainDir
// reads, which TestIsolateLoomDirs_KeepsClaudesMainDirOffTheRealHome
// checks.
const (
	EnvHome      = "LOOM_HOME"
	EnvGlobalDir = "LOOM_GLOBAL_DIR"
	// EnvClaudeTmpDir is Claude Code's temp root override. loom archives
	// and sweeps the per-session dirs under that root, so no test may see
	// the developer's real one, which holds live sessions' scratchpads.
	EnvClaudeTmpDir = "CLAUDE_CODE_TMPDIR"
	// EnvClaudeConfigDir is the main Claude config dir's override, which
	// account.MainDir falls back to (before ~/.claude) when the default
	// account's identity is unknown. Extra accounts link the main dir's
	// entries into their own, so no test may see the developer's real one
	// (or the account dir the developer's shell runs as): a test writing
	// into an account dir would write through the link.
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"
)

// IsolateLoomDirs points LOOM_HOME, LOOM_GLOBAL_DIR, CLAUDE_CODE_TMPDIR and
// CLAUDE_CONFIG_DIR at fresh paths under one new temporary directory, for
// the rest of the process. HOME is left alone (git and other tools read
// it); with CLAUDE_CONFIG_DIR set, nothing in loom resolves ~/.claude. The
// CLAUDE_CONFIG_DIR one is created, empty, as a main config dir would be,
// and so is the CLAUDE_CODE_TMPDIR one, so a real Claude launched by an
// opt-in test finds its root's parent; Claude's own root (claude-<uid>
// inside it) is not, so it reads as absent and every archive operation is
// a no-op unless a test makes one. Call it from TestMain before m.Run and
// run the returned cleanup after it. Tests that need directories of their
// own still t.Setenv over these; the rest are isolated by default.
func IsolateLoomDirs() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "loom-test-dirs")
	if err != nil {
		return nil, fmt.Errorf("create isolated loom dirs: %w", err)
	}
	for name, sub := range map[string]string{EnvHome: "home", EnvGlobalDir: "global", EnvClaudeTmpDir: "claude-tmp", EnvClaudeConfigDir: "claude-config"} {
		if err := os.Setenv(name, filepath.Join(dir, sub)); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	for _, name := range []string{EnvClaudeTmpDir, EnvClaudeConfigDir} {
		if err := os.Mkdir(os.Getenv(name), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("create %s's dir: %w", name, err)
		}
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
