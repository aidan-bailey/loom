package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// MaxBranchSearchResults is the maximum number of branches returned by SearchBranches.
const MaxBranchSearchResults = 50

// gitTimeout bounds any single git subprocess. The metadata tick fans these out
// on every tick, so a hung git process would freeze the UI without this cap.
// A var, not a const, so tests can shorten it.
var gitTimeout = 8 * time.Second

// gitWorktreeRemoveTimeout bounds `git worktree remove`. It must NOT share
// gitTimeout: that budget is sized for the metadata tick, but a remove
// walks and unlinks the whole tree — a built Rust worktree with a
// multi-GB target/ easily outlives 8s on a loaded box — and killing git
// mid-delete is not a clean failure. It leaves a half-removed worktree:
// .git already unlinked, sources partly gone, registry entry prunable.
// That is exactly the state that wedged resume on 2026-08-21. Remove runs
// from Pause/Kill/Setup in a tea.Cmd goroutine, never on the tick, so a
// generous bound costs nothing; it exists only so a truly hung git (dead
// NFS, D-state I/O) cannot pin the operation forever.
var gitWorktreeRemoveTimeout = 5 * time.Minute

// WorktreeRemoveTimeout exposes the `git worktree remove` deadline to the
// one caller outside this package that removes a worktree directly
// (session.RemoveOrphanWorktree), so it cannot fall back to a tick-sized
// budget and reintroduce the half-removed-worktree failure.
func WorktreeRemoveTimeout() time.Duration { return gitWorktreeRemoveTimeout }

// gitWorktreeAddTimeout bounds `git worktree add`, which checks out the
// whole tree and so scales with its size exactly as a remove does. Under
// the 8s tick budget a slow checkout of a large repo was killed half-way,
// leaving the new worktree locked "initializing": a tree InspectTree
// cannot vouch for, and, once moved aside, a registry entry that blocked
// every later `worktree add` at that path. Add runs from Start and Resume
// in a tea.Cmd goroutine, never on the tick.
var gitWorktreeAddTimeout = 5 * time.Minute

// gitNetworkTimeout applies to commands that talk to a remote (push/sync/fetch).
const gitNetworkTimeout = 30 * time.Second

// gitOkEvery rate-limits the `git.cmd.ok` debug record. runGitCommand fires
// on every git invocation — metadata ticks alone emit many per second — so an
// un-gated debug trace swamps the log at --log-level=debug. The timer lets one
// success record through per window so operators can still confirm git is
// running, without floods. Failures and timeouts bypass this gate because they
// are rare and diagnostic.
var gitOkEvery = log.NewEvery(5 * time.Second)

// FetchBranches fetches and prunes remote-tracking branches (best-effort, won't fail if offline).
// Pass nil for runner to use the default subprocess runner.
func FetchBranches(repoPath string, runner CommandRunner) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, repoPath, "fetch", "--prune")
	_ = r.Run(c)
}

// SearchBranches searches for branches whose name contains filter (case-insensitive),
// ordered by most recently updated first. Returns at most MaxBranchSearchResults.
// If filter is empty, returns all branches up to the limit.
// Pass nil for runner to use the default subprocess runner.
func SearchBranches(repoPath, filter string, runner CommandRunner) ([]string, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, repoPath, "branch", "-a",
		"--sort=-committerdate",
		"--format=%(refname:short)")
	output, err := r.CombinedOutput(c)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %s (%w)", output, err)
	}

	seen := make(map[string]bool)
	var branches []string
	lower := strings.ToLower(filter)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "HEAD") {
			continue
		}
		name := strings.TrimPrefix(line, "origin/")
		if seen[name] {
			continue
		}
		seen[name] = true
		if filter != "" && !strings.Contains(strings.ToLower(name), lower) {
			continue
		}
		branches = append(branches, name)
		if len(branches) >= MaxBranchSearchResults {
			break
		}
	}
	return branches, nil
}

// runGitCommand executes a git command and returns any error.
// Applies gitTimeout to bound wall time — critical for the metadata tick,
// which fans this out every few seconds.
func (g *GitWorktree) runGitCommand(path string, args ...string) (string, error) {
	return g.runGitCommandEnvTimeout(nil, gitTimeout, path, args...)
}

