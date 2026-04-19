package session

import (
	"fmt"
	"github.com/aidan-bailey/loom/session/git"
	"path/filepath"
)

// SessionBackend is the polymorphic surface for operations that differ
// between regular (worktree-backed) instances and workspace terminals.
// It consolidates scattered IsWorkspaceTerminal branching into a single
// indirection so new backend types (e.g., remote sessions) can be added
// by implementing this interface rather than extending a switch.
//
// Scope note: lifecycle operations (Setup/Kill/Pause/Resume) still
// branch inline in Instance because they interact with shared state
// (tmux session, lock bookkeeping) in ways that don't fit cleanly
// behind this interface. Migrating those is a follow-up.
type SessionBackend interface {
	// RepoName returns the human-visible repo/workspace name used in
	// UI labels.
	RepoName() (string, error)
	// WorkTreePath returns the filesystem directory where the tmux
	// session should operate.
	WorkTreePath() string
	// Diff returns the current diff stats (line counts + content).
	Diff() *git.DiffStats
	// DiffShort returns diff line counts only, without content body —
	// cheaper for non-focused instances that only render counts.
	DiffShort() *git.DiffStats
	// RefreshBranch returns the current branch name, or empty string
	// if the backend doesn't need to refresh (worktrees have a fixed
	// branch for their lifetime; workspace terminals track HEAD).
	RefreshBranch() string
}

// worktreeBackend is the default backend for regular instances that
// live in a git worktree.
type worktreeBackend struct {
	inst *Instance
}

func (b *worktreeBackend) RepoName() (string, error) {
	if !b.inst.isStarted() {
		return "", fmt.Errorf("cannot get repo name for instance that has not been started")
	}
	return b.inst.getGitWorktree().GetRepoName(), nil
}

func (b *worktreeBackend) WorkTreePath() string {
	gw := b.inst.getGitWorktree()
	if gw == nil {
		return ""
	}
	return gw.GetWorktreePath()
}

func (b *worktreeBackend) Diff() *git.DiffStats {
	gw := b.inst.getGitWorktree()
	if gw == nil {
		return &git.DiffStats{}
	}
	return gw.Diff()
}

func (b *worktreeBackend) DiffShort() *git.DiffStats {
	gw := b.inst.getGitWorktree()
	if gw == nil {
		return &git.DiffStats{}
	}
	return gw.DiffShortStat()
}

func (b *worktreeBackend) RefreshBranch() string {
	return ""
}

// workspaceTerminalBackend powers instances that run directly in the
// root repo without a worktree (IsWorkspaceTerminal == true).
type workspaceTerminalBackend struct {
	inst *Instance
}

func (b *workspaceTerminalBackend) RepoName() (string, error) {
	return filepath.Base(b.inst.Path), nil
}

func (b *workspaceTerminalBackend) WorkTreePath() string {
	return b.inst.Path
}

func (b *workspaceTerminalBackend) Diff() *git.DiffStats {
	return git.DiffUncommitted(b.inst.Path, nil)
}

func (b *workspaceTerminalBackend) DiffShort() *git.DiffStats {
	return git.DiffUncommittedShortStat(b.inst.Path, nil)
}

func (b *workspaceTerminalBackend) RefreshBranch() string {
	branch, err := git.CurrentBranch(b.inst.Path, nil)
	if err != nil {
		return ""
	}
	return branch
}

// backend returns the appropriate SessionBackend for this instance.
// The discriminator is still IsWorkspaceTerminal for now; storage
// format migration to a tagged variant is a follow-up.
func (i *Instance) backend() SessionBackend {
	if i.IsWorkspaceTerminal {
		return &workspaceTerminalBackend{inst: i}
	}
	return &worktreeBackend{inst: i}
}
