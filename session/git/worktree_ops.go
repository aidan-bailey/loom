package git

import (
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"os"
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

// TreeState is what InspectTree found at a worktree's path.
type TreeState int

const (
	// TreeAbsent means nothing is on disk at the path.
	TreeAbsent TreeState = iota
	// TreeGutted means the path is on disk but has no .git entry: git has
	// already let go of it (typically a half-finished `worktree remove`).
	// Only leftovers remain, which clearWorktreePath moves aside rather
	// than deletes.
	TreeGutted
	// TreeIntact means git recognizes the path as a working tree of its
	// own. Whatever it holds, committed or not, is live work.
	TreeIntact
	// TreeUnverified means the path has a .git entry but git could not
	// confirm it as a working tree rooted there (an error, a timeout, or
	// a .git git does not accept). Nothing is known about what it holds.
	TreeUnverified
)

// String implements fmt.Stringer for logs and errors.
func (s TreeState) String() string {
	switch s {
	case TreeAbsent:
		return "absent"
	case TreeGutted:
		return "gutted"
	case TreeIntact:
		return "intact"
	case TreeUnverified:
		return "unverified"
	default:
		return fmt.Sprintf("TreeState(%d)", int(s))
	}
}

// InspectTree classifies what is on disk at the worktree path. The error
// accompanies TreeUnverified and says why the tree could not be confirmed;
// IsTimeout tells a git that never answered apart from one that rejected
// the tree.
//
// The .git check comes first and is load-bearing, not an optimization:
// worktrees can live inside the repo they belong to (a workspace keeps
// them under <repo>/.loom/worktrees), so git run from a directory that
// lost its .git walks up and answers for the enclosing repo. An intact
// verdict lets Resume launch an agent into the tree, so git must also
// report the path as its own top level and as a linked worktree of this
// session's repository (not an unrelated repo, the main checkout, or a
// submodule), and the worktree must not still be locked "initializing" by
// a `git worktree add` that never finished.
func (g *GitWorktree) InspectTree() (TreeState, error) {
	fi, err := os.Stat(g.worktreePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return TreeAbsent, nil
	case err != nil:
		return TreeUnverified, fmt.Errorf("stat worktree %s: %w", g.worktreePath, err)
	case !fi.IsDir():
		return TreeGutted, nil
	}

	// os.Stat, like clearWorktreePath's own check, so the two agree on
	// which trees count as gutted.
	if _, err := os.Stat(filepath.Join(g.worktreePath, ".git")); errors.Is(err, os.ErrNotExist) {
		return TreeGutted, nil
	} else if err != nil {
		return TreeUnverified, fmt.Errorf("stat %s/.git: %w", g.worktreePath, err)
	}

	out, err := g.runGitStdout(g.worktreePath, "rev-parse", "--path-format=absolute",
		"--is-inside-work-tree", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return TreeUnverified, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "true" {
		return TreeUnverified, fmt.Errorf("git does not consider %s a working tree: %q", g.worktreePath, strings.TrimSpace(out))
	}
	top, gitDir, commonDir := lines[1], lines[2], lines[3]
	if !sameFile(top, g.worktreePath) {
		return TreeUnverified, fmt.Errorf("git resolves %s to the working tree at %s, not its own", g.worktreePath, top)
	}

	repoCommon, err := g.runGitStdout(g.repoPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return TreeUnverified, fmt.Errorf("resolve the git dir of repository %s: %w", g.repoPath, err)
	}
	if !sameFile(commonDir, strings.TrimSpace(repoCommon)) {
		return TreeUnverified, fmt.Errorf("%s belongs to the repository at %s, not %s", g.worktreePath, commonDir, g.repoPath)
	}
	if sameFile(gitDir, commonDir) {
		return TreeUnverified, fmt.Errorf("%s is the main checkout of %s, not a linked worktree", g.worktreePath, g.repoPath)
	}
	// `git worktree add` holds this lock until its checkout completes.
	// Loom's git runs with English messages, so the reason is literal.
	if reason, err := os.ReadFile(filepath.Join(gitDir, "locked")); err == nil && strings.TrimSpace(string(reason)) == "initializing" {
		return TreeUnverified, fmt.Errorf("%s is still locked \"initializing\": the `git worktree add` that created it never finished", g.worktreePath)
	}
	return TreeIntact, nil
}

// sameFile reports whether a and b name the same file or directory: the
// stored path may reach it through symlinks or a bind mount, or differ in
// case on a case-insensitive filesystem, where comparing strings would not.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
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
	if _, err := g.removeWorktree(); err != nil && !isWorktreeAbsentErr(err) {
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
		g.setBranchCreated()
	} else {
		// Create a new worktree from the existing local branch
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
			return fmt.Errorf("failed to create worktree from branch %s: %w", g.branchName, err)
		}
	}

	// Record the base commit SHA for diff calculations, but only if not already
	// set (e.g. preserved from storage during a resume). Overwriting it would
	// reset the diff baseline to the pause commit, hiding all pre-pause changes.
	if err := g.EnsureBaseCommit(); err != nil {
		return fmt.Errorf("failed to get base commit for existing branch %s: %w", g.branchName, err)
	}

	return nil
}