// runGitCommandTimeout is runGitCommand with an explicit deadline, for the
// few commands whose wall time scales with tree size rather than with the
// tick budget (see gitWorktreeRemoveTimeout).
func (g *GitWorktree) runGitCommandTimeout(timeout time.Duration, path string, args ...string) (string, error) {
	return g.runGitCommandEnvTimeout(nil, timeout, path, args...)
}

// removeWorktree runs `git worktree remove -f` under the remove-specific
// deadline. Every remove site must go through here rather than
// runGitCommand so none of them inherit the tick budget.
func (g *GitWorktree) removeWorktree() (string, error) {
	return g.runGitCommandTimeout(gitWorktreeRemoveTimeout, g.repoPath, "worktree", "remove", "-f", g.worktreePath)
}

// addWorktree runs `git worktree add <args>` from the repository under the
// add-specific deadline (see gitWorktreeAddTimeout). Every add site goes
// through here.
func (g *GitWorktree) addWorktree(args ...string) (string, error) {
	return g.runGitCommandTimeout(gitWorktreeAddTimeout, g.repoPath, append([]string{"worktree", "add"}, args...)...)
}

// runGitCommandEnv is runGitCommand with additional environment variables
// appended onto the process's own environment (e.g. GIT_INDEX_FILE to
// build a tree against a scratch index without touching the real one).
// They land after GitCommand's locale overrides, so they win on a duplicate key.
// Pass nil extraEnv to behave exactly like runGitCommand.
func (g *GitWorktree) runGitCommandEnv(extraEnv []string, path string, args ...string) (string, error) {
	return g.runGitCommandEnvTimeout(extraEnv, gitTimeout, path, args...)
}

// runGitCommandEnvTimeout is runGitCore with stdout and stderr combined,
// the form every runGitCommand* variant returns.
func (g *GitWorktree) runGitCommandEnvTimeout(extraEnv []string, timeout time.Duration, path string, args ...string) (string, error) {
	return g.runGitCore(extraEnv, timeout, false, path, args...)
}

// runGitStdout is runGitCommand for callers that parse the output: it
// returns stdout alone, so nothing git writes to stderr (a warning, a
// GIT_TRACE line) can be mistaken for the answer. Stderr still reaches
// the error on failure.
func (g *GitWorktree) runGitStdout(path string, args ...string) (string, error) {
	return g.runGitCore(nil, gitTimeout, true, path, args...)
}

// ErrTimeout marks a git command killed at its deadline: git never
// answered, which says nothing about the state it was asked about.
var ErrTimeout = errors.New("git did not answer in time")

// IsTimeout reports whether err comes from a git command that timed out.
func IsTimeout(err error) bool { return errors.Is(err, ErrTimeout) }

// runGitCore is the single implementation behind every git runner here:
// env overlay, explicit deadline, and combined or stdout-only output.
func (g *GitWorktree) runGitCore(extraEnv []string, timeout time.Duration, stdoutOnly bool, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, path, args...)
	c.Env = append(c.Env, extraEnv...)

	t0 := time.Now()
	var output []byte
	var err error
	if stdoutOnly {
		output, err = g.runner.Output(c)
	} else {
		output, err = g.runner.CombinedOutput(c)
	}
	if ctx.Err() == context.DeadlineExceeded {
		log.For("git").Debug("git.cmd.timeout", "cmd", strings.Join(args, " "), "path", path, "timeout_ms", timeout.Milliseconds())
		return "", fmt.Errorf("git command timed out after %s: git %s: %w", timeout, strings.Join(args, " "), ErrTimeout)
	}
	if err != nil {
		if stdoutOnly {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				output = exitErr.Stderr
			}
		}
		// Debug because many callers intentionally ignore "branch not found" /
		// "worktree doesn't exist" errors. Elevating to Warn would spam. Callers
		// that want Warn/Error semantics do so at their layer.
		log.For("git").Debug("git.cmd.failed", "cmd", strings.Join(args, " "), "path", path, "duration_ms", time.Since(t0).Milliseconds(), "err", err.Error(), "output", strings.TrimSpace(string(output)))
		return "", fmt.Errorf("git command failed: %s (%w)", strings.TrimSpace(string(output)), err)
	}
	if gitOkEvery.ShouldLog() {
		log.For("git").Debug("git.cmd.ok", "cmd", strings.Join(args, " "), "path", path, "duration_ms", time.Since(t0).Milliseconds())
	}

	return string(output), nil
}

