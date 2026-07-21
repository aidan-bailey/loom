package session

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Loom-context prompt files, embedded at build time and written to the
// config dir by WriteLoomContextFiles. The worktree variant orients an
// isolated-worktree session; the workspace variant orients the root-repo
// main session (IsWorkspaceTerminal).
const (
	loomContextFileWorktree  = "claude-loom-context.md"
	loomContextFileWorkspace = "claude-loom-context-workspace.md"
)

//go:embed claude-loom-context.md
var loomContextWorktreeBytes []byte

//go:embed claude-loom-context-workspace.md
var loomContextWorkspaceBytes []byte

// loomContextEnabled mirrors config.LoomContextEnabled(); the app sets it
// from config (SetLoomContextEnabled) on startup, workspace activation,
// and after a settings toggle. atomic.Bool keeps the launch-time read
// race-safe against the main-goroutine writes.
var loomContextEnabled atomic.Bool

// SetLoomContextEnabled updates the global loom-context toggle.
func SetLoomContextEnabled(enabled bool) { loomContextEnabled.Store(enabled) }

// WriteLoomContextFiles writes both embedded prompt files into configDir,
// (re)writing a file only when it is missing or its bytes differ from the
// embedded content (so a loom upgrade refreshes the prose automatically).
// A no-op when configDir is empty.
func WriteLoomContextFiles(configDir string) error {
	if configDir == "" {
		return nil
	}
	files := []struct {
		name    string
		content []byte
	}{
		{loomContextFileWorktree, loomContextWorktreeBytes},
		{loomContextFileWorkspace, loomContextWorkspaceBytes},
	}
	for _, f := range files {
		path := filepath.Join(configDir, f.name)
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, f.content) {
			continue
		}
		if err := os.WriteFile(path, f.content, 0o644); err != nil {
			return fmt.Errorf("write loom context %s: %w", f.name, err)
		}
	}
	return nil
}

// BuildLoomContextCommand returns program with Claude's
// --append-system-prompt-file flag pointing at filePath. The adapter
// registry no-ops for non-Claude programs.
func BuildLoomContextCommand(program, filePath string) string {
	return defaultRegistry.Lookup(program).ApplyLoomContextFlag(program, filePath)
}

// loomContextProgram wraps program with the loom-context flag for launch.
// Returns program unchanged when the feature is disabled, configDir is
// empty, or the selected file is missing (fail-safe: never point Claude
// at a nonexistent file). Selects the workspace variant for workspace
// terminals, else the worktree variant. Non-Claude programs are a no-op
// via BuildLoomContextCommand.
func loomContextProgram(program, configDir string, isWorkspaceTerminal bool) string {
	if !loomContextEnabled.Load() || configDir == "" {
		return program
	}
	name := loomContextFileWorktree
	if isWorkspaceTerminal {
		name = loomContextFileWorkspace
	}
	path := filepath.Join(configDir, name)
	if _, err := os.Stat(path); err != nil {
		return program
	}
	return BuildLoomContextCommand(program, path)
}