// EnsureBaseCommit records the worktree's HEAD as its base commit when none
// is recorded, as a rebuild from an existing branch does. A session whose
// start was interrupted reaches Resume without one, and without it the
// session never gets diff stats. A recorded base is never overwritten.
func (g *GitWorktree) EnsureBaseCommit() error {
	if g.GetBaseCommitSHA() != "" {
		return nil
	}
	out, err := g.runGitStdout(g.worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	g.setBaseCommitSHA(strings.TrimSpace(out))
	return nil
}

// setupNewWorktree creates a new worktree rooted at the resolved base
// commit (see ResolveBaseCommit).
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

	// The configured base is read here rather than cached on the struct so
	// every constructor — including NewGitWorktreeFromStorage, which does no
	// config load — behaves the same. This is the only path that needs it,
	// and it runs once per session creation, not on any hot path.
	baseSHA, baseName, err := ResolveBaseCommit(g.repoPath, config.LoadConfigFrom(g.configDir).GetBaseBranch(), g.runner)
	if err != nil {
		return err
	}
	g.setBaseCommitSHA(baseSHA)
	log.For("git").Debug("worktree.base_resolved", "branch", g.branchName, "base_ref", baseName, "base_sha", baseSHA)

	// Create the worktree at the resolved base commit rather than at the root
	// repo's HEAD: a feature branch checked out in the main checkout must not
	// silently become every new session's starting point. Pinning a commit
	// (not a branch name) also keeps the new worktree free of any uncommitted
	// changes sitting in the main checkout.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, baseSHA); err != nil {
		return fmt.Errorf("failed to create worktree from %s (%s): %w", baseName, baseSHA, err)
	}
	g.setBranchCreated()

	return nil
}

// Cleanup removes the worktree and associated branch
func (g *GitWorktree) Cleanup() error {
	return g.cleanup(!g.isExistingBranch)
}

// CleanupFailedStart undoes a Setup whose session then failed to start: it
// removes the worktree Setup just created, but deletes the branch only if
// Setup created it too. Setup also checks out a branch that already
// existed — one left behind by an earlier session with the same title,
// whose commits may be unmerged — and that one is kept.
func (g *GitWorktree) CleanupFailedStart() error {
	return g.cleanup(!g.isExistingBranch && g.createdBranch())
}

// cleanup removes the worktree, prunes, drops the title sidecar and, when
// deleteBranch is set, deletes the branch.
func (g *GitWorktree) cleanup(deleteBranch bool) (err error) {
	t0 := time.Now()
	log.For("git").Debug("worktree.cleanup.begin", "branch", g.branchName, "path", g.worktreePath, "delete_branch", deleteBranch)
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
		if _, err := g.removeWorktree(); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, unless the caller keeps it
	if deleteBranch {
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
	if _, err := g.removeWorktree(); err != nil {
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
		repoRoot, err := findMainRepoRoot(worktreePath, r)
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
		listCmd := internalexec.GitCommand(listCtx, repoRoot, "worktree", "list", "--porcelain")
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
		pruneCmd := internalexec.GitCommand(pruneCtx, repoRoot, "worktree", "prune")
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
			deleteCmd := internalexec.GitCommand(delCtx, repoRoot, "branch", "-D", branch)
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
