package git

import (
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrBranchGone is returned by Setup when the branch a paused instance
// was pointing at has disappeared both locally and on origin — typically
// because an operator ran `git branch -D <name>` from outside the app.
// Callers use errors.Is to classify the failure and surface a recovery
// hint (kill-to-clean-up) instead of a generic setup error.
var ErrBranchGone = errors.New("branch not found locally or on remote")

// isWorktreeAbsentErr reports whether a `git worktree remove` failure
// was simply "this worktree isn't registered anyway" — the expected,
// no-op case during pre-setup cleanup. Any other failure (permissions,
// locked index, disk error) is the operator's to see.
func isWorktreeAbsentErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "is not a working tree") ||
		strings.Contains(msg, "No such file or directory")
}

// isBranchAbsentErr reports whether a `git branch -D` failure was
// simply "branch doesn't exist" — expected during pre-setup cleanup.
func isBranchAbsentErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}

// worktreeTitleSidecarSuffix is appended to a worktree path to form the
// sidecar file that records its original instance title.
const worktreeTitleSidecarSuffix = ".loom-title"

// WorktreeTitleSidecarPath returns the path of the sidecar file that
// records a worktree's original instance title. It lives next to the
// worktree directory (a sibling file), NOT inside it — keeping it out of
// the work tree avoids polluting git status/diffs. Orphan discovery reads
// it to reconstruct the exact display title — and therefore the exact
// tmux session name — which the lossy branch-name sanitization
// (lowercasing, dash-collapsing) otherwise destroys.
func WorktreeTitleSidecarPath(worktreePath string) string {
	return worktreePath + worktreeTitleSidecarSuffix
}

// writeTitleSidecar records the worktree's original title beside its
// directory. Best-effort: a failure only degrades orphan recovery back to
// the humanized-branch-leaf title, so it must never fail worktree setup.
func writeTitleSidecar(worktreePath, title string) {
	if title == "" {
		return
	}
	path := WorktreeTitleSidecarPath(worktreePath)
	if err := os.WriteFile(path, []byte(title), 0o644); err != nil {
		log.For("git").Debug("worktree.title_sidecar_write_failed", "path", path, "err", err.Error())
	}
}

// removeTitleSidecar deletes the title sidecar if present. Best-effort.
func removeTitleSidecar(worktreePath string) {
	path := WorktreeTitleSidecarPath(worktreePath)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.For("git").Debug("worktree.title_sidecar_remove_failed", "path", path, "err", err.Error())
	}
}

// RemoveTitleSidecar deletes the title sidecar if present. Exported for
// callers outside this package that remove a worktree without going
// through GitWorktree.Cleanup — e.g. session.RemoveOrphanWorktree, which
// force-removes an orphaned worktree directly via `git worktree remove`.
// Best-effort, mirroring removeTitleSidecar.
func RemoveTitleSidecar(worktreePath string) {
	removeTitleSidecar(worktreePath)
}

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() (err error) {
	t0 := time.Now()
	log.For("git").Debug("worktree.setup.begin", "branch", g.branchName, "path", g.worktreePath, "existing_branch", g.isExistingBranch)
	defer func() {
		args := []any{"branch", g.branchName, "duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		log.For("git").Debug("worktree.setup.end", args...)
	}()

	// Ensure worktrees directory exists early (can be done in parallel with branch check)
	worktreesDir, err := getWorktreeDirectory(g.configDir)
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return err
	}

	// If this worktree uses a pre-existing branch, always set up from that branch
	// (it may exist locally or only on the remote).
	switch {
	case g.isExistingBranch:
		err = g.setupFromExistingBranch()
	default:
		// Check if branch exists using git CLI (much faster than go-git PlainOpen)
		if _, refErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName)); refErr == nil {
			err = g.setupFromExistingBranch()
		} else {
			err = g.setupNewWorktree()
		}
	}
	if err == nil {
		// Record the original title so a future orphan scan can recover
		// the exact display title (and tmux session name) for this
		// worktree, which the branch name alone cannot reconstruct.
		writeTitleSidecar(g.worktreePath, g.sessionName)
	}
	return err
}

