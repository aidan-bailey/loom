package git

import (
	"claude-squad/log"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// MaxBranchSearchResults is the maximum number of branches returned by SearchBranches.
const MaxBranchSearchResults = 50

// gitTimeout bounds any single git subprocess. The metadata tick fans these out
// on every tick, so a hung git process would freeze the UI without this cap.
const gitTimeout = 8 * time.Second

// gitNetworkTimeout applies to commands that talk to a remote (push/sync/fetch).
const gitNetworkTimeout = 30 * time.Second

// FetchBranches fetches and prunes remote-tracking branches (best-effort, won't fail if offline).
// Pass nil for runner to use the default subprocess runner.
func FetchBranches(repoPath string, runner CommandRunner) {
	r := defaultRunner(runner)
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--prune")
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
	c := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "-a",
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
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	baseArgs := []string{"-C", path}
	c := exec.CommandContext(ctx, "git", append(baseArgs, args...)...)

	output, err := g.runner.CombinedOutput(c)
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git command timed out after %s: git %s", gitTimeout, strings.Join(args, " "))
	}
	if err != nil {
		return "", fmt.Errorf("git command failed: %s (%w)", output, err)
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
	pushCmd := exec.CommandContext(ctx, "gh", "repo", "sync", "--source", "-b", g.branchName)
	pushCmd.Dir = g.worktreePath
	if err := g.runner.Run(pushCmd); err != nil {
		// Fallback needs its own deadline: a slow `gh repo sync` above can
		// burn through most or all of ctx's budget before failing, which would
		// cancel the push before it even dials.
		fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
		defer fallbackCancel()
		gitPushCmd := exec.CommandContext(fallbackCtx, "git", "push", "-u", "origin", g.branchName)
		gitPushCmd.Dir = g.worktreePath
		if pushOutput, pushErr := g.runner.CombinedOutput(gitPushCmd); pushErr != nil {
			return fmt.Errorf("failed to push branch: %s (%w)", pushOutput, pushErr)
		}
	}

	// Now sync with remote
	syncCtx, syncCancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer syncCancel()
	syncCmd := exec.CommandContext(syncCtx, "gh", "repo", "sync", "-b", g.branchName)
	syncCmd.Dir = g.worktreePath
	if output, err := g.runner.CombinedOutput(syncCmd); err != nil {
		return fmt.Errorf("failed to sync changes: %s (%w)", output, err)
	}

	// Open the branch in the browser
	if open {
		if err := g.OpenBranchURL(); err != nil {
			// Just log the error but don't fail the push operation
			log.ErrorLog.Printf("failed to open branch URL: %v", err)
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
	c := exec.CommandContext(ctx, "gh", "browse", "--branch", g.branchName)
	c.Dir = g.worktreePath
	if err := g.runner.Run(c); err != nil {
		return fmt.Errorf("failed to open branch URL: %w", err)
	}
	return nil
}
