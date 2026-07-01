package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMergeFixture builds a repo with two linked worktrees: target
// (branch "target-branch", checked out at the init commit) and source
// (branch "source-branch", one commit ahead that edits README.md).
// Each test then adds its own additional target-side commit (or none,
// for the fast-forward case) before calling target.Merge(sourceBranch)
// to exercise fast-forward / merge-commit / conflict outcomes.
func setupMergeFixture(t *testing.T) (target *GitWorktree, sourceBranch string) {
	t.Helper()
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi\n"), 0644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	targetPath := filepath.Join(tmpDir, "target")
	runGit(t, repoDir, "worktree", "add", "-b", "target-branch", targetPath)

	sourcePath := filepath.Join(tmpDir, "source")
	sourceBranch = "source-branch"
	runGit(t, repoDir, "worktree", "add", "-b", sourceBranch, sourcePath)
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "README.md"), []byte("hi\nfrom source\n"), 0644))
	runGit(t, sourcePath, "add", ".")
	runGit(t, sourcePath, "commit", "-m", "source edits README")

	target = NewGitWorktreeFromStorage(repoDir, targetPath, "target", "target-branch", "", true, tmpDir)
	return target, sourceBranch
}

// hasMergeHead reports whether worktreePath is mid-merge, robust to the
// linked-worktree ".git is a file pointing elsewhere" layout.
func hasMergeHead(worktreePath string) bool {
	return exec.Command("git", "-C", worktreePath, "rev-parse", "-q", "--verify", "MERGE_HEAD").Run() == nil
}

func TestMerge_FastForward(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	err := target.Merge(sourceBranch)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(target.worktreePath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "hi\nfrom source\n", string(content), "fast-forward merge should bring in source's README edit")
	assert.False(t, hasMergeHead(target.worktreePath))
}

func TestMerge_CreatesMergeCommit(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	// Diverge target with a commit on a different file so the merge
	// can't fast-forward but also can't conflict.
	require.NoError(t, os.WriteFile(filepath.Join(target.worktreePath, "target-only.txt"), []byte("t"), 0644))
	runGit(t, target.worktreePath, "add", ".")
	runGit(t, target.worktreePath, "commit", "-m", "target-only change")

	err := target.Merge(sourceBranch)
	require.NoError(t, err)

	out, err := exec.Command("git", "-C", target.worktreePath, "log", "-1", "--pretty=%P").Output()
	require.NoError(t, err)
	parents := strings.Fields(strings.TrimSpace(string(out)))
	assert.Len(t, parents, 2, "expected a merge commit with two parents")
}

func TestMerge_ConflictLeavesMergeHeadAndReturnsError(t *testing.T) {
	target, sourceBranch := setupMergeFixture(t)

	// Diverge target by editing the SAME line of README.md that the
	// source-branch commit also touches, guaranteeing a real conflict.
	require.NoError(t, os.WriteFile(filepath.Join(target.worktreePath, "README.md"), []byte("hi\nfrom target\n"), 0644))
	runGit(t, target.worktreePath, "add", ".")
	runGit(t, target.worktreePath, "commit", "-m", "target edits README")

	err := target.Merge(sourceBranch)

	require.Error(t, err)
	assert.True(t, hasMergeHead(target.worktreePath), "conflicted merge must leave MERGE_HEAD in place — Merge must not auto-abort")
}
