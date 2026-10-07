package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runGit runs a git command in dir with a deterministic identity. Named
// runGit (not git) to avoid colliding with the session/git package import
// used by sibling test files in this package.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestRemoveOrphanWorktree_RemovesDirKeepsBranch(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	runGit(t, repo, "branch", "feature")

	wt := filepath.Join(t.TempDir(), "feature_wt")
	runGit(t, repo, "worktree", "add", wt, "feature")
	require.DirExists(t, wt)

	err := RemoveOrphanWorktree(repo, wt)
	assert.NoError(t, err)
	assert.NoDirExists(t, wt)

	// Branch must survive.
	cmd := exec.Command("git", "-C", repo, "branch", "--list", "feature")
	out, _ := cmd.Output()
	assert.Contains(t, string(out), "feature")
}

func TestRemoveOrphanWorktree_RemovesTitleSidecar(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	runGit(t, repo, "branch", "feature")

	wt := filepath.Join(t.TempDir(), "feature_wt")
	runGit(t, repo, "worktree", "add", wt, "feature")
	sidecar := git.WorktreeTitleSidecarPath(wt)
	require.NoError(t, os.WriteFile(sidecar, []byte("my feature"), 0o644))

	err := RemoveOrphanWorktree(repo, wt)
	assert.NoError(t, err)
	assert.NoFileExists(t, sidecar, "orphan auto-clean must not leave a dangling .loom-title sidecar")
}

// orphanWorktree makes a repo with branch "feature" checked out in a
// linked worktree, an orphan the auto-clean would remove.
func orphanWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	repo = t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	runGit(t, repo, "branch", "feature")
	wt = filepath.Join(t.TempDir(), "feature_wt")
	runGit(t, repo, "worktree", "add", wt, "feature")
	return repo, wt
}

// worktreeLockFile is the lock file of the linked worktree wt.
func worktreeLockFile(t *testing.T, wt string) string {
	t.Helper()
	gitfile, err := os.ReadFile(filepath.Join(wt, ".git"))
	require.NoError(t, err)
	return filepath.Join(strings.TrimSpace(strings.TrimPrefix(string(gitfile), "gitdir:")), "locked")
}

// TestRemoveOrphanWorktree_UnlocksAStaleInitializingLock: the kermit
// lubm-benchmark tree, which failed the sweep at every start.
func TestRemoveOrphanWorktree_UnlocksAStaleInitializingLock(t *testing.T) {
	repo, wt := orphanWorktree(t)
	lock := worktreeLockFile(t, wt)
	require.NoError(t, os.WriteFile(lock, []byte("initializing"), 0o644))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(lock, old, old))

	require.NoError(t, RemoveOrphanWorktree(repo, wt))
	assert.NoDirExists(t, wt)
}

func TestRemoveOrphanWorktree_KeepsAUserLockedTree(t *testing.T) {
	repo, wt := orphanWorktree(t)
	runGit(t, repo, "worktree", "lock", "--reason", "keep me", wt)

	err := RemoveOrphanWorktree(repo, wt)

	require.ErrorIs(t, err, git.ErrWorktreeLocked)
	assert.Contains(t, err.Error(), "keep me")
	assert.DirExists(t, wt)
}
