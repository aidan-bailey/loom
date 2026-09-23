package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTmuxServer stands in for tmux across a relaunch, so no test here
// ever reaches a real tmux server. has-session answers "no such session"
// (an answered probe, so liveness reads Dead rather than Unknown) until a
// new-session has been issued; every new-session is recorded, not run.
type fakeTmuxServer struct {
	mu        sync.Mutex
	created   bool
	failStart bool       // make every new-session fail
	launches  [][]string // argv of every new-session
}

// Start implements tmux.PtyFactory.
func (f *fakeTmuxServer) Start(cmd *exec.Cmd) (*os.File, error) {
	if slices.Contains(cmd.Args, "new-session") {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.launches = append(f.launches, slices.Clone(cmd.Args))
		if f.failStart {
			return nil, errors.New("fake tmux: new-session failed")
		}
		f.created = true
	}
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

// Close implements tmux.PtyFactory.
func (f *fakeTmuxServer) Close() {}

// runner is the tmux command executor backed by this fake server.
func (f *fakeTmuxServer) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "has-session") {
				f.mu.Lock()
				defer f.mu.Unlock()
				if !f.created {
					return errors.New("can't find session")
				}
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	}
}

func (f *fakeTmuxServer) launchArgs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.launches)
}

// newTickPausedInstance reproduces the state the health tick leaves an
// instance in when its agent exits on its own (`/exit`, a crash): the
// tmux session is gone, applyLiveness has flipped the status to Paused,
// and — because no real Pause ran — nothing was stashed and the worktree
// is still on disk. Recovery launches go to the returned fake server.
func newTickPausedInstance(t *testing.T) (*Instance, *fakeTmuxServer) {
	t.Helper()
	srv := &fakeTmuxServer{}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	inst.program = "claude"

	orig := newRecoverySession
	newRecoverySession = func(name, program string, env ...string) *tmux.TmuxSession {
		return tmux.NewTmuxSessionWithDeps(name, program, srv, srv.runner(), env...)
	}
	t.Cleanup(func() { newRecoverySession = orig })

	require.Equal(t, tmux.LivenessDead, inst.getTmuxSession().SessionLiveness(),
		"precondition: the agent's tmux session is gone")
	require.NoError(t, inst.TransitionTo(Paused), "precondition: tick marks the instance paused")
	return inst, srv
}

// TestResume_AgentExitedKeepsUncommittedWork is the regression guard for
// the resume data-loss bug. An agent that exits on its own leaves the
// instance marked Paused by the health tick with its worktree — and any
// uncommitted work in it — still on disk. Resume used to treat that like
// a real pause and rebuild the worktree, and the rebuild's
// `git worktree remove -f` deleted the uncommitted work. The tree is
// intact, so Resume must relaunch the agent in it instead.
func TestResume_AgentExitedKeepsUncommittedWork(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("tracked edit\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0644))

	require.NoError(t, inst.Resume(nil))

	tracked, err := os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "tracked edit\n", string(tracked), "the tracked-file modification must survive resume")
	untracked, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	require.NoError(t, err, "the uncommitted file must survive resume")
	assert.Equal(t, "never committed\n", string(untracked))

	assert.Equal(t, Running, inst.GetStatus())
	launches := srv.launchArgs()
	require.Len(t, launches, 1, "resume must relaunch the agent")
	assert.Equal(t, dir, argAfter(launches[0], "-c"), "the agent must be relaunched in the existing worktree")
	assert.Contains(t, launches[0][len(launches[0])-1], "--continue",
		"the relaunch must carry the agent's recovery flag")
}

