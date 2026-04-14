package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// DiffStats holds statistics about the changes in a diff
type DiffStats struct {
	// Content is the full diff content
	Content string
	// Added is the number of added lines
	Added int
	// Removed is the number of removed lines
	Removed int
	// Error holds any error that occurred during diff computation
	// This allows propagating setup errors (like missing base commit) without breaking the flow
	Error error
}

func (d *DiffStats) IsEmpty() bool {
	return d.Added == 0 && d.Removed == 0 && d.Content == ""
}

// CurrentBranch returns the current branch name for the given repo directory.
func CurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %s (%w)", output, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// DiffUncommitted returns the diff of uncommitted changes in the given repo directory.
// Used for workspace terminals that operate on the root repo without a worktree.
// Only shows tracked file changes to avoid mutating the user's git index.
func DiffUncommitted(repoPath string) *DiffStats {
	stats := &DiffStats{}

	cmd := exec.Command("git", "-C", repoPath, "--no-pager", "diff", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		stats.Error = fmt.Errorf("git diff failed: %s (%w)", output, err)
		return stats
	}

	content := string(output)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}

// parseShortStat parses the output of git diff --shortstat.
// Example: " 3 files changed, 10 insertions(+), 5 deletions(-)\n"
func parseShortStat(output string) (added, removed int) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, 0
	}
	parts := strings.Split(output, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		fields := strings.Fields(part)
		if len(fields) >= 2 {
			if strings.Contains(part, "insertion") {
				fmt.Sscanf(fields[0], "%d", &added)
			} else if strings.Contains(part, "deletion") {
				fmt.Sscanf(fields[0], "%d", &removed)
			}
		}
	}
	return added, removed
}

// DiffShortStat returns only the line counts (Added/Removed) without full diff content.
// Uses git diff --shortstat which is significantly cheaper for large diffs.
func (g *GitWorktree) DiffShortStat() *DiffStats {
	stats := &DiffStats{}

	if g.GetBaseCommitSHA() == "" {
		stats.Error = fmt.Errorf("base commit SHA not set")
		return stats
	}

	// Guard git add -N with untracked file check (same as Diff)
	untrackedOutput, err := g.runGitCommand(g.worktreePath, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		stats.Error = err
		return stats
	}
	if strings.TrimSpace(untrackedOutput) != "" {
		_, err = g.runGitCommand(g.worktreePath, "add", "-N", ".")
		if err != nil {
			stats.Error = err
			return stats
		}
	}

	content, err := g.runGitCommand(g.worktreePath, "--no-pager", "diff", "--shortstat", g.GetBaseCommitSHA())
	if err != nil {
		stats.Error = err
		return stats
	}
	stats.Added, stats.Removed = parseShortStat(content)
	return stats
}

// DiffUncommittedShortStat returns only line counts for uncommitted changes.
func DiffUncommittedShortStat(repoPath string) *DiffStats {
	stats := &DiffStats{}
	cmd := exec.Command("git", "-C", repoPath, "--no-pager", "diff", "--shortstat", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		stats.Error = fmt.Errorf("git diff --shortstat failed: %s (%w)", output, err)
		return stats
	}
	stats.Added, stats.Removed = parseShortStat(string(output))
	return stats
}

// Diff returns the git diff between the worktree and the base branch along with statistics
func (g *GitWorktree) Diff() *DiffStats {
	stats := &DiffStats{}

	if g.GetBaseCommitSHA() == "" {
		stats.Error = fmt.Errorf("base commit SHA not set")
		return stats
	}

	// Only run `git add -N .` when there are untracked files, to avoid
	// unnecessary index writes that could interfere with the agent.
	untrackedOutput, err := g.runGitCommand(g.worktreePath, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		stats.Error = err
		return stats
	}
	if strings.TrimSpace(untrackedOutput) != "" {
		_, err = g.runGitCommand(g.worktreePath, "add", "-N", ".")
		if err != nil {
			stats.Error = err
			return stats
		}
	}

	content, err := g.runGitCommand(g.worktreePath, "--no-pager", "diff", g.GetBaseCommitSHA())
	if err != nil {
		stats.Error = err
		return stats
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}
