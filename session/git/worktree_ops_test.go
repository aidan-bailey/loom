package git

import (
	"claude-squad/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize("", false)
	defer log.Close()
	os.Exit(m.Run())
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
}

// setupTestRepoWithWorktree creates a real git repo and a linked worktree,
// returning the config dir (where worktrees/ lives), the repo dir, the
// worktree path, and the branch name.
func setupTestRepoWithWorktree(t *testing.T) (configDir, repoDir, worktreePath, branchName string) {
	t.Helper()
	tmpDir := t.TempDir()
	configDir = filepath.Join(tmpDir, "config")
	repoDir = filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	branchName = "cleanup-test-branch"
	worktreePath = filepath.Join(configDir, "worktrees", branchName+"_fixture")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreePath), 0755))
	runGit(t, repoDir, "worktree", "add", "-b", branchName, worktreePath)

	return configDir, repoDir, worktreePath, branchName
}

func branchExists(t *testing.T, repoDir, branchName string) bool {
	t.Helper()
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", branchName).Output()
	return strings.TrimSpace(string(out)) != ""
}

// TestCleanupWorktrees_DeletesBranch is the integration test for the fix:
// CleanupWorktrees must remove the worktree directory AND delete the branch.
// The pre-fix code tried `git branch -D` before removing the worktree
// registration, so deletion failed (worktree was still checked out) and the
// error was only logged — the branch leaked.
func TestCleanupWorktrees_DeletesBranch(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)

	err := CleanupWorktrees(configDir, nil)
	assert.NoError(t, err)

	_, statErr := os.Stat(worktreePath)
	assert.True(t, os.IsNotExist(statErr), "worktree dir should be removed")

	assert.False(t, branchExists(t, repoDir, branchName),
		"branch %q should be deleted after cleanup", branchName)
}

// TestSetup_BranchDeletedExternallyReturnsErrBranchGone is the F10
// regression guard. When a paused instance's branch is deleted via
// `git branch -D` from outside the app AND no origin tracking branch
// exists, Setup must return a typed sentinel so Resume can surface a
// recovery hint instead of a generic "failed to setup git worktree".
// Before this fix the error was a plain `fmt.Errorf` string, not an
// errors.Is-compatible signal, so callers could only string-match.
func TestSetup_BranchDeletedExternallyReturnsErrBranchGone(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)

	// Simulate the post-pause pathological state: worktree removed
	// AND the branch deleted out-of-band (no origin remote).
	runGit(t, repoDir, "worktree", "remove", "-f", worktreePath)
	runGit(t, repoDir, "branch", "-D", branchName)
	require.False(t, branchExists(t, repoDir, branchName), "precondition: branch must be gone")

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "lost-session", branchName, "", true, configDir)
	err := gw.Setup()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBranchGone, "Setup must return ErrBranchGone sentinel when branch vanished")
}

// TestCleanupWorktrees_EmptyDirectoryNoError confirms cleanup is a safe no-op
// when the worktrees directory is empty.
func TestCleanupWorktrees_EmptyDirectoryNoError(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "worktrees"), 0755))

	err := CleanupWorktrees(configDir, nil)
	assert.NoError(t, err)
}
