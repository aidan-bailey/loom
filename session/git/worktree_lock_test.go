package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backdateLock ages worktreePath's lock file past staleInitLockAge, as if
// the `git worktree add` that wrote it was killed long ago.
func backdateLock(t *testing.T, worktreePath string) {
	t.Helper()
	old := time.Now().Add(-staleInitLockAge() - time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(adminDir(t, worktreePath), "locked"), old, old))
}

// TestUnlockStaleInit_RemovesAStaleInitializingLock: the kermit case — a
// tree locked "initializing" for days by an add loom killed at its deadline.
func TestUnlockStaleInit_RemovesAStaleInitializingLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, unlocked, state)
	assert.Equal(t, "initializing", reason)
	assert.NoFileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}

// TestUnlockStaleInit_KeepsAFreshInitializingLock: a lock younger than the
// add deadline may belong to an add still checking out.
func TestUnlockStaleInit_KeepsAFreshInitializingLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, lockKept, state)
	assert.Equal(t, "initializing", reason)
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}

// TestUnlockStaleInit_KeepsAUserLock: age never unlocks a lock the user set.
func TestUnlockStaleInit_KeepsAUserLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	runGit(t, repoDir, "worktree", "lock", "--reason", "on a removable disk", worktreePath)
	backdateLock(t, worktreePath)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, lockKept, state)
	assert.Equal(t, "on a removable disk", reason)
}

func TestUnlockStaleInit_NotLocked(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)

	state, reason, err := unlockStaleInit(repoDir, worktreePath, nil)

	require.NoError(t, err)
	assert.Equal(t, notLocked, state)
	assert.Empty(t, reason)
}

// TestUnlockStaleInit_GuttedTreeRunsNothing: git run inside a tree with no
// .git answers for whatever repo encloses it, so nothing runs there.
func TestUnlockStaleInit_GuttedTreeRunsNothing(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	lock := filepath.Join(adminDir(t, worktreePath), "locked")
	require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))
	ran := false
	r := hookRunner{before: func(*exec.Cmd) error { ran = true; return nil }}

	state, _, err := unlockStaleInit(repoDir, worktreePath, r)

	require.NoError(t, err)
	assert.Equal(t, notLocked, state)
	assert.False(t, ran, "no git command runs inside a gutted tree")
	assert.FileExists(t, lock, "a gutted tree keeps its existing handling")
}

// TestUnlockStaleInit_RefusesAnUnusableGitDir: git must answer with an
// absolute git dir. An empty or relative answer would be joined onto the
// process's cwd, so the lock of some other tree could be read — and
// removed. It is an error, and nothing past the answer runs.
func TestUnlockStaleInit_RefusesAnUnusableGitDir(t *testing.T) {
	for name, answer := range map[string]string{"empty": "", "relative": ".git\n"} {
		t.Run(name, func(t *testing.T) {
			_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
			lockInitializing(t, worktreePath)
			backdateLock(t, worktreePath)
			r := cmd_test.MockCmdExec{
				OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(answer), nil },
				RunFunc: func(*exec.Cmd) error {
					t.Error("nothing but the rev-parse may run")
					return nil
				},
				CombinedOutputFunc: func(*exec.Cmd) ([]byte, error) {
					t.Error("nothing but the rev-parse may run")
					return nil, nil
				},
			}

			state, _, err := unlockStaleInit(repoDir, worktreePath, r)

			require.Error(t, err)
			assert.Equal(t, notLocked, state)
			assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
		})
	}
}