// clearWorktreePath frees worktreePath so a subsequent `git worktree add`
// can use it.
//
// `git worktree remove` deletes the tree's files and then rmdir's the
// directory. A process still writing inside it — an agent that outlived
// its tmux session, or a build it spawned dropping files into
// node_modules/ or target/ — makes that rmdir fail with ENOTEMPTY. git
// has already unlinked .git by then, so what survives is a half-removed
// worktree: a directory on disk with a registry entry marked `prunable`.
// Merely logging that failure and pressing on makes `worktree add` die
// with a cryptic "already exists", and because nothing self-heals the
// state, every later resume of that session fails the same way forever.
func (g *GitWorktree) clearWorktreePath() error {
	if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil && !isWorktreeAbsentErr(err) {
		log.WarnKV("git.worktree_cleanup_failed", "path", g.worktreePath, "err", err.Error())
	}

	if _, err := os.Stat(g.worktreePath); os.IsNotExist(err) {
		return nil // removed cleanly, or was never there — the common case
	}

	// Drop a registry entry still pointing here, so the branch is not
	// reported as checked out somewhere else by the `worktree add` below.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "prune"); err != nil {
		log.WarnKV("git.worktree_prune_failed", "path", g.worktreePath, "err", err.Error())
	}

	// A surviving .git means this is still a live working tree, which may
	// hold tracked work. Deleting it is not ours to do — say so plainly
	// instead.
	if _, err := os.Stat(filepath.Join(g.worktreePath, ".git")); err == nil {
		return fmt.Errorf("worktree directory %s already exists and is still a live working tree; refusing to remove it", g.worktreePath)
	}

	// No .git, so git can no longer tell us what is dirty here — and a
	// gutted worktree can still hold work that was never committed. Move
	// the leftovers aside rather than deleting them: `worktree add` gets a
	// free path, and anything stranded stays recoverable on disk.
	orphaned, err := freeOrphanPath(g.worktreePath)
	if err != nil {
		return err
	}
	if err := os.Rename(g.worktreePath, orphaned); err != nil {
		return fmt.Errorf("failed to move leftover worktree directory %s aside (a process may still be writing into it): %w", g.worktreePath, err)
	}
	log.WarnKV("git.worktree_leftover_preserved", "path", g.worktreePath, "moved_to", orphaned)
	return nil
}

// freeOrphanPath returns an unused sibling path to park a leftover
// worktree directory at. Sibling rather than child so it never lands
// inside the worktree git is about to recreate, and suffixed rather than
// deleted so stranded uncommitted work survives.
func freeOrphanPath(worktreePath string) (string, error) {
	candidate := worktreePath + ".orphaned"
	for n := 1; ; n++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
		if n > 100 {
			return "", fmt.Errorf("no free path to preserve leftover worktree directory %s: %s and 100 suffixed variants all exist", worktreePath, worktreePath+".orphaned")
		}
		candidate = fmt.Sprintf("%s.orphaned-%d", worktreePath, n)
	}
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	// Free the path for `worktree add`, recovering from a half-removed
	// worktree if one is left in the way.
	if err := g.clearWorktreePath(); err != nil {
		return err
	}

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("%w: %s", ErrBranchGone, g.branchName)
		}
		// Create a local tracking branch via worktree add -b
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, fmt.Sprintf("origin/%s", g.branchName)); err != nil {
			return fmt.Errorf("failed to create worktree from remote branch %s: %w", g.branchName, err)
		}
	} else {
		// Create a new worktree from the existing local branch
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
			return fmt.Errorf("failed to create worktree from branch %s: %w", g.branchName, err)
		}
	}

	// Record the base commit SHA for diff calculations, but only if not already
	// set (e.g. preserved from storage during a resume). Overwriting it would
	// reset the diff baseline to the pause commit, hiding all pre-pause changes.
	if g.GetBaseCommitSHA() == "" {
		output, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("failed to get base commit for existing branch %s: %w", g.branchName, err)
		}
		g.setBaseCommitSHA(strings.TrimSpace(string(output)))
	}

	return nil
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	// Free the path for `worktree add`, recovering from a half-removed
	// worktree if one is left in the way.
	if err := g.clearWorktreePath(); err != nil {
		return err
	}

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen).
	// Absent-branch is expected; anything else (e.g. branch is checked
	// out elsewhere) is operator-visible territory.
	if _, err := g.runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil && !isBranchAbsentErr(err) {
		log.WarnKV("git.branch_cleanup_failed", "branch", g.branchName, "err", err.Error())
	}

	output, err := g.runGitCommand(g.repoPath, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "fatal: ambiguous argument 'HEAD'") ||
			strings.Contains(err.Error(), "fatal: not a valid object name") ||
			strings.Contains(err.Error(), "fatal: HEAD: not a valid object name") {
			return fmt.Errorf("this appears to be a brand new repository: please create an initial commit before creating an instance")
		}
		return fmt.Errorf("failed to get HEAD commit hash: %w", err)
	}
	headCommit := strings.TrimSpace(string(output))
	g.setBaseCommitSHA(headCommit)

	// Create a new worktree from the HEAD commit
	// Otherwise, we'll inherit uncommitted changes from the previous worktree.
	// This way, we can start the worktree with a clean slate.
	// TODO: we might want to give an option to use main/master instead of the current branch.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, headCommit); err != nil {
		return fmt.Errorf("failed to create worktree from commit %s: %w", headCommit, err)
	}

	return nil
}

