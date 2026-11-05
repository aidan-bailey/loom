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

// Loom-context prompt files, written to the config dir by
// WriteLoomContextFiles and appended to every Claude launch's system
// prompt. The worktree variant orients an isolated-worktree session, the
// workspace variant the root-repo main session (IsWorkspaceTerminal). Each
// is its own head followed by the fragments both share (loomContextFile).
const (
	loomContextFileWorktree  = "claude-loom-context.md"
	loomContextFileWorkspace = "claude-loom-context-workspace.md"
)

// The embedded fragments the prompt files are built from.
var (
	//go:embed loom_context_worktree.md
	loomContextWorktreeHead []byte
	//go:embed loom_context_main.md
	loomContextMainHead []byte
	//go:embed loom_context_shared.md
	loomContextShared []byte
	//go:embed loom_context_sessions.md
	loomContextSessions []byte
	//go:embed loom_context_subagent.md
	loomContextSubagentHead []byte
	//go:embed loom_context_flow.md
	loomContextFlow []byte
)

// loomContextFile is a prompt file's content: head, then the shared
// fragment ("Loom itself"), then the sessions fragment ("Other
// sessions"), then, withFlow, the flow fragment ("The agent flow"), with
// a blank line at each seam (every fragment ends in one newline).
// bytes.Join builds a slice of its own, so no append ever writes into an
// embedded one.
func loomContextFile(head []byte, withFlow bool) []byte {
	parts := [][]byte{head, loomContextShared, loomContextSessions}
	if withFlow {
		parts = append(parts, loomContextFlow)
	}
	return bytes.Join(parts, []byte("\n"))
}

// subagentContextFile is a subagent's cut of the prompt whose head is
// head: the subagent head, then head and the shared fragment, then,
// withFlow, the flow fragment. The sessions fragment stays the parent's,
// since messaging and starting sessions is its job. Loom's SubagentStart
// hook hands it to every subagent but a fork (subagentBrief,
// hooks.Prepare).
func subagentContextFile(head []byte, withFlow bool) []byte {
	parts := [][]byte{loomContextSubagentHead, head, loomContextShared}
	if withFlow {
		parts = append(parts, loomContextFlow)
	}
	return bytes.Join(parts, []byte("\n"))
}

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

// WriteLoomContextFiles writes both prompt files (loomContextFile, with
// the flow fragment when withFlow) into configDir, (re)writing a file only
// when it is missing or its bytes differ (so a loom upgrade or a change of
// the flow setting refreshes the prose automatically). A no-op when
// configDir is empty.
func WriteLoomContextFiles(configDir string, withFlow bool) error {
	if configDir == "" {
		return nil
	}
	files := []struct {
		name    string
		content []byte
	}{
		{loomContextFileWorktree, loomContextFile(loomContextWorktreeHead, withFlow)},
		{loomContextFileWorkspace, loomContextFile(loomContextMainHead, withFlow)},
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

// loomContextPath is the prompt file a launch in configDir points Claude
// at, and whether it gets one: the feature is on for configDir and the
// variant's file exists (fail-safe: never point Claude at a nonexistent
// file). Workspace terminals get the workspace variant, the rest the
// worktree variant.
func loomContextPath(configDir string, isWorkspaceTerminal bool) (string, bool) {
	if configDir == "" || !LoomContextEnabled(configDir) {
		return "", false
	}
	name := loomContextFileWorktree
	if isWorkspaceTerminal {
		name = loomContextFileWorkspace
	}
	path := filepath.Join(configDir, name)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// loomContextProgram wraps program with the loom-context flag for launch,
// pointing at loomContextPath's file. Returns program unchanged when there
// is none. Non-Claude programs are a no-op via BuildLoomContextCommand.
func loomContextProgram(program, configDir string, isWorkspaceTerminal bool) string {
	path, ok := loomContextPath(configDir, isWorkspaceTerminal)
	if !ok {
		return program
	}
	return BuildLoomContextCommand(program, path)
}

// subagentBrief is what loom's SubagentStart hook tells each subagent of
// a program launched in configDir: the cut (subagentContextFile) of the
// prompt loomContextProgram gives the program itself, or "" when it gives
// none (the feature off, the file missing, a program other than Claude).
func subagentBrief(program, configDir string, isWorkspaceTerminal bool) string {
	if _, ok := loomContextPath(configDir, isWorkspaceTerminal); !ok || !IsClaudeProgram(program) {
		return ""
	}
	head := loomContextWorktreeHead
	if isWorkspaceTerminal {
		head = loomContextMainHead
	}
	return string(subagentContextFile(head, FlowEnabled(configDir)))
}
