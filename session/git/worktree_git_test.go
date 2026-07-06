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

// newStashTestRepo creates a real git repo with one commit and a
// GitWorktree pointed at its root (repoPath == worktreePath — no
// linked worktree needed to exercise stash create/store/apply/drop,
// which all operate on a plain working tree).
func newStashTestRepo(t *testing.T) *GitWorktree {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v failed: %s", args, string(out))
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0644))
	run("git", "add", ".")
	run("git", "commit", "-m", "init")

	return NewGitWorktreeFromStorage(dir, dir, "stash-test", "main", "", true, dir)
}

func TestStashChanges_TrackedAndUntracked(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0644))

	sha, err := gw.StashChanges("test pause stash")
	require.NoError(t, err)
	assert.NotEmpty(t, sha)

	// StashChanges must not touch the working tree — `git stash
	// create` only builds the commit object.
	tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(tracked))
	_, err = os.Stat(filepath.Join(dir, "untracked.txt"))
	assert.NoError(t, err, "untracked file must still be present after StashChanges")

	// The stash is visible via `git stash list` (stored, not just a
	// dangling commit) so a user can recover it manually if needed.
	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "test pause stash")
}

func TestStashChanges_CleanWorktreeReturnsEmpty(t *testing.T) {
	gw := newStashTestRepo(t)

	sha, err := gw.StashChanges("nothing to stash")
	require.NoError(t, err)
	assert.Empty(t, sha)

	out, err := exec.Command("git", "-C", gw.GetWorktreePath(), "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)))
}

func TestApplyStash_RestoresAndDrops(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0644))
	sha, err := gw.StashChanges("test pause stash")
	require.NoError(t, err)

	// Simulate Pause's worktree teardown: reset to what Setup() would
	// recreate on Resume (clean, at the commit the branch pointed to).
	require.NoError(t, exec.Command("git", "-C", dir, "checkout", "--", "tracked.txt").Run())
	require.NoError(t, os.Remove(filepath.Join(dir, "untracked.txt")))

	require.NoError(t, gw.ApplyStash(sha))

	tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(tracked))
	untracked, err := os.ReadFile(filepath.Join(dir, "untracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(untracked))

	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "ApplyStash must drop the entry after a clean apply")
}

func TestApplyStash_EmptyShaIsNoOp(t *testing.T) {
	gw := newStashTestRepo(t)
	assert.NoError(t, gw.ApplyStash(""))
}

func TestApplyStash_ConflictLeavesStashInPlace(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("stashed-version\n"), 0644))
	sha, err := gw.StashChanges("conflicting stash")
	require.NoError(t, err)

	// Reset to clean, then make a conflicting change so applying the
	// stash on top produces a conflict.
	require.NoError(t, exec.Command("git", "-C", dir, "checkout", "--", "tracked.txt").Run())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("different-version\n"), 0644))

	err = gw.ApplyStash(sha)
	assert.Error(t, err)

	out, err := exec.Command("git", "-C", dir, "stash", "list").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "conflicting stash", "a conflicting apply must not drop the stash entry")
}

// TestStashDoesNotInterfereAcrossWorktrees is the regression guard for
// the design's core hazard: refs/stash is a single stack shared by
// every worktree of the same repo. Two GitWorktrees stashing around
// the same time must each apply their own changes, never the other's,
// regardless of push order.
func TestStashDoesNotInterfereAcrossWorktrees(t *testing.T) {
	base := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v failed: %s", args, string(out))
	}
	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repo, 0755))
	run(repo, "git", "init", "-b", "main")
	run(repo, "git", "config", "user.email", "test@example.com")
	run(repo, "git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("base\n"), 0644))
	run(repo, "git", "add", ".")
	run(repo, "git", "commit", "-m", "init")

	wtA := filepath.Join(base, "wtA")
	wtB := filepath.Join(base, "wtB")
	run(repo, "git", "worktree", "add", "-b", "branch-a", wtA)
	run(repo, "git", "worktree", "add", "-b", "branch-b", wtB)

	gwA := NewGitWorktreeFromStorage(repo, wtA, "a", "branch-a", "", true, base)
	gwB := NewGitWorktreeFromStorage(repo, wtB, "b", "branch-b", "", true, base)

	require.NoError(t, os.WriteFile(filepath.Join(wtA, "f.txt"), []byte("a-changes\n"), 0644))
	shaA, err := gwA.StashChanges("from A")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(wtB, "f.txt"), []byte("b-changes\n"), 0644))
	shaB, err := gwB.StashChanges("from B")
	require.NoError(t, err)

	require.NoError(t, exec.Command("git", "-C", wtA, "checkout", "--", "f.txt").Run())
	require.NoError(t, exec.Command("git", "-C", wtB, "checkout", "--", "f.txt").Run())

	require.NoError(t, gwB.ApplyStash(shaB))
	contentB, err := os.ReadFile(filepath.Join(wtB, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b-changes\n", string(contentB), "B must restore its own changes, not A's")

	require.NoError(t, gwA.ApplyStash(shaA))
	contentA, err := os.ReadFile(filepath.Join(wtA, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a-changes\n", string(contentA), "A must restore its own changes, not B's")
}