// Cleanup removes the worktree and associated branch
func (g *GitWorktree) Cleanup() (err error) {
	t0 := time.Now()
	log.For("git").Debug("worktree.cleanup.begin", "branch", g.branchName, "path", g.worktreePath)
	defer func() {
		args := []any{"branch", g.branchName, "duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		log.For("git").Debug("worktree.cleanup.end", args...)
	}()

	var errs []error

	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, but skip if this is a pre-existing branch
	if !g.isExistingBranch {
		if _, err := g.runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil {
			// Only log if it's not a "branch not found" error
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, fmt.Errorf("failed to remove branch %s: %w", g.branchName, err))
			}
		}
	}

	// Prune the worktree to clean up any remaining references
	if err := g.Prune(); err != nil {
		errs = append(errs, err)
	}

	// Drop the title sidecar so it doesn't linger beside a now-removed
	// worktree (best-effort; never blocks cleanup).
	removeTitleSidecar(g.worktreePath)

	if len(errs) > 0 {
		return g.combineErrors(errs)
	}

	return nil
}

// Remove removes the worktree but keeps the branch
func (g *GitWorktree) Remove() error {
	// Remove the worktree using git command
	if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// Prune removes all working tree administrative files and directories
func (g *GitWorktree) Prune() error {
	if _, err := g.runGitCommand(g.repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}
	return nil
}

// CleanupWorktrees removes all worktrees and their associated branches.
// configDir is the workspace config directory; if empty, falls back to GetConfigDir().
// Pass nil for runner to use the default subprocess runner.
func CleanupWorktrees(configDir string, runner CommandRunner) error {
	r := defaultRunner(runner)
	worktreesDir, err := getWorktreeDirectory(configDir)
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return fmt.Errorf("failed to read worktree directory: %w", err)
	}

	// Group worktree directories by their parent repo root
	repoWorktrees := make(map[string][]string) // repoRoot -> []worktreePath

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		worktreePath := filepath.Join(worktreesDir, entry.Name())

		// Use findMainRepoRoot so the path still resolves once the linked
		// worktree directory is removed below — branch deletion has to
		// run against the main repo, not the now-gone worktree.
		repoRoot, err := findMainRepoRoot(worktreePath)
		if err != nil {
			// Can't determine repo (e.g. .git file missing) — just remove the directory
			_ = os.RemoveAll(worktreePath)
			continue
		}
		repoWorktrees[repoRoot] = append(repoWorktrees[repoRoot], worktreePath)
	}

	// For each repo, resolve branches via worktree list, then clean up.
	// Ordering is important: `git branch -D` fails while a branch's worktree
	// is still registered, so we must remove the worktree directory and prune
	// the registration *before* deleting the branch.
	var errs []error
	for repoRoot, worktreePaths := range repoWorktrees {
		listCtx, listCancel := context.WithTimeout(context.Background(), gitTimeout)
		listCmd := exec.CommandContext(listCtx, "git", "-C", repoRoot, "worktree", "list", "--porcelain")
		output, listErr := r.Output(listCmd)
		listCancel()
		if listErr != nil {
			// Can't resolve branches for this repo. Record the error so the
			// caller knows branches may have leaked, but still remove the
			// worktree directories so the cleanup is at least partially done.
			errs = append(errs, fmt.Errorf("failed to list worktrees for %s: %w", repoRoot, listErr))
		}

		worktreeBranches := make(map[string]string)
		var currentWorktree string
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "worktree ") {
				currentWorktree = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch ") {
				branchPath := strings.TrimPrefix(line, "branch ")
				branchName := strings.TrimPrefix(branchPath, "refs/heads/")
				if currentWorktree != "" {
					worktreeBranches[currentWorktree] = branchName
				}
			}
		}

		for _, wtPath := range worktreePaths {
			if err := os.RemoveAll(wtPath); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove worktree %s: %w", wtPath, err))
			}
		}

		pruneCtx, pruneCancel := context.WithTimeout(context.Background(), gitTimeout)
		pruneCmd := exec.CommandContext(pruneCtx, "git", "-C", repoRoot, "worktree", "prune")
		if err := r.Run(pruneCmd); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune worktrees for %s: %w", repoRoot, err))
		}
		pruneCancel()

		for _, wtPath := range worktreePaths {
			branch, ok := worktreeBranches[wtPath]
			if !ok {
				continue
			}
			delCtx, delCancel := context.WithTimeout(context.Background(), gitTimeout)
			deleteCmd := exec.CommandContext(delCtx, "git", "-C", repoRoot, "branch", "-D", branch)
			if err := r.Run(deleteCmd); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete branch %s: %w", branch, err))
			}
			delCancel()
		}
	}

	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return fmt.Errorf("cleanup errors: %s", strings.Join(msgs, "; "))
	}
	return nil
}
