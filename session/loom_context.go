package session

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aidan-bailey/loom/config"
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

// dirFlags is a boolean setting kept per config dir. Each workspace has its
// own config, and the model keeps several workspaces loaded at once, so a
// launch reads the setting of its own instance's config dir. Safe for a
// launch job's read against the model's writes.
type dirFlags struct{ m sync.Map }

func (f *dirFlags) set(dir string, v bool) { f.m.Store(dir, v) }

// get is dir's setting, or def for a dir never set.
func (f *dirFlags) get(dir string, def bool) bool {
	if v, ok := f.m.Load(dir); ok {
		return v.(bool)
	}
	return def
}

// loomContextOn mirrors config.LoomContextEnabled() per config dir; the
// model sets it from each workspace's config when it loads the workspace
// and after a settings change. A dir never set reads as off.
var loomContextOn dirFlags

// SetLoomContextEnabled sets the loom-context toggle of the sessions whose
// config dir is configDir.
func SetLoomContextEnabled(configDir string, enabled bool) { loomContextOn.set(configDir, enabled) }

// LoomContextEnabled reports configDir's loom-context toggle.
func LoomContextEnabled(configDir string) bool { return loomContextOn.get(configDir, false) }

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
		if err := config.AtomicWriteFile(path, f.content, 0o644); err != nil {
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
	if configDir == "" || !LoomContextEnabled(configDir) {
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
