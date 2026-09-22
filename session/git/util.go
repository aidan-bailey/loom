package git

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/github"
)

// sanitizeBranchName transforms an arbitrary string into a Git branch name friendly string.
// Note: Git branch names have several rules, so this function uses a simple approach
// by allowing only a safe subset of characters.
func sanitizeBranchName(s string) string {
	// Convert to lower-case
	s = strings.ToLower(s)

	// Replace spaces with a dash
	s = strings.ReplaceAll(s, " ", "-")

	// Remove any characters not allowed in our safe subset.
	// Here we allow: letters, digits, dash, underscore, slash, and dot.
	re := regexp.MustCompile(`[^a-z0-9\-_/.]+`)
	s = re.ReplaceAllString(s, "")

	// Replace multiple dashes with a single dash (optional cleanup)
	reDash := regexp.MustCompile(`-+`)
	s = reDash.ReplaceAllString(s, "-")

	// Collapse runs of dots to a single dot. Git itself rejects refs
	// containing `..`, and leaving the pattern intact would let a title like
	// "../escape" traverse out of the worktrees directory when combined with
	// filepath.Join.
	reDot := regexp.MustCompile(`\.+`)
	s = reDot.ReplaceAllString(s, ".")

	// Trim leading and trailing dashes, slashes, or dots to avoid issues
	// (leading dot = hidden-file style, trailing dot = invalid on Windows).
	s = strings.Trim(s, "-/.")

	return s
}

// checkGHCLI checks if GitHub CLI is installed and configured.
func (g *GitWorktree) checkGHCLI() error {
	return github.CheckCLI(g.runner)
}

// IsGitRepo checks if the given path is within a git repository.
// Pass nil for runner to use the default subprocess runner.
func IsGitRepo(path string, runner CommandRunner) bool {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, path, "rev-parse", "--show-toplevel")
	return r.Run(c) == nil
}

func findGitRepoRoot(path string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := internalexec.GitCommand(ctx, path, "rev-parse", "--show-toplevel")
	out, err := r.Output(c)
	if err != nil {
		return "", fmt.Errorf("failed to find Git repository root from path: %s", path)
	}
	return strings.TrimSpace(string(out)), nil
}

// findMainRepoRoot returns the main repository's working tree for any path
// inside a git checkout — including a linked worktree. Unlike findGitRepoRoot
// (which returns the local worktree's own top level), this survives after the
// caller removes the linked worktree directory, because the main repo's path
// is elsewhere on disk. Pass nil for runner to use the default subprocess
// runner.
func findMainRepoRoot(path string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := internalexec.GitCommand(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := r.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to find main Git repository root from path: %s", path)
	}
	commonDir := strings.TrimSpace(string(out))
	return filepath.Dir(commonDir), nil
}
