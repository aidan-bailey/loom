package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// ErrNoCommits reports a repository with no commits yet, where no base
// commit can exist. Its message is user-facing: it surfaces directly in
// the TUI when session creation fails.
var ErrNoCommits = errors.New("this appears to be a brand new repository: please create an initial commit before creating an instance")

// ResolveBaseCommit returns the commit SHA a new session worktree should be
// cut from, along with the human-readable name of the ref it came from (for
// the branch picker label and logs).
//
// configured is config.BaseBranch. When set, only that branch is considered —
// locally first, then as a remote-tracking ref, mirroring how
// setupFromExistingBranch resolves a branch that exists only on origin. A
// configured branch that resolves nowhere is an error rather than a silent
// fallback: quietly rooting a session somewhere other than where the user
// asked is worse than refusing.
//
// When configured is empty the repo's default branch is auto-detected:
// origin/HEAD, then main, then master, then whatever is checked out. That
// last rung is loom's historical behaviour, kept so repos with no remote and
// no conventionally-named default branch keep working.
//
// All lookups read local refs only — no fetch, so this stays fast and works
// offline. A stale origin/<branch> is the cost of that.
func ResolveBaseCommit(repoPath, configured string, runner CommandRunner) (sha, name string, err error) {
	r := defaultRunner(runner)

	if configured != "" {
		if sha, ok := resolveRef(repoPath, "refs/heads/"+configured, r); ok {
			return sha, configured, nil
		}
		if sha, ok := resolveRef(repoPath, "refs/remotes/origin/"+configured, r); ok {
			return sha, "origin/" + configured, nil
		}
		return "", "", fmt.Errorf("configured base branch %q not found locally or on origin", configured)
	}

	for _, candidate := range autoBaseCandidates(repoPath, r) {
		if sha, ok := resolveRef(repoPath, "refs/heads/"+candidate, r); ok {
			return sha, candidate, nil
		}
		if sha, ok := resolveRef(repoPath, "refs/remotes/origin/"+candidate, r); ok {
			return sha, "origin/" + candidate, nil
		}
	}

	// Last resort: today's behaviour — whatever the root repo has checked out.
	sha, ok := resolveRef(repoPath, "HEAD", r)
	if !ok {
		return "", "", ErrNoCommits
	}
	return sha, "HEAD", nil
}

// autoBaseCandidates lists default-branch names to try, most authoritative
// first. origin/HEAD is what the remote itself declares its default to be;
// main and master are the conventional fallbacks for repos with no remote.
func autoBaseCandidates(repoPath string, runner CommandRunner) []string {
	candidates := make([]string, 0, 3)
	if detected := originHeadBranch(repoPath, runner); detected != "" {
		candidates = append(candidates, detected)
	}
	for _, conventional := range []string{"main", "master"} {
		if !contains(candidates, conventional) {
			candidates = append(candidates, conventional)
		}
	}
	return candidates
}

// originHeadBranch reads refs/remotes/origin/HEAD and strips the remote
// prefix, yielding e.g. "main". Returns "" when the ref is absent — common in
// repos with no remote, and in clones where origin/HEAD was never set.
func originHeadBranch(repoPath string, runner CommandRunner) string {
	out, err := runGitOutput(repoPath, runner, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
}

// resolveRef returns the commit SHA ref points at. ok is false when the ref
// does not exist, which every caller here treats as "try the next candidate"
// rather than as a failure.
func resolveRef(repoPath, ref string, runner CommandRunner) (sha string, ok bool) {
	out, err := runGitOutput(repoPath, runner, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", false
	}
	sha = strings.TrimSpace(out)
	return sha, sha != ""
}

// runGitOutput runs a read-only git command. It mirrors
// GitWorktree.runGitCommand but is a free function, since base resolution
// happens before any GitWorktree exists.
func runGitOutput(repoPath string, runner CommandRunner, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, repoPath, args...)
	out, err := runner.Output(c)
	if err != nil {
		// Debug, not Warn: absent refs are the expected case on most rungs
		// of the fallback ladder.
		log.For("git").Debug("git.base_lookup_failed", "cmd", strings.Join(args, " "), "path", repoPath, "err", err.Error())
		return "", err
	}
	return string(out), nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
