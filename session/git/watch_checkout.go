package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// watchFetchTimeout bounds the watcher's `git fetch` of one branch alone;
// the checkout refresh that follows runs under gitWorktreeAddTimeout (5 min).
const watchFetchTimeout = 60 * time.Second

// ErrNoSuchPath is ShowFile's answer for a revision that has no file at the
// path (absent, or a directory). Any other failure is a different error: a
// caller reads ErrNoSuchPath as "the repository has no such file", and a
// read git could not make must never look like that.
var ErrNoSuchPath = errors.New("no such file at that revision")

// runGitCombined runs one git command in dir under timeout, returning its
// combined output in the error when it fails.
func runGitCombined(dir string, r CommandRunner, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, dir, args...)
	if out, err := r.CombinedOutput(c); err != nil {
		return fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

// FetchBranch fetches branch from origin into refs/remotes/origin/<branch>
// and returns the commit SHA that ref now holds. The destination is named
// in the refspec, so the ref moves whatever the clone's configured fetch
// refspecs are (a single-branch clone's would leave it stale). The local
// branch of the same name is not touched. Network-bound:
// watchFetchTimeout applies. A name git does not accept as a branch is
// refused before any fetch.
func FetchBranch(repoPath string, branch string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	if branch == "" {
		return "", fmt.Errorf("fetch origin: not a valid branch name %q", branch)
	}
	if _, err := runGitOutput(repoPath, r, "check-ref-format", "refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("fetch origin: not a valid branch name %q", branch)
	}
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	if err := runGitCombined(repoPath, r, watchFetchTimeout, "fetch", "--quiet", "origin", refspec); err != nil {
		return "", fmt.Errorf("fetch origin %s: %w", branch, err)
	}
	sha, ok := resolveRef(repoPath, "refs/remotes/origin/"+branch, r)
	if !ok {
		return "", fmt.Errorf("fetch origin %s: refs/remotes/origin/%s is not a commit after the fetch", branch, branch)
	}
	return sha, nil
}

// BranchExists reports where branch already exists: "locally" when
// refs/heads/<branch> is a ref, "on origin" when origin has the branch (asked
// with ls-remote, so a branch the clone never fetched counts), or "" when it
// is in neither. A failed check is an error, never an answer of "": a caller
// about to create the branch must not take an unanswered question for
// absence. Network-bound: watchFetchTimeout applies to the ls-remote.
func BranchExists(repoPath string, branch string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	if branch == "" {
		return "", fmt.Errorf("branch check: not a valid branch name %q", branch)
	}
	if _, err := runGitOutput(repoPath, r, "check-ref-format", "refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("branch check: not a valid branch name %q", branch)
	}
	// for-each-ref answers an absent ref with empty output and success, so a
	// failure here is a real one.
	local, err := runGitOutput(repoPath, r, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("branch check %s: %w", branch, err)
	}
	if strings.TrimSpace(local) == "refs/heads/"+branch {
		return "locally", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), watchFetchTimeout)
	defer cancel()
	out, err := r.Output(internalexec.GitCommand(ctx, repoPath, "ls-remote", "--heads", "origin", "refs/heads/"+branch))
	if err != nil {
		return "", fmt.Errorf("branch check %s on origin: %w", branch, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if _, ref, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok && ref == "refs/heads/"+branch {
			return "on origin", nil
		}
	}
	return "", nil
}

// ShowFile returns the bytes of path (relative to the repository root, with
// forward slashes) as of rev. A revision that has no such file, or has a
// directory there, is ErrNoSuchPath; every other failure (an unknown rev, a
// timeout, a git error) is another error. rev may not start with "-".
// Local only: no fetch, gitTimeout applies to each of its two reads.
func ShowFile(repoPath string, rev string, path string, runner CommandRunner) ([]byte, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("show %q: not a revision", rev)
	}
	r := defaultRunner(runner)
	// Probe the tree first: `git show` cannot tell "no such path" from other
	// failures without reading git's English stderr, and an empty probe
	// answer, unlike a failure, means exactly that the path is absent.
	out, err := runGitOutput(repoPath, r, "ls-tree", "--full-tree", "-z", rev, "--", path)
	if err != nil {
		return nil, fmt.Errorf("show %s:%s: %w", rev, path, err)
	}
	isFile := false
	for _, entry := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		if !ok || name != path {
			continue
		}
		if f := strings.Fields(meta); len(f) >= 2 && f[1] == "blob" {
			isFile = true
		}
	}
	if !isFile {
		return nil, fmt.Errorf("show %s:%s: %w", rev, path, ErrNoSuchPath)
	}
	body, err := runGitOutput(repoPath, r, "show", rev+":"+path)
	if err != nil {
		return nil, fmt.Errorf("show %s:%s: %w", rev, path, err)
	}
	return []byte(body), nil
}