// TestUnlockStaleInit_FailedUnlockKeepsTheLock: a stale lock that git will
// not remove stays a respected lock, so the caller refuses the tree rather
// than hand `remove -f` one it will refuse itself. The refusal carries why
// the unlock failed, and says so: no add is running, so it must not claim
// one may be, nor that loom will unlock the tree later.
func TestUnlockStaleInit_FailedUnlockKeepsTheLock(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	errUnlock := errors.New("unlock refused")
	failUnlock := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "unlock") {
			return errUnlock
		}
		return nil
	}}

	state, reason, err := unlockStaleInit(repoDir, worktreePath, failUnlock)

	require.Error(t, err)
	assert.Equal(t, lockKept, state)
	assert.Equal(t, "initializing", reason)
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))

	refused := RefuseLocked(repoDir, worktreePath, failUnlock)

	var locked *LockedError
	require.ErrorAs(t, refused, &locked)
	require.ErrorIs(t, refused, ErrWorktreeLocked)
	assert.Equal(t, "initializing", locked.Reason)
	require.Error(t, locked.UnlockErr, "RefuseLocked passes the unlock failure on")
	assert.ErrorIs(t, refused, errUnlock, "Unwrap reaches the unlock failure")
	assert.Equal(t, locked.UnlockErr, errors.Unwrap(refused))
	msg := refused.Error()
	assert.Contains(t, msg, "reason: initializing")
	assert.Contains(t, msg, "interrupted `git worktree add`")
	assert.Contains(t, msg, "could not unlock it")
	assert.Contains(t, msg, "unlock refused", "the failure is in the message")
	assert.Contains(t, msg, "worktree unlock")
	assert.Equal(t, 1, strings.Count(msg, worktreePath), "the worktree is named once, in the command")
	assert.NotContains(t, msg, "may still be checking")
	assert.NotContains(t, msg, "by itself")
}

// TestRefuseLocked_RespectedLockCarriesNoUnlockFailure: a lock that was
// never up for removal has no unlock failure to report.
func TestRefuseLocked_RespectedLockCarriesNoUnlockFailure(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath) // fresh: may belong to a running add

	refused := RefuseLocked(repoDir, worktreePath, nil)

	var locked *LockedError
	require.ErrorAs(t, refused, &locked)
	assert.NoError(t, locked.UnlockErr)
	assert.NoError(t, errors.Unwrap(refused))
	require.ErrorIs(t, refused, ErrWorktreeLocked)
}

// TestLockedError_Message: the message leads with the reason and the remedy
// (the error bar is cut off at its width) and names the worktree once, inside
// the command, whose single path works from the tree itself. The reason stays
// on one line, a missing one is said so, and an "initializing" lock is not
// offered up for unlocking while the add that wrote it may still be running —
// unless loom already found it stale and could not remove it, when the
// message says exactly that.
func TestLockedError_Message(t *testing.T) {
	const repo, wt = "/the/repo", "/the/wt"
	unlock := "`git -C '/the/wt' worktree unlock .`"

	t.Run("user lock", func(t *testing.T) {
		msg := (&LockedError{Repo: repo, Path: wt, Reason: "keep me"}).Error()

		assert.Equal(t, "worktree locked (reason: keep me); unlock it with "+unlock+" to let loom remove it", msg)
	})
	t.Run("multi-line reason", func(t *testing.T) {
		msg := (&LockedError{Repo: repo, Path: wt, Reason: "line one\nline two\n\tindented"}).Error()

		assert.Contains(t, msg, "worktree locked (reason: line one line two indented); ")
		assert.NotContains(t, msg, "\n")
	})
	t.Run("no reason", func(t *testing.T) {
		msg := (&LockedError{Repo: repo, Path: wt, Reason: " \n"}).Error()

		assert.Contains(t, msg, "worktree locked (reason: none given); ")
	})
	t.Run("initializing", func(t *testing.T) {
		msg := (&LockedError{Repo: repo, Path: wt, Reason: "initializing"}).Error()

		assert.Equal(t, "worktree locked (reason: initializing) by a `git worktree add` that may still be checking it out; "+
			"loom unlocks it by itself after 6 minutes, or if no add is running: "+unlock, msg)
		assert.NotContains(t, msg, "6m0s")
	})
	t.Run("initializing, stale, unlock failed", func(t *testing.T) {
		e := &LockedError{Repo: repo, Path: wt, Reason: "initializing",
			UnlockErr: errors.New("git worktree unlock: fatal: cannot unlock\n\tthe lock (exit status 128)")}

		msg := e.Error()

		assert.Equal(t, "worktree locked (reason: initializing) by an interrupted `git worktree add`; "+
			"loom could not unlock it (git worktree unlock: fatal: cannot unlock the lock (exit status 128)): "+unlock, msg)
		assert.ErrorIs(t, e, ErrWorktreeLocked)
		assert.Equal(t, e.UnlockErr, errors.Unwrap(e))
	})
	t.Run("the path appears once, quoted, and the repo not at all", func(t *testing.T) {
		for name, e := range map[string]*LockedError{
			"user":         {Repo: repo, Path: wt, Reason: "keep me"},
			"initializing": {Repo: repo, Path: wt, Reason: "initializing"},
			"unlock":       {Repo: repo, Path: wt, Reason: "initializing", UnlockErr: errors.New("boom")},
		} {
			msg := e.Error()
			assert.Equal(t, 1, strings.Count(msg, wt), name)
			assert.NotContains(t, msg, repo, name)
			assert.NotContains(t, msg, "worktree "+wt+" is locked", name)
		}
	})
	t.Run("a path with a quote is shell-quoted", func(t *testing.T) {
		msg := (&LockedError{Repo: repo, Path: "/it's/wt", Reason: "keep me"}).Error()

		assert.Contains(t, msg, `git -C '/it'\''s/wt' worktree unlock .`)
	})
}