// PushChanges commits and pushes changes in the worktree to the remote branch
func (g *GitWorktree) PushChanges(commitMessage string, open bool) error {
	if err := g.checkGHCLI(); err != nil {
		return err
	}

	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	// First push the branch to remote to ensure it exists
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	pushCmd := internalexec.GhCommand(ctx, "repo", "sync", "--source", "-b", g.branchName)
	pushCmd.Dir = g.worktreePath
	if err := g.runner.Run(pushCmd); err != nil {
		// Fallback needs its own deadline: a slow `gh repo sync` above can
		// burn through most or all of ctx's budget before failing, which would
		// cancel the push before it even dials.
		fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
		defer fallbackCancel()
		gitPushCmd := internalexec.GitCommand(fallbackCtx, g.worktreePath, "push", "-u", "origin", g.branchName)
		if pushOutput, pushErr := g.runner.CombinedOutput(gitPushCmd); pushErr != nil {
			return fmt.Errorf("failed to push branch: %s (%w)", pushOutput, pushErr)
		}
	}

	// Now sync with remote
	syncCtx, syncCancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer syncCancel()
	syncCmd := internalexec.GhCommand(syncCtx, "repo", "sync", "-b", g.branchName)
	syncCmd.Dir = g.worktreePath
	if output, err := g.runner.CombinedOutput(syncCmd); err != nil {
		return fmt.Errorf("failed to sync changes: %s (%w)", output, err)
	}

	// Open the branch in the browser
	if open {
		if err := g.OpenBranchURL(); err != nil {
			// Just log the error but don't fail the push operation
			log.For("git").Error("open_branch_url_failed", "err", err)
		}
	}

	return nil
}

// CommitChanges commits changes locally without pushing to remote
func (g *GitWorktree) CommitChanges(commitMessage string) error {
	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit (local only)
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	return nil
}

