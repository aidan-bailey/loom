package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDropStash_StoreBackIsRetried: when the drop hits another session's
// entry (the shared stack moved), putting that entry back is the only
// thing between it and gc. One failed store is retried before the user is
// handed the job.
func TestDropStash_StoreBackIsRetried(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("ours\n"), 0644))
	ours, err := gw.StashChanges("ours")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("theirs\n"), 0644))
	theirs := gitOut(t, dir, "stash", "create", "other session")

	var pushOnce, failOnce sync.Once
	runner := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "drop") {
			pushOnce.Do(func() { runGit(t, dir, "stash", "store", "-m", "other session", theirs) })
		}
		var err error
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "store") && slices.Contains(c.Args, theirs) {
			failOnce.Do(func() { err = errors.New("cannot lock ref 'refs/stash'") })
		}
		return err
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, runner)

	require.NoError(t, gwR.DropStash(ours))

	listed := strings.Fields(gitOut(t, dir, "stash", "list", "--format=%H"))
	assert.Contains(t, listed, theirs, "the other session's stash must be stored back")
	assert.NotContains(t, listed, ours)
}

// TestDropStash_StoreBackFailureNamesTheCommit: when both attempts fail,
// the error is all that is left of the other entry: it must name the
// commit and the command that restores it.
func TestDropStash_StoreBackFailureNamesTheCommit(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("ours\n"), 0644))
	ours, err := gw.StashChanges("ours")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("theirs\n"), 0644))
	theirs := gitOut(t, dir, "stash", "create", "other session")

	var pushOnce sync.Once
	runner := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "drop") {
			pushOnce.Do(func() { runGit(t, dir, "stash", "store", "-m", "other session", theirs) })
		}
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "store") && slices.Contains(c.Args, theirs) {
			return errors.New("cannot lock ref 'refs/stash'")
		}
		return nil
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, runner)

	err = gwR.DropStash(ours)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stash store "+theirs)
}

// TestApplyStash_DropFailureIsReported: the apply restored the work, but a
// drop that then fails used to reach only loom.log — and its message may
// be the only record of another session's entry it removed.
func TestApplyStash_DropFailureIsReported(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("stashed\n"), 0644))
	sha, err := gw.StashChanges("ours")
	require.NoError(t, err)
	runGit(t, dir, "checkout", "--", "tracked.txt")

	failDrop := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "drop") {
			return errors.New("cannot lock ref 'refs/stash'")
		}
		return nil
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, failDrop)

	err = gwR.ApplyStash(sha)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStashNotDropped)
	assert.Contains(t, err.Error(), sha)
	body, _ := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	assert.Equal(t, "stashed\n", string(body), "the work itself was restored")
}
