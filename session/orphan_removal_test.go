package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