// TestMinutesText: the stale-lock age is told in whole minutes, rounded up
// so "older than N minutes" is never an overstatement of the real age.
func TestMinutesText(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{6 * time.Minute, "6 minutes"},
		{time.Minute, "1 minute"},
		{time.Minute + time.Second, "2 minutes"},
		{2*time.Minute + 30*time.Second, "3 minutes"},
		{time.Second, "1 minute"},
	} {
		assert.Equal(t, tc.want, minutesText(tc.d), tc.d.String())
	}
}

// TestCleanup_RemovesATreeWithAStaleInitializingLock: Kill.
func TestCleanup_RemovesATreeWithAStaleInitializingLock(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", false, configDir)

	require.NoError(t, gw.Cleanup())

	assert.NoDirExists(t, worktreePath)
	assert.False(t, branchExists(t, repoDir, branchName), "Kill deletes a branch the session created")
}

// TestRemove_RemovesATreeWithAStaleInitializingLock: Pause.
func TestRemove_RemovesATreeWithAStaleInitializingLock(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)

	require.NoError(t, gw.Remove())

	assert.NoDirExists(t, worktreePath)
	assert.True(t, branchExists(t, repoDir, branchName), "Pause keeps the branch")
}

// TestCleanupAndRemove_RefuseARespectedLock: a lock loom respects stops
// Kill and Pause before anything is deleted, with an error naming the fix.
func TestCleanupAndRemove_RefuseARespectedLock(t *testing.T) {
	locks := []struct {
		name   string
		lock   func(t *testing.T, repoDir, wt string)
		reason string
	}{
		{"fresh initializing", func(t *testing.T, _, wt string) { lockInitializing(t, wt) }, "initializing"},
		{"user lock", func(t *testing.T, repoDir, wt string) {
			runGit(t, repoDir, "worktree", "lock", "--reason", "on a removable disk", wt)
			backdateLock(t, wt)
		}, "on a removable disk"},
	}
	ops := []struct {
		name string
		run  func(*GitWorktree) error
	}{
		{"Cleanup", (*GitWorktree).Cleanup},
		{"Remove", (*GitWorktree).Remove},
	}
	for _, lk := range locks {
		for _, op := range ops {
			t.Run(lk.name+"/"+op.name, func(t *testing.T) {
				configDir, repoDir, wt, branch := setupTestRepoWithWorktree(t)
				lk.lock(t, repoDir, wt)
				require.NoError(t, os.WriteFile(WorktreeTitleSidecarPath(wt), []byte("sess"), 0o644))
				gw := NewGitWorktreeFromStorage(repoDir, wt, "sess", branch, "", false, configDir)

				err := op.run(gw)

				require.ErrorIs(t, err, ErrWorktreeLocked)
				assert.Contains(t, err.Error(), "reason: "+lk.reason)
				assert.Contains(t, err.Error(), "worktree unlock")
				assert.DirExists(t, wt)
				assert.True(t, branchExists(t, repoDir, branch), "the branch stays with its tree")
				assert.FileExists(t, WorktreeTitleSidecarPath(wt), "and so does its title sidecar")
			})
		}
	}
}

// TestSetup_KeepsAStaleLockOfALiveTree: the stale-lock unlock belongs to
// the deleting paths only. A rebuild never unlocks an intact tree, however
// old its lock: the lock is what keeps `remove -f` off its work.
func TestSetup_KeepsAStaleLockOfALiveTree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	lockInitializing(t, worktreePath)
	backdateLock(t, worktreePath)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "work.txt"), []byte("uncommitted\n"), 0o644))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	require.Error(t, gw.Setup())

	assert.FileExists(t, filepath.Join(worktreePath, "work.txt"))
	assert.FileExists(t, filepath.Join(adminDir(t, worktreePath), "locked"))
}
