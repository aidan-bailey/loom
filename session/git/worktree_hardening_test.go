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
	"time"
	"unicode/utf8"

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

// TestSetup_ReusedBranchIsOwnedOnlyByTheSessionThatCreatedIt: a new
// session's Setup that finds its branch already there checks out a branch
// some earlier session left behind, so Cleanup keeps it; a rebuild of a
// session's own tree (Resume over a removed or gutted tree, from the saved
// record or from the same GitWorktree) finds its own branch there and must
// leave Cleanup deleting it.
func TestSetup_ReusedBranchIsOwnedOnlyByTheSessionThatCreatedIt(t *testing.T) {
	t.Run("a new session reusing a branch keeps it", func(t *testing.T) {
		configDir, repoDir, _, _ := setupTestRepoWithWorktree(t)
		runGit(t, repoDir, "branch", "u/reused")
		prefix := "u/"
		gw, _, err := NewGitWorktreeFromSpec(WorktreeSpec{RepoPath: repoDir, SessionName: "reused", ConfigDir: configDir, BranchPrefix: &prefix})
		require.NoError(t, err)

		require.NoError(t, gw.Setup())
		assert.True(t, gw.IsExistingBranch())
		require.NoError(t, gw.Cleanup())

		assert.True(t, branchExists(t, repoDir, "u/reused"))
	})
	t.Run("a rebuild from the saved record deletes its own branch", func(t *testing.T) {
		configDir, repoDir, _, _ := setupTestRepoWithWorktree(t)
		runGit(t, repoDir, "branch", "u/own")
		gw := NewGitWorktreeFromStorage(repoDir, filepath.Join(configDir, "worktrees", "own_x"), "own", "u/own", "", false, configDir)

		require.NoError(t, gw.Setup())
		assert.False(t, gw.IsExistingBranch())
		require.NoError(t, gw.Cleanup())

		assert.False(t, branchExists(t, repoDir, "u/own"))
	})
	t.Run("a second Setup of a new session's worktree deletes its own branch", func(t *testing.T) {
		configDir, repoDir, _, _ := setupTestRepoWithWorktree(t)
		prefix := "u/"
		gw, _, err := NewGitWorktreeFromSpec(WorktreeSpec{RepoPath: repoDir, SessionName: "again", ConfigDir: configDir, BranchPrefix: &prefix})
		require.NoError(t, err)
		require.NoError(t, gw.Setup())
		require.NoError(t, os.RemoveAll(gw.GetWorktreePath()), "the tree is gone, as before a rebuild")

		require.NoError(t, gw.Setup())
		assert.False(t, gw.IsExistingBranch())
		require.NoError(t, gw.Cleanup())

		assert.False(t, branchExists(t, repoDir, "u/again"))
	})
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

// TestSetup_UnreadableTreeKeepsItsLock: the unlock is for a path git has
// provably let go of. A .git that cannot be stat'ed is unknown, not
// absent, so its lock stays.
func TestSetup_UnreadableTreeKeepsItsLock(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through the permission bits")
	}
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	lockFile := filepath.Join(adminDir(t, worktreePath), "locked")
	require.NoError(t, os.Chmod(worktreePath, 0o000))
	t.Cleanup(func() {
		// Setup may have moved the tree aside; TempDir cleanup needs
		// every copy readable again.
		moved, _ := filepath.Glob(worktreePath + "*")
		for _, p := range moved {
			_ = os.Chmod(p, 0o755)
		}
	})

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	_ = gw.Setup()

	assert.FileExists(t, lockFile, "an unreadable tree is not known to be gone")
}

// TestCleanup_AsksGitNothingItAlreadyKnows: a kill checks the worktree's
// lock twice (before it closes the agent, and in Cleanup, since a lock can
// appear in between), and each check ran `git rev-parse --absolute-git-dir`
// to find the admin dir its .git file names; and Cleanup pruned after a
// remove that had already dropped the tree's registration. Both are
// subprocesses a slow machine makes wait. The lock is found through the
// .git file, and prune runs only when the remove did not. (The trash is
// blocked here, so git removes the tree.)
func TestCleanup_AsksGitNothingItAlreadyKnows(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	blockTrash(t, configDir)
	rec := &recordingRunner{}
	gw := NewGitWorktreeFromStorageWithRunner(repoDir, worktreePath, "x", branchName, "", false, configDir, rec)

	require.NoError(t, RefuseLocked(repoDir, worktreePath, rec))
	require.NoError(t, gw.Cleanup())

	for _, argv := range rec.args {
		assert.False(t, slices.Contains(argv, "rev-parse"), "ran %v", argv)
		assert.False(t, slices.Contains(argv, "prune"), "ran %v", argv)
	}
	assert.NoDirExists(t, worktreePath)
	assert.False(t, branchExists(t, repoDir, branchName))
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), worktreePath, "the registration is gone")
}

