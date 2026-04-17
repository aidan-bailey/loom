package git

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
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

	// Trim leading and trailing dashes or slashes to avoid issues
	s = strings.Trim(s, "-/")

	return s
}

// checkGHCLI checks if GitHub CLI is installed and configured.
func (g *GitWorktree) checkGHCLI() error {
	// Check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed. Please install it first")
	}

	// Check if gh is authenticated
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", "auth", "status")
	if err := g.runner.Run(c); err != nil {
		return fmt.Errorf("GitHub CLI is not configured. Please run 'gh auth login' first")
	}

	return nil
}

// IsGitRepo checks if the given path is within a git repository.
// Pass nil for runner to use the default subprocess runner.
func IsGitRepo(path string, runner CommandRunner) bool {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	return r.Run(c) == nil
}

func findGitRepoRoot(path string, runner CommandRunner) (string, error) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := r.Output(c)
	if err != nil {
		return "", fmt.Errorf("failed to find Git repository root from path: %s", path)
	}
	return strings.TrimSpace(string(out)), nil
}