// argAfter returns the argument following flag in argv, or "".
func argAfter(argv []string, flag string) string {
	for i, a := range argv[:len(argv)-1] {
		if a == flag {
			return argv[i+1]
		}
	}
	return ""
}

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// TestResume_AbsentWorktreeStillRebuilds keeps the ordinary paused case
// working: with nothing on disk there is nothing to lose, so Resume
// recreates the worktree from the branch.
func TestResume_AbsentWorktreeStillRebuilds(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	gitIn(t, gw.GetRepoPath(), "worktree", "remove", "--force", dir)
	require.NoDirExists(t, dir)

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, filepath.Join(dir, ".git"), "resume must recreate the worktree")
	assert.Equal(t, "pause-test-branch", gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestResume_GuttedWorktreeIsSetAsideAndRebuilt keeps the gutted case on
// the existing recovery path: a directory git has let go of (no .git) is
// rebuilt, and its leftovers — possibly uncommitted work git can no
// longer see — are moved aside, not deleted.
func TestResume_GuttedWorktreeIsSetAsideAndRebuilt(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.Remove(filepath.Join(dir, ".git")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stranded.txt"), []byte("stranded\n"), 0644))

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, filepath.Join(dir, ".git"), "the gutted worktree must be rebuilt")
	matches, err := filepath.Glob(dir + ".orphaned*")
	require.NoError(t, err)
	require.Len(t, matches, 1, "the gutted leftovers must be moved aside")
	assert.Equal(t, "stranded\n", readFile(t, filepath.Join(matches[0], "stranded.txt")))
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestResume_UnverifiedWorktreeRefuses: a worktree with a .git entry git
// will not accept may still hold work, so Resume must neither rebuild it
// nor launch into it.
func TestResume_UnverifiedWorktreeRefuses(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent/loom-test\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me\n"), 0644))

	err = inst.Resume(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot confirm the worktree")
	assert.Equal(t, "keep me\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.Equal(t, "gitdir: /nonexistent/loom-test\n", readFile(t, filepath.Join(dir, ".git")))
	assert.Empty(t, srv.launchArgs(), "nothing may be launched into an unverified worktree")
	assert.Equal(t, Paused, inst.GetStatus())
}

// TestResume_InPlaceStartFailureKeepsWorktreeAndBranch guards the failure
// path of a relaunch: a tmux start that fails must not clean up the
// worktree (with its uncommitted work) or delete the session's branch.
func TestResume_InPlaceStartFailureKeepsWorktreeAndBranch(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	srv.failStart = true
	fixture, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := fixture.GetWorktreePath()
	// A branch loom created itself (isExistingBranch=false), which
	// GitWorktree.Cleanup would delete along with the worktree.
	gw := git.NewGitWorktreeFromStorage(fixture.GetRepoPath(), dir, "pause-test", "pause-test-branch", "", false, "")
	inst.setGitWorktree(gw)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0644))

	require.Error(t, inst.Resume(nil))

	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.NotEmpty(t, gitIn(t, gw.GetRepoPath(), "branch", "--list", "pause-test-branch"),
		"a failed relaunch must not delete the session's branch")
}

// TestResume_InPlaceAppliesPendingStashOntoCleanTree covers a rebuild that
// was interrupted after recreating the worktree but before restoring the
// stash: the tree is clean and the stash is the only copy of the work.
func TestResume_InPlaceAppliesPendingStashOntoCleanTree(t *testing.T) {
	inst, _ := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("stashed edit\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("stashed new\n"), 0644))
	sha, err := gw.StashChanges("test pause")
	require.NoError(t, err)
	gw.SetStashRef(sha)
	gitIn(t, dir, "checkout", "--", ".")
	gitIn(t, dir, "clean", "-fd")

	require.NoError(t, inst.Resume(nil))

	assert.Equal(t, "stashed edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "stashed new\n", readFile(t, filepath.Join(dir, "new.txt")))
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// TestResume_InPlaceStashAlreadyOnDisk covers a pause interrupted after
// it stashed but before it removed the worktree. The stash never touched
// the tree, so the work is already on disk: Resume must relaunch without
// reapplying it, and drop the now-redundant entry.
func TestResume_InPlaceStashAlreadyOnDisk(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("tracked edit\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0644))
	sha, err := gw.StashChanges("interrupted pause")
	require.NoError(t, err)
	gw.SetStashRef(sha)

	require.NoError(t, inst.Resume(nil))

	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.Empty(t, gw.GetStashRef())
	assert.Empty(t, gitIn(t, gw.GetRepoPath(), "stash", "list"), "the redundant stash entry must be dropped")
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestResume_InPlaceRefusesDivergentStash: when the worktree has changes
// the stash does not match, applying one onto the other is not safe.
// Resume must refuse and leave both alone, and resume normally once the
// user has dealt with the stash and dropped it.
func TestResume_InPlaceRefusesDivergentStash(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("stashed edit\n"), 0644))
	sha, err := gw.StashChanges("aborted pause")
	require.NoError(t, err)
	gw.SetStashRef(sha)
	// The agent kept working after the stash was taken.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("newer edit\n"), 0644))

	err = resumeLikeApp(t, inst)

	require.Error(t, err)
	// The full SHA, and how to find the entry by it: its stash@{N}
	// position shifts as other sessions stash.
	assert.Contains(t, err.Error(), sha)
	assert.Contains(t, err.Error(), "stash list --format='%gd %H'")
	assert.Equal(t, "newer edit\n", readFile(t, filepath.Join(dir, "README.md")), "the worktree must be left untouched")
	assert.Equal(t, sha, gw.GetStashRef(), "the stash reference must be kept")
	assert.Empty(t, srv.launchArgs())
	assert.Equal(t, Paused, inst.GetStatus())

	// The user keeps the worktree as it is and drops the stash.
	gitIn(t, gw.GetRepoPath(), "stash", "drop", "stash@{0}")

	require.NoError(t, resumeLikeApp(t, inst))
	assert.Equal(t, "newer edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// TestCrashRestart_RefusesGuttedWorktree: reconcile restarts a crashed
// session whenever its worktree directory exists, but a gutted one has
// no .git, so git in it answers for the enclosing repo. CrashRestart must
// refuse it rather than launch an agent there.
func TestCrashRestart_RefusesGuttedWorktree(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(gw.GetWorktreePath(), ".git")))

	err = inst.CrashRestart()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an intact working tree")
	assert.Empty(t, srv.launchArgs(), "no agent may be launched into a gutted worktree")
	assert.Equal(t, Paused, inst.GetStatus())
}

// TestCrashRestart_RelaunchesIntactWorktree keeps the normal crash
// recovery working: an intact worktree gets a fresh agent in place.
func TestCrashRestart_RelaunchesIntactWorktree(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)

	require.NoError(t, inst.CrashRestart())

	launches := srv.launchArgs()
	require.Len(t, launches, 1)
	assert.Equal(t, gw.GetWorktreePath(), argAfter(launches[0], "-c"))
	assert.Equal(t, Running, inst.GetStatus())
}

// resumeLikeApp resumes the way the app does: runResumeSelected moves the
// instance to Loading before Resume runs, and transitionFailedMsg reverts
// it to Paused when Resume fails.
func resumeLikeApp(t *testing.T, inst *Instance) error {
	t.Helper()
	require.NoError(t, inst.TransitionTo(Loading))
	err := inst.Resume(nil)
	if err != nil {
		require.NoError(t, inst.TransitionTo(Paused))
	}
	return err
}

// TestResume_DroppedStashIsNeverReapplied follows the refusal's advice to
// the letter: the user keeps the worktree (commits it) and drops the
// stash. The dropped entry's commit still exists until gc, but it is no
// longer wanted — applying it onto the now-clean tree would conflict with
// the commit and resurrect work the user discarded.
func TestResume_DroppedStashIsNeverReapplied(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("stashed edit\n"), 0644))
	sha, err := gw.StashChanges("aborted pause")
	require.NoError(t, err)
	gw.SetStashRef(sha)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("newer edit\n"), 0644))
	require.Error(t, resumeLikeApp(t, inst), "precondition: the divergent stash is refused")

	gitIn(t, dir, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-am", "keep the worktree")
	gitIn(t, gw.GetRepoPath(), "stash", "drop", "stash@{0}")

	require.NoError(t, resumeLikeApp(t, inst))

	assert.Equal(t, "newer edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Empty(t, gitIn(t, dir, "status", "--porcelain"), "the dropped stash must not be applied")
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestPause_AbortDropsItsStash: when the agent's session survives Close,
// Pause aborts and the agent keeps working in the worktree. The stash it
// took first is then a stale copy of a tree that is still there — left
// pending, a later resume finds the tree diverged and refuses, and the
// next pause overwrites the reference and leaks the entry. Pause must drop
// it and save the cleared reference.
func TestPause_AbortDropsItsStash(t *testing.T) {
	inst := newTestPausableInstanceWithExec(t, cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				return errors.New("tmux: server not responding")
			}
			return nil // has-session succeeds: the session is still alive
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	})
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("tracked edit\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0644))
	var saved []string
	save := func() error {
		saved = append(saved, inst.ToInstanceData().Worktree.StashRef)
		return nil
	}

	require.Error(t, inst.Pause(save))

	assert.Empty(t, gw.GetStashRef())
	require.NotEmpty(t, saved)
	assert.Empty(t, saved[len(saved)-1], "the cleared reference must be saved")
	assert.Empty(t, gitIn(t, gw.GetRepoPath(), "stash", "list"), "the stale stash must be dropped")
	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")), "the worktree keeps the work")
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.Equal(t, Running, inst.GetStatus())
}
