package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// AheadBehind returns how many commits branch has that base lacks
// (ahead) and vice versa (behind), reading local refs only — one
// subprocess, no network, so it is safe on the metadata tick.
func AheadBehind(repoPath, branch, base string, runner CommandRunner) (ahead, behind int, err error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-list", "--left-right", "--count", branch+"..."+base)
	out, err := r.Output(c)
	if err != nil {
		return 0, 0, fmt.Errorf("rev-list %s...%s: %w", branch, base, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("rev-list %s...%s: unexpected output %q", branch, base, strings.TrimSpace(string(out)))
	}
	if ahead, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("rev-list ahead count: %w", err)
	}
	if behind, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("rev-list behind count: %w", err)
	}
	return ahead, behind, nil
}

// FetchRef updates the remote-tracking ref named like "origin/main".
// The ref must genuinely exist under refs/remotes: a local branch whose
// name merely contains a slash ("release/2.0") is not a remote ref and
// returns nil without running anything, as does a ref with no slash at
// all. Network-bound: gitNetworkTimeout applies.
func FetchRef(repoPath, ref string, runner CommandRunner) error {
	remote, branch, ok := strings.Cut(ref, "/")
	if !ok || remote == "" || branch == "" {
		return nil
	}
	r := defaultRunner(runner)
	if !isRemoteTrackingRef(repoPath, ref, r) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet", remote, branch)
	if out, err := r.CombinedOutput(c); err != nil {
		return fmt.Errorf("fetch %s %s: %s (%w)", remote, branch, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// isRemoteTrackingRef reports whether refs/remotes/<ref> exists, which
// is what tells "origin/main" apart from a local branch that merely
// contains a slash. One local rev-parse, no network, so it is far
// cheaper than the failed fetch it prevents.
func isRemoteTrackingRef(repoPath, ref string, r CommandRunner) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "--quiet", "refs/remotes/"+ref)
	_, err := r.Output(c)
	return err == nil
}
