package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// watchFetchTimeout bounds the watcher's `git fetch` of one branch: the
// 60 s the watcher allots a fetch and its checkout refresh together.
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