// TestRefuseLocked_FindsTheLockThroughARelativeGitFile: git writes a
// relative gitdir into a worktree's .git file under
// worktree.useRelativePaths. The lock must still be found.
func TestRefuseLocked_FindsTheLockThroughARelativeGitFile(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	runGit(t, repoDir, "worktree", "lock", "--reason", "keep me", worktreePath)
	rel, err := filepath.Rel(worktreePath, adminDir(t, worktreePath))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: "+rel+"\n"), 0o644))

	err = RefuseLocked(repoDir, worktreePath, nil)

	require.ErrorIs(t, err, ErrWorktreeLocked)
	assert.Contains(t, err.Error(), "keep me")
}

// blockTrash puts a file where configDir's worktree trash goes, so no tree
// can be moved there and Cleanup removes it with git.
func blockTrash(t *testing.T, configDir string) {
	t.Helper()
	trash, err := TrashDir(configDir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(trash, nil, 0o600))
}

// TestCleanup_MovesAnIntactTreeToTheTrash: `git worktree remove -f` deletes
// the whole tree before it returns (a Rust target/ of several GB), and the
// kill's row spun in Deleting all that while. A tree git's own files show
// to be a linked worktree of this repository is renamed into the trash
// instead, where a background job deletes it (RemoveTrash); its
// registration is pruned and its branch deleted as before.
func TestCleanup_MovesAnIntactTreeToTheTrash(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "work.txt"), []byte("wip"), 0o644))
	rec := &recordingRunner{}
	gw := NewGitWorktreeFromStorageWithRunner(repoDir, worktreePath, "x", branchName, "", false, configDir, rec)

	require.NoError(t, gw.Cleanup())

	trash := gw.Trashed()
	require.NotEmpty(t, trash, "the tree was moved")
	root, err := TrashDir(configDir)
	require.NoError(t, err)
	assert.Equal(t, root, filepath.Dir(trash))
	assert.FileExists(t, filepath.Join(trash, "work.txt"), "for the background job to delete")
	assert.NoDirExists(t, worktreePath)
	for _, argv := range rec.args {
		assert.False(t, slices.Contains(argv, "remove"), "ran %v", argv)
		assert.False(t, slices.Contains(argv, "rev-parse"), "ran %v", argv)
	}
	assert.False(t, branchExists(t, repoDir, branchName))
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), worktreePath, "the registration is gone")

	require.NoError(t, RemoveTrash(trash))
	assert.NoDirExists(t, trash)
}

// TestCleanup_RemovesWithGitWhatItCannotVouchFor pins the fast path's
// checks: a tree whose admin dir no longer points back at it, or another
// repository's worktree, is no tree of this repository's to move, and goes
// to `git worktree remove` as before.
func TestCleanup_RemovesWithGitWhatItCannotVouchFor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(t *testing.T, worktreePath string) (repoDir string)
	}{
		{"its admin dir points elsewhere", func(t *testing.T, worktreePath string) string {
			require.NoError(t, os.WriteFile(filepath.Join(adminDir(t, worktreePath), "gitdir"), []byte("/elsewhere/.git\n"), 0o644))
			return ""
		}},
		{"another repository's", func(t *testing.T, _ string) string {
			_, other, _, _ := setupTestRepoWithWorktree(t)
			return other
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
			if other := tc.break_(t, worktreePath); other != "" {
				repoDir = other
			}
			gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "x", branchName, "", true, configDir)

			_ = gw.Cleanup()

			assert.Empty(t, gw.Trashed(), "nothing was moved")
		})
	}
}