// StashChanges snapshots the worktree's tracked and untracked changes
// into a stash commit without disturbing the shared stash stack's
// ordering, and without ever calling `git stash push`/`create` (both
// pass through the same option parser, which documents only
// `git stash create [<message>]` — no -u/--include-untracked; passing
// it anyway silently swallows the flag into the commit message and
// drops untracked files from the snapshot, verified empirically
// against the installed git). Instead this builds, by hand, the same
// two-parent "index on ..." / "On ..." commit pair `git stash push -u`
// itself would produce, via plumbing against a scratch
// GIT_INDEX_FILE so the real index and working tree are left
// untouched:
//   - iCommit: tree = HEAD plus staged/unstaged modifications to
//     already-tracked files (`add -u`), parent = HEAD. This is the
//     "index" side of the stash.
//   - the returned commit: tree = iCommit's tree plus untracked files
//     (`add -A` on the same scratch index), parents = [HEAD, iCommit].
//     Untracked files exist only in this tree, not iCommit's, so
//     `stash apply` (which diffs parent1..parent2 for the index and
//     parent2..commit for the worktree) restores them via the second
//     diff, same as it would for a real `stash push -u` entry.
//
// Note this is deliberately a two-parent commit, not real git's
// three-parent (b, i, u) --include-untracked format — there is no
// separate untracked-only commit unpacked straight onto disk. Building
// that would mean reproducing stash's internal object plumbing exactly;
// instead ApplyStash restores content via this simpler shape and then
// re-derives the tracked/untracked classification itself (see its doc
// comment) so the user-visible result still matches real stash.
//
// `git stash store` then anchors the resulting commit against gc
// (dangling commits are prunable after gc.pruneExpire) and makes it
// visible via `git stash list` for manual recovery, using the SHA —
// never stack position — that plumbing already handed back, so there
// is no race window reading back "top of stack" afterward. Returns ""
// (no error) if the worktree has nothing to stash, mirroring IsDirty's
// scope (tracked and untracked).
func (g *GitWorktree) StashChanges(message string) (string, error) {
	isDirty, err := g.IsDirty()
	if err != nil {
		return "", fmt.Errorf("failed to check for changes: %w", err)
	}
	if !isDirty {
		return "", nil
	}

	head, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to resolve HEAD for stash: %w", err)
	}
	head = strings.TrimSpace(head)

	env, cleanup, err := scratchIndexEnv()
	if err != nil {
		return "", fmt.Errorf("failed to create scratch index for stash: %w", err)
	}
	defer cleanup()

	if _, err := g.runGitCommandEnv(env, g.worktreePath, "read-tree", head); err != nil {
		return "", fmt.Errorf("failed to seed scratch index for stash: %w", err)
	}
	if _, err := g.runGitCommandEnv(env, g.worktreePath, "add", "-u"); err != nil {
		return "", fmt.Errorf("failed to stage tracked changes for stash: %w", err)
	}
	iTree, err := g.runGitCommandEnv(env, g.worktreePath, "write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write index tree for stash: %w", err)
	}
	iCommit, err := g.runGitCommandEnv(env, g.worktreePath, "commit-tree", strings.TrimSpace(iTree), "-p", head, "-m", "index on stash: "+message)
	if err != nil {
		return "", fmt.Errorf("failed to create index commit for stash: %w", err)
	}
	iCommit = strings.TrimSpace(iCommit)

	if _, err := g.runGitCommandEnv(env, g.worktreePath, "add", "-A"); err != nil {
		return "", fmt.Errorf("failed to stage untracked files for stash: %w", err)
	}
	wTree, err := g.runGitCommandEnv(env, g.worktreePath, "write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write working-tree tree for stash: %w", err)
	}
	sha, err := g.runGitCommandEnv(env, g.worktreePath, "commit-tree", strings.TrimSpace(wTree), "-p", head, "-p", iCommit, "-m", "On stash: "+message)
	if err != nil {
		return "", fmt.Errorf("failed to create stash commit: %w", err)
	}
	sha = strings.TrimSpace(sha)

	if _, err := g.runGitCommand(g.worktreePath, "stash", "store", "-m", message, sha); err != nil {
		return "", fmt.Errorf("failed to store stash: %w", err)
	}
	return sha, nil
}

// scratchIndexEnv reserves a unique path for a throwaway index and
// returns the GIT_INDEX_FILE overlay pointing at it, so plumbing can
// stage and write trees without touching the worktree's real index.
// cleanup removes whatever git left at the path.
func scratchIndexEnv() (env []string, cleanup func(), err error) {
	tmpIndex, err := os.CreateTemp("", "loom-stash-index-*")
	if err != nil {
		return nil, nil, err
	}
	path := tmpIndex.Name()
	tmpIndex.Close()
	os.Remove(path) // git creates it fresh; the path just needs to be reserved and unique.
	return []string{"GIT_INDEX_FILE=" + path}, func() { os.Remove(path) }, nil
}

// StashOnDisk reports whether the worktree already holds exactly what the
// stash commit sha would restore: its tracked and untracked (not ignored)
// content, snapshotted the way StashChanges builds a stash, matches the
// stash's tree. StashChanges leaves the worktree as it found it, so a
// pause interrupted after stashing — or a restore that applied the stash
// but never cleared its reference — leaves a worktree for which this is
// true, and applying the stash again would be redundant.
func (g *GitWorktree) StashOnDisk(sha string) (bool, error) {
	stashTree, err := g.runGitCommand(g.worktreePath, "rev-parse", "--verify", sha+"^{tree}")
	if err != nil {
		return false, fmt.Errorf("failed to resolve stash %.12s: %w", sha, err)
	}
	env, cleanup, err := scratchIndexEnv()
	if err != nil {
		return false, fmt.Errorf("failed to create scratch index: %w", err)
	}
	defer cleanup()
	if _, err := g.runGitCommandEnv(env, g.worktreePath, "read-tree", "HEAD"); err != nil {
		return false, fmt.Errorf("failed to seed scratch index: %w", err)
	}
	if _, err := g.runGitCommandEnv(env, g.worktreePath, "add", "-A"); err != nil {
		return false, fmt.Errorf("failed to snapshot worktree: %w", err)
	}
	diskTree, err := g.runGitCommandEnv(env, g.worktreePath, "write-tree")
	if err != nil {
		return false, fmt.Errorf("failed to write worktree snapshot: %w", err)
	}
	return strings.TrimSpace(diskTree) == strings.TrimSpace(stashTree), nil
}