// SyncDetachedWorktree keeps a detached worktree at path on commit sha: it
// adds one when path is absent (also over a registration whose directory is
// gone), and otherwise checks sha out detached in it, discarding edits to
// tracked files, which are the hook's scratch and never the user's work.
// sha must be a full SHA of a commit already in the repository (FetchBranch's
// answer); a branch name is refused.
//
// It acts on an existing path only when git lists it as a detached linked
// worktree of repoPath (the repository's own checkout, whatever its HEAD,
// and a worktree on a branch, a session's, are refused) and git resolves its
// toplevel to the path itself: the checkout sits inside the repository, so
// git run in a gutted one would walk up to the user's own checkout. Any
// other directory there is left as it is, with an error. Nothing is deleted.
func SyncDetachedWorktree(repoPath string, path string, sha string, runner CommandRunner) error {
	if !isFullSHA(sha) {
		return fmt.Errorf("watcher checkout: %q is not a full commit SHA", sha)
	}
	r := defaultRunner(runner)
	if _, ok := resolveRef(repoPath, sha, r); !ok {
		return fmt.Errorf("watcher checkout: %s is not a commit in %s", sha, repoPath)
	}

	_, statErr := os.Lstat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		// --force: a registration whose directory is gone is overridden.
		if err := runGitCombined(repoPath, r, gitWorktreeAddTimeout, "worktree", "add", "--force", "--detach", path, sha); err != nil {
			return fmt.Errorf("watcher checkout %s: %w", path, err)
		}
		return nil
	case statErr != nil:
		return fmt.Errorf("watcher checkout %s: %w", path, statErr)
	}

	owned, err := isDetachedLinkedWorktree(repoPath, path, r)
	if err != nil {
		return fmt.Errorf("watcher checkout %s: %w", path, err)
	}
	if !owned {
		return fmt.Errorf("watcher checkout: %s exists and is not a detached worktree of %s; left as it is", path, repoPath)
	}
	top, err := runGitOutput(path, r, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("watcher checkout %s: %w", path, err)
	}
	if resolvedPath(strings.TrimSpace(top)) != resolvedPath(path) {
		return fmt.Errorf("watcher checkout: git resolves %s to %s, so it is no working tree of its own; left as it is", path, strings.TrimSpace(top))
	}
	if err := runGitCombined(path, r, gitWorktreeAddTimeout, "checkout", "--quiet", "--force", "--detach", sha); err != nil {
		return fmt.Errorf("watcher checkout %s: %w", path, err)
	}
	return nil
}

// isDetachedLinkedWorktree reports whether `git worktree list --porcelain`
// of repoPath lists path (compared by resolved path) as a linked worktree
// with a detached HEAD. The first block is the repository's own checkout and
// never counts.
func isDetachedLinkedWorktree(repoPath, path string, r CommandRunner) (bool, error) {
	out, err := runGitOutput(repoPath, r, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	want := resolvedPath(path)
	for n, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		if n == 0 {
			continue
		}
		lines := strings.Split(block, "\n")
		if !strings.HasPrefix(lines[0], "worktree ") || resolvedPath(strings.TrimPrefix(lines[0], "worktree ")) != want {
			continue
		}
		for _, l := range lines[1:] {
			if l == "detached" {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}