// TestPurgeWorktreeTrash_RemovesWhatAStopLeft: a daemon stopped before its
// background delete ran leaves the moved tree in the trash, which nothing
// else would delete. The next load purges the trash dir.
func TestPurgeWorktreeTrash_RemovesWhatAStopLeft(t *testing.T) {
	configDir := t.TempDir()
	root, err := TrashDir(configDir)
	require.NoError(t, err)
	left := filepath.Join(root, "x_18be000000000001.18be000000000002")
	require.NoError(t, os.MkdirAll(filepath.Join(left, "target", "debug"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(left, "target", "debug", "bin"), []byte("x"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(left, "target"), 0o555), "a read-only dir, as a module cache leaves")

	assert.Equal(t, 1, PurgeWorktreeTrash(configDir))

	assert.NoDirExists(t, left)
}

// TestLinkedWorktreeOf_ALockedTreeIsNotMoved: Cleanup refuses a locked tree
// before it moves anything (RefuseLocked), but a lock taken in between must
// still keep the tree out of the trash: git's remove, which refuses it, is
// the path a locked tree takes.
func TestLinkedWorktreeOf_ALockedTreeIsNotMoved(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	require.True(t, linkedWorktreeOf(repoDir, worktreePath), "fixture: an unlocked tree is vouched for")
	runGit(t, repoDir, "worktree", "lock", worktreePath)

	assert.False(t, linkedWorktreeOf(repoDir, worktreePath))
}

// TestRunGit_FailureErrorIsBounded pins the 2026-10-22 log: a `git diff`
// that printed a whole diff and then died ("fatal: unable to stat …") put
// all of it, 1.6 MB, into the error string, and so into serve.log through
// diff_stats_update_failed. The error keeps the start and the end of what
// git printed (the cause is usually at the end of combined output) and
// says how much it dropped.
func TestRunGit_FailureErrorIsBounded(t *testing.T) {
	huge := strings.Repeat("+a line of somebody's diff\n", 60_000) // ~1.6 MB
	out := "diff --git a/x b/x\n" + huge + "fatal: unable to stat 'x.tmp.1.2': No such file or directory"
	fail := func(*exec.Cmd) ([]byte, error) { return []byte(out), errors.New("exit status 128") }
	gw := NewGitWorktreeFromStorageWithRunner("/repo", "/repo/.loom/wt", "s", "b", "", false, t.TempDir(),
		cmd_test.MockCmdExec{CombinedOutputFunc: fail, OutputFunc: fail})

	_, err := gw.runGitCommand("/repo", "diff", "abc")

	require.Error(t, err)
	assert.Less(t, len(err.Error()), 4096, "the error must not carry git's whole output")
	assert.Contains(t, err.Error(), "diff --git a/x b/x", "the start of the output is kept")
	assert.Contains(t, err.Error(), "fatal: unable to stat 'x.tmp.1.2'", "the cause at the end is kept")
	assert.Contains(t, err.Error(), "bytes omitted")
	assert.Contains(t, err.Error(), "exit status 128", "the wrapped exit error is still named")
}

// TestRunGit_ShortFailureOutputIsUnchanged: the callers that classify git's
// English errors (isBranchAbsentErr, …) read the message verbatim, so a
// short one must pass through untouched.
func TestRunGit_ShortFailureOutputIsUnchanged(t *testing.T) {
	fail := func(*exec.Cmd) ([]byte, error) {
		return []byte("error: branch 'foo' not found.\n"), errors.New("exit status 1")
	}
	gw := NewGitWorktreeFromStorageWithRunner("/repo", "/repo/.loom/wt", "s", "b", "", false, t.TempDir(),
		cmd_test.MockCmdExec{CombinedOutputFunc: fail, OutputFunc: fail})

	_, err := gw.runGitCommand("/repo", "branch", "-D", "foo")

	require.Error(t, err)
	assert.Equal(t, "git command failed: error: branch 'foo' not found. (exit status 1)", err.Error())
}

// TestBoundOutput_NeverCutsACharacterInTwo: both cut points land inside a
// two-byte character here, and a split one would put invalid UTF-8 into the
// log record.
func TestBoundOutput_NeverCutsACharacterInTwo(t *testing.T) {
	out := "x" + strings.Repeat("é", 3000) + "y"

	got := boundOutput(out)

	assert.True(t, utf8.ValidString(got), "bounded output must stay valid UTF-8")
	assert.Contains(t, got, "bytes omitted")
	atLimit := out[:maxFailureOutput]
	assert.Equal(t, atLimit, boundOutput(atLimit), "output at the limit is untouched")
}
