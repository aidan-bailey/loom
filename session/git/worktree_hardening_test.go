package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockInitializing leaves worktreePath's registry entry the way a `git
// worktree add` killed mid-checkout does: locked "initializing".
func lockInitializing(t *testing.T, worktreePath string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(adminDir(t, worktreePath), "locked"), []byte("initializing"), 0o644))
}

// TestSetup_RebuildsAfterInterruptedAddIsMovedAside follows Resume's advice
// for a tree still locked "initializing" (a `worktree add` killed at its
// deadline on a large repo): move it aside and resume. On git 2.55 that
// left `worktree remove -f` refusing the locked entry and `worktree add`
// refusing the path ("a missing but locked worktree … use 'unlock' and
// 'prune'"), prune or not, so the session could never be rebuilt.
func TestSetup_RebuildsAfterInterruptedAddIsMovedAside(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	require.NoError(t, os.Rename(worktreePath, worktreePath+".bak"))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	require.NoError(t, gw.Setup(), "a moved-aside interrupted add must not block the rebuild")

	state, err := gw.InspectTree()
	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state, "the rebuilt tree is complete and no longer locked")
	assert.DirExists(t, worktreePath+".bak", "the moved-aside tree is the user's, untouched")
}

// TestSetup_RebuildsGuttedLockedWorktree: the same lock on a gutted tree
// (no .git) must not block the move-aside rebuild either.
func TestSetup_RebuildsGuttedLockedWorktree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	require.NoError(t, gw.Setup())

	state, err := gw.InspectTree()
	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state)
	matches, _ := filepath.Glob(worktreePath + ".orphaned*")
	assert.NotEmpty(t, matches, "the gutted leftovers are set aside, not deleted")
}

// TestSetup_KeepsTheLockOfALiveTree: the unlock is only for an entry
// nothing live is left behind. A tree that still has its .git may hold
// work, and its lock is what stops `remove -f` from deleting it.
func TestSetup_KeepsTheLockOfALiveTree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "work.txt"), []byte("uncommitted\n"), 0o644))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	err := gw.Setup()

	require.Error(t, err)
	assert.FileExists(t, filepath.Join(worktreePath, "work.txt"), "a tree with its .git is never deleted")
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"), "and its lock is kept")
}

// slowAddRunner fakes a git whose `worktree add` takes d (a checkout of a
// large repository on a loaded box) and whose every other command
// succeeds at once, reporting the branch as present.
func slowAddRunner(d time.Duration) cmd_test.MockCmdExec {
	run := func(c *exec.Cmd) ([]byte, error) {
		if slices.Contains(c.Args, "worktree") && slices.Contains(c.Args, "add") {
			time.Sleep(d)
		}
		return []byte{}, nil
	}
	return cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := run(c); return err },
		OutputFunc:         run,
		CombinedOutputFunc: run,
	}
}

func withAddTimeouts(t *testing.T, tick, add time.Duration) {
	t.Helper()
	prevTick, prevAdd := gitTimeout, gitWorktreeAddTimeout
	gitTimeout, gitWorktreeAddTimeout = tick, add
	t.Cleanup(func() { gitTimeout, gitWorktreeAddTimeout = prevTick, prevAdd })
}

// TestWorktreeAdd_NotBoundByTickTimeout: `worktree add` ran under the 8s
// tick budget, so a slow checkout was killed half-way and left the tree
// locked "initializing". An add that outlives gitTimeout must finish.
func TestWorktreeAdd_NotBoundByTickTimeout(t *testing.T) {
	withAddTimeouts(t, 20*time.Millisecond, 5*time.Second)
	dir := t.TempDir()
	gw := NewGitWorktreeFromStorageWithRunner(dir, filepath.Join(dir, "wt"), "s", "b", "", true, dir, slowAddRunner(120*time.Millisecond))

	require.NoError(t, gw.Setup(), "an add slower than the tick budget must still complete")
}

// TestWorktreeAdd_HasItsOwnDeadline: bounded all the same, so a truly hung
// git surfaces as an error.
func TestWorktreeAdd_HasItsOwnDeadline(t *testing.T) {
	withAddTimeouts(t, 5*time.Second, 20*time.Millisecond)
	dir := t.TempDir()
	gw := NewGitWorktreeFromStorageWithRunner(dir, filepath.Join(dir, "wt"), "s", "b", "", true, dir, slowAddRunner(120*time.Millisecond))

	err := gw.Setup()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 20ms")
}

// recordingRunner runs every command for real and records its argv.
type recordingRunner struct {
	mu   sync.Mutex
	args [][]string
}

func (r *recordingRunner) record(c *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, slices.Clone(c.Args))
}

func (r *recordingRunner) Run(c *exec.Cmd) error { r.record(c); return c.Run() }
func (r *recordingRunner) Output(c *exec.Cmd) ([]byte, error) {
	r.record(c)
	return c.Output()
}
func (r *recordingRunner) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	r.record(c)
	return c.CombinedOutput()
}

// TestSetupNewWorktree_DeletesNoBranch: a new session's Setup only creates
// a branch after show-ref said it is absent, so a `branch -D` there could
// only ever delete one created since — another session's.
func TestSetupNewWorktree_DeletesNoBranch(t *testing.T) {
	configDir, repoDir, _, _ := setupTestRepoWithWorktree(t)
	rec := &recordingRunner{}
	gw := NewGitWorktreeFromStorageWithRunner(repoDir, filepath.Join(configDir, "worktrees", "fresh_x"), "fresh", "u/fresh", "", false, configDir, rec)

	require.NoError(t, gw.Setup())

	for _, argv := range rec.args {
		assert.False(t, slices.Contains(argv, "branch") && slices.Contains(argv, "-D"),
			"Setup ran %v", argv)
	}
	assert.True(t, branchExists(t, repoDir, "u/fresh"))
}

// statFailRunner runs everything for real, except that it answers the
// repository's own `rev-parse --git-common-dir` with a path that does not
// exist, as a permission error or a vanished directory would leave it.
type statFailRunner struct{ repoDir string }

func (r statFailRunner) fake(c *exec.Cmd) ([]byte, bool) {
	if slices.Contains(c.Args, r.repoDir) && slices.Contains(c.Args, "--git-common-dir") && !slices.Contains(c.Args, "--show-toplevel") {
		return []byte(filepath.Join(r.repoDir, "gone", ".git") + "\n"), true
	}
	return nil, false
}
func (r statFailRunner) Run(c *exec.Cmd) error { return c.Run() }
func (r statFailRunner) Output(c *exec.Cmd) ([]byte, error) {
	if out, ok := r.fake(c); ok {
		return out, nil
	}
	return c.Output()
}
func (r statFailRunner) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if out, ok := r.fake(c); ok {
		return out, nil
	}
	return c.CombinedOutput()
}

// TestInspectTree_StatFailureIsNotAnotherRepository: a path InspectTree
// cannot stat is unverified because of that error — not reported as the
// tree belonging to some other repository, which sends the user chasing
// the wrong problem.
func TestInspectTree_StatFailureIsNotAnotherRepository(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	gw := NewGitWorktreeFromStorageWithRunner(repoDir, worktreePath, "sess", "b", "", true, "", statFailRunner{repoDir: repoDir})

	state, err := gw.InspectTree()

	require.Error(t, err)
	assert.Equal(t, TreeUnverified, state)
	assert.NotContains(t, err.Error(), "belongs to the repository")
	assert.ErrorIs(t, err, os.ErrNotExist)
}
