package git

import (
	"claude-squad/log"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() error {
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
	if g.isExistingBranch {
		return g.setupFromExistingBranch()
	}

	// Check if branch exists using git CLI (much faster than go-git PlainOpen)
	_, err = g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if err == nil {
		return g.setupFromExistingBranch()
	}
	return g.setupNewWorktree()
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("branch %s not found locally or on remote", g.branchName)
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
	if g.baseCommitSHA == "" {
		output, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("failed to get base commit for existing branch %s: %w", g.branchName, err)
		}
		g.baseCommitSHA = strings.TrimSpace(string(output))
	}

	return nil
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen)
	_, _ = g.runGitCommand(g.repoPath, "branch", "-D", g.branchName) // Ignore error if branch doesn't exist

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
	g.baseCommitSHA = headCommit

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
func (g *GitWorktree) Cleanup() error {
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

		repoRoot, err := findGitRepoRoot(worktreePath, r)
		if err != nil {
			// Can't determine repo (e.g. .git file missing) — just remove the directory
			os.RemoveAll(worktreePath)
			continue
		}
		repoWorktrees[repoRoot] = append(repoWorktrees[repoRoot], worktreePath)
	}

	// For each repo, resolve branches via worktree list, then clean up
	for repoRoot, worktreePaths := range repoWorktrees {
		// Get worktree→branch mappings for this repo
		listCtx, listCancel := context.WithTimeout(context.Background(), gitTimeout)
		listCmd := exec.CommandContext(listCtx, "git", "-C", repoRoot, "worktree", "list", "--porcelain")
		output, _ := r.Output(listCmd)
		listCancel()

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
			// Delete the branch associated with this worktree if found
			if branch, ok := worktreeBranches[wtPath]; ok {
				delCtx, delCancel := context.WithTimeout(context.Background(), gitTimeout)
				deleteCmd := exec.CommandContext(delCtx, "git", "-C", repoRoot, "branch", "-D", branch)
				if err := r.Run(deleteCmd); err != nil {
					log.ErrorLog.Printf("failed to delete branch %s: %v", branch, err)
				}
				delCancel()
			}

			// Remove the worktree directory
			os.RemoveAll(wtPath)
		}

		// Prune stale worktree references for this repo
		pruneCtx, pruneCancel := context.WithTimeout(context.Background(), gitTimeout)
		pruneCmd := exec.CommandContext(pruneCtx, "git", "-C", repoRoot, "worktree", "prune")
		if err := r.Run(pruneCmd); err != nil {
			log.ErrorLog.Printf("failed to prune worktrees for %s: %v", repoRoot, err)
		}
		pruneCancel()
	}

	return nil
}