// StashListed reports whether the stash commit sha is still an entry in
// `git stash list`, returning its current stash@{N} name when it is (a
// position that shifts as other sessions stash; see DropStash). One
// listing supplies every entry's commit, so no per-entry lookup can fail
// quietly: when the list cannot be read the answer is an error, never
// "not listed" — callers forget a stash that is not listed.
func (g *GitWorktree) StashListed(sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	out, err := g.runGitStdout(g.repoPath, "stash", "list", "--format=%gd %H")
	if err != nil {
		return "", fmt.Errorf("failed to list stash: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if ref, hash, ok := strings.Cut(strings.TrimSpace(line), " "); ok && hash == sha {
			return ref, nil
		}
	}
	return "", nil
}

// ApplyStash applies the stash commit sha onto the worktree, targeting
// the exact commit rather than stack position (stash@{0}) — refs/stash
// is shared across every worktree of this repo. A no-op for an empty
// sha.
//
// Because StashChanges' commit is two-parent (see its doc comment),
// `git stash apply` treats every previously-untracked file as a clean
// "add" against the index, so it comes back staged (`git status`
// would show it as `A`) rather than untracked (`??`) — unlike real
// `git stash apply -u`, which restores untracked content straight to
// disk without touching the index. To match the user-visible result
// of a real stash, ApplyStash finds every path that is newly added in
// the index relative to HEAD (`git diff --cached --diff-filter=A`)
// and unstages it (`git reset --`); the file's content on disk is
// untouched, only its index entry is removed, so it reads back as
// untracked. Best-effort: a failure here is logged, not returned,
// since the restore itself already succeeded and the file is still
// present (just staged) either way.
//
// On a clean apply, the matching stash-list entry is dropped
// (also best-effort, logged not returned, for the same reason). On
// conflict, the stash entry is left in place — mirroring `git stash
// pop`'s own safety behavior — and the error is returned so the
// caller does not clear its reference to it; neither the unstage nor
// the drop step runs in that case.
func (g *GitWorktree) ApplyStash(sha string) error {
	if sha == "" {
		return nil
	}
	if _, err := g.runGitCommand(g.worktreePath, "stash", "apply", sha); err != nil {
		return fmt.Errorf("failed to apply stash: %w", err)
	}
	if err := g.unstageNewlyAddedFiles(); err != nil {
		log.For("git").Warn("stash.unstage_new_files_failed", "err", err.Error())
	}
	if err := g.DropStash(sha); err != nil {
		log.For("git").Warn("stash.drop_after_apply_failed", "err", err.Error())
	}
	return nil
}

// unstageNewlyAddedFiles unstages every path present in the index but
// absent from HEAD's tree, turning files ApplyStash just staged as
// "added" back into untracked working-tree files (content on disk is
// untouched — only the index entry is removed). See ApplyStash's doc
// comment for why this is needed: its commit format can't distinguish
// "was untracked" from "was newly staged" any other way. A no-op if
// nothing qualifies.
func (g *GitWorktree) unstageNewlyAddedFiles() error {
	out, err := g.runGitCommand(g.worktreePath, "diff", "--cached", "--name-only", "--diff-filter=A", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to list newly staged files: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"reset", "--"}, paths...)
	if _, err := g.runGitCommand(g.worktreePath, args...); err != nil {
		return fmt.Errorf("failed to unstage newly added files: %w", err)
	}
	return nil
}

// DropStash removes the stash-list entry for commit sha, if present. A
// no-op if sha is empty or no longer listed (already dropped, or never
// stored). Runs against repoPath rather than worktreePath since refs/stash
// is a repo-level concept and the worktree directory may not exist at
// call time (e.g. after Kill has already removed it).
//
// `git stash drop` only takes a position, stash@{N}, and refs/stash is
// shared by every worktree of the repository: another session pushing or
// dropping between the lookup and the drop shifts the stack, and the drop
// then deletes that session's entry — its only copy of the work. So the
// drop is verified against the commit git reports it removed. A wrong
// entry is stored straight back (at the top of the stack; its content and
// message are kept, its position is not) and the lookup retried.
func (g *GitWorktree) DropStash(sha string) error {
	for attempt := 0; attempt < maxStashDropAttempts; attempt++ {
		ref, err := g.StashListed(sha)
		if err != nil || ref == "" {
			return err
		}
		out, err := g.runGitStdout(g.repoPath, "stash", "drop", ref)
		if err != nil {
			return fmt.Errorf("failed to drop stash %s: %w", ref, err)
		}
		dropped := droppedStashSHA(out)
		if dropped == sha {
			return nil
		}
		if dropped == "" {
			return fmt.Errorf("`git stash drop %s` did not say which stash it removed (%q); check `git -C %s stash list --format='%%gd %%H'` against stash %s", ref, strings.TrimSpace(out), g.repoPath, sha)
		}
		log.For("git").Warn("stash.drop_hit_other_entry", "want", sha, "dropped", dropped, "ref", ref)
		if err := g.restoreStashEntry(dropped); err != nil {
			return fmt.Errorf("the stack moved while dropping stash %s, so `git stash drop %s` removed another entry, %s, and storing it back failed — restore it with `git -C %s stash store %s`: %w", sha, ref, dropped, g.repoPath, dropped, err)
		}
	}
	return fmt.Errorf("the stash stack kept moving while dropping stash %s; left it in place", sha)
}

// maxStashDropAttempts bounds DropStash's retries when the shared stash
// stack keeps moving under it.
const maxStashDropAttempts = 5

// droppedStashRe matches `git stash drop`'s report, "Dropped <ref> (<sha>)".
// GitCommand forces git's messages to English, so the wording is stable.
var droppedStashRe = regexp.MustCompile(`Dropped .* \(([0-9a-f]{40,64})\)`)

// droppedStashSHA returns the commit `git stash drop` reported removing,
// or "" if the output does not say.
func droppedStashSHA(out string) string {
	if m := droppedStashRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// restoreStashEntry puts a stash commit back on the stash list, under the
// message its commit carries (for a stash, what `git stash list` shows).
func (g *GitWorktree) restoreStashEntry(sha string) error {
	msg, err := g.runGitStdout(g.repoPath, "log", "-1", "--format=%s", sha)
	if err != nil {
		return err
	}
	_, err = g.runGitCommand(g.repoPath, "stash", "store", "-m", strings.TrimSpace(msg), sha)
	return err
}

// Merge runs `git merge <sourceBranch>` in the worktree's directory,
// bringing another session's branch into this one. On conflict or any
// other non-zero exit, the git error/output is returned as-is and the
// merge is left exactly as git leaves it (MERGE_HEAD + conflict
// markers on a real conflict) — callers must not run `merge --abort`
// automatically, so a conflicted merge stays available for the user
// (or the agent) to resolve and commit.
func (g *GitWorktree) Merge(sourceBranch string) error {
	if _, err := g.runGitCommand(g.worktreePath, "merge", sourceBranch); err != nil {
		return fmt.Errorf("failed to merge %s: %w", sourceBranch, err)
	}
	return nil
}

// IsDirty checks if the worktree has uncommitted changes
func (g *GitWorktree) IsDirty() (bool, error) {
	output, err := g.runGitCommand(g.worktreePath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to check worktree status: %w", err)
	}
	return len(output) > 0, nil
}

// IsBranchCheckedOut checks if the instance branch is currently checked out
func (g *GitWorktree) IsBranchCheckedOut() (bool, error) {
	output, err := g.runGitCommand(g.repoPath, "branch", "--show-current")
	if err != nil {
		return false, fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)) == g.branchName, nil
}

// OpenBranchURL opens the branch URL in the default browser
func (g *GitWorktree) OpenBranchURL() error {
	// Check if GitHub CLI is available
	if err := g.checkGHCLI(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := internalexec.GhCommand(ctx, "browse", "--branch", g.branchName)
	c.Dir = g.worktreePath
	if err := g.runner.Run(c); err != nil {
		return fmt.Errorf("failed to open branch URL: %w", err)
	}
	return nil
}
