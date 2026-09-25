package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu         sync.Mutex
	created    bool
	failStart  bool       // make every new-session fail
	failAttach bool       // make every attach-session (Restore) fail
	failKill   bool       // make every kill-session fail
	launches   [][]string // argv of every new-session
	runs       [][]string // argv of every other command
	// probe, when set, answers has-session instead of the created flag.
	probe func() error
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
	if slices.Contains(cmd.Args, "attach-session") {
		f.mu.Lock()
		fail := f.failAttach
		f.mu.Unlock()
		if fail {
			return nil, errors.New("fake tmux: attach-session failed")
		}
	}
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

// Close implements tmux.PtyFactory.
func (f *fakeTmuxServer) Close() {}

// runner is the tmux command executor backed by this fake server.
func (f *fakeTmuxServer) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			f.mu.Lock()
			f.runs = append(f.runs, slices.Clone(c.Args))
			probe, created, failKill := f.probe, f.created, f.failKill
			f.mu.Unlock()
			if failKill && slices.Contains(c.Args, "kill-session") {
				return errors.New("fake tmux: server not responding")
			}
			if slices.Contains(c.Args, "has-session") {
				if probe != nil {
					return probe()
				}
				if !created {
					return errors.New("can't find session")
				}
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	}
}

// ran reports whether any command's argv contained all of args.
func (f *fakeTmuxServer) ran(args ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, argv := range f.runs {
		if !slices.ContainsFunc(args, func(a string) bool { return !slices.Contains(argv, a) }) {
			return true
		}
	}
	return false
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

// newCrashRecoveredInstance is the record ReconcileAndRestore hands to
// CrashRestart: persisted Running, its tmux session gone, worktree on disk.
func newCrashRecoveredInstance(t *testing.T) (*Instance, *fakeTmuxServer) {
	t.Helper()
	srv := &fakeTmuxServer{}
	inst := newTestPausableInstanceWithExec(t, srv.runner())
	inst.program = "claude"
	orig := newRecoverySession
	newRecoverySession = func(name, program string, env ...string) *tmux.TmuxSession {
		return tmux.NewTmuxSessionWithDeps(name, program, srv, srv.runner(), env...)
	}
	t.Cleanup(func() { newRecoverySession = orig })
	require.Equal(t, Running, inst.GetStatus())
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

	require.NoError(t, resumeLikeApp(t, inst))

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

	require.NoError(t, resumeLikeApp(t, inst))

	assert.FileExists(t, filepath.Join(dir, ".git"), "resume must recreate the worktree")
	assert.Equal(t, "pause-test-branch", gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestResume_MissingAccountFailsBeforeAnySideEffect pins that a Resume
// refused over an unresolvable account touches nothing: no worktree
// rebuild, no stash apply, no launch. Without the check running before
// any of that, this scenario would still fail — but only once
// finishResume's startFreshWithRecovery reaches recoveryLaunch, by which
// point the rebuild below has already recreated the worktree on disk.
func TestResume_MissingAccountFailsBeforeAnySideEffect(t *testing.T) {
	withAccountDirs(t, nil)
	inst, srv := newTickPausedInstance(t)
	inst.SetAccount("gone")
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	gitIn(t, gw.GetRepoPath(), "worktree", "remove", "--force", dir)
	require.NoDirExists(t, dir)

	err = resumeLikeApp(t, inst)

	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.NoDirExists(t, dir, "a refused resume must not rebuild the worktree")
	assert.Empty(t, srv.launchArgs(), "a refused resume must not launch anything")
	assert.Equal(t, Paused, inst.GetStatus())
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

	require.NoError(t, resumeLikeApp(t, inst))

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

	err = resumeLikeApp(t, inst)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "move it aside", "a permanent rejection must say how to proceed")
	assert.NotContains(t, err.Error(), "less busy", "a permanent rejection is not a timeout")
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

	require.Error(t, resumeLikeApp(t, inst))

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

	require.NoError(t, resumeLikeApp(t, inst))

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

	require.NoError(t, resumeLikeApp(t, inst))

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

	requireStashForgotten(t, resumeLikeApp(t, inst), sha)
	assert.Equal(t, "newer edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// TestCrashRestart_RefusesGuttedWorktree: reconcile restarts a crashed
// session whenever its worktree directory exists, but a gutted one has
// no .git, so git in it answers for the enclosing repo. CrashRestart must
// refuse it rather than launch an agent there.
func TestCrashRestart_RefusesGuttedWorktree(t *testing.T) {
	inst, srv := newCrashRecoveredInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(gw.GetWorktreePath(), ".git")))

	err = inst.CrashRestart()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an intact working tree")
	assert.Empty(t, srv.launchArgs(), "no agent may be launched into a gutted worktree")
}

// TestCrashRestart_RelaunchesIntactWorktree keeps the normal crash
// recovery working: an intact worktree gets a fresh agent in place.
func TestCrashRestart_RelaunchesIntactWorktree(t *testing.T) {
	inst, srv := newCrashRecoveredInstance(t)
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
// it to Paused when Resume fails. A Notice alone is a success (the app
// shows it and keeps the instance running); it is returned for the test
// to check.
func resumeLikeApp(t *testing.T, inst *Instance) error {
	t.Helper()
	require.NoError(t, inst.TransitionTo(Loading))
	err := inst.Resume(nil)
	if _, notice := OnlyNotice(err); err != nil && !notice {
		require.NoError(t, inst.TransitionTo(Paused))
	}
	return err
}

// requireStashForgotten asserts err is only the notice that Resume forgot
// the unlisted stash sha, naming it and how to recover it.
func requireStashForgotten(t *testing.T, err error, sha string) {
	t.Helper()
	n, ok := OnlyNotice(err)
	require.True(t, ok, "a forgotten stash must be reported, as a notice: %v", err)
	assert.Contains(t, n.Error(), sha, "the notice names the stash")
	assert.Contains(t, n.Error(), "stash apply "+sha)
	assert.Contains(t, n.Error(), "fsck --unreachable | grep commit")
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

	requireStashForgotten(t, resumeLikeApp(t, inst), sha)

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

// TestResume_InPlaceRecordsMissingBaseCommit: a session whose start was
// interrupted is persisted without a base commit. Rebuilding records one;
// relaunching in place must too, or the session never gets diff stats.
func TestResume_InPlaceRecordsMissingBaseCommit(t *testing.T) {
	inst, _ := newTickPausedInstance(t)
	fixture, err := inst.GetGitWorktree()
	require.NoError(t, err)
	gw := git.NewGitWorktreeFromStorage(fixture.GetRepoPath(), fixture.GetWorktreePath(), "pause-test", "pause-test-branch", "", true, "")
	inst.setGitWorktree(gw)

	require.NoError(t, resumeLikeApp(t, inst))

	assert.Equal(t, gitIn(t, gw.GetWorktreePath(), "rev-parse", "HEAD"), gw.GetBaseCommitSHA())
}

// TestUnverifiedTreeError tells the user what to do, which depends on why
// the tree could not be verified: a git that never answered is worth a
// retry; a tree git rejects will be rejected again, so say how to get past it.
func TestUnverifiedTreeError(t *testing.T) {
	timeout := unverifiedTreeError("/wt", fmt.Errorf("git command timed out after 8s: git rev-parse: %w", git.ErrTimeout))
	assert.Contains(t, timeout.Error(), "retry")
	assert.NotContains(t, timeout.Error(), "move it aside")
	assert.ErrorIs(t, timeout, git.ErrTimeout)

	rejected := unverifiedTreeError("/wt", errors.New("fatal: not a git repository: /gone/.git/worktrees/wt"))
	assert.Contains(t, rejected.Error(), "move it aside")
	assert.Contains(t, rejected.Error(), "/wt")
	assert.NotContains(t, rejected.Error(), "less busy")

	// The mv is a command the user pastes: the path is shell-quoted.
	quoted := unverifiedTreeError("/a b/it's", errors.New("locked \"initializing\""))
	assert.Contains(t, quoted.Error(), `mv '/a b/it'\''s' '/a b/it'\''s.bak'`)
}

// TestResume_RelaunchReleasesTheDeadSession: the dead session object still
// holds its attach client, emulator and output pump. Relaunching must close
// it first, as Restart does — by exact name, since tmux prefix-matches a
// bare -t and the session is gone. Killing the tmux session by name alone
// would pass a kill-session check while leaking all three, so the old
// object is attached (on the fake PTY) first and checked afterwards.
func TestResume_RelaunchReleasesTheDeadSession(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	old := inst.getTmuxSession()
	require.NoError(t, old.Restore(), "attach the old session object, as the health tick left it")
	require.True(t, old.PtmxAlive(), "precondition: it holds an attach client")
	require.True(t, old.HasEmulator(), "precondition: and an emulator")

	require.NoError(t, resumeLikeApp(t, inst))

	assert.False(t, old.PtmxAlive(), "the dead session's attach client must be released")
	assert.False(t, old.HasEmulator(), "and its emulator")
	assert.True(t, srv.ran("kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(inst.Title))),
		"the dead session must be closed, by exact name")
	assert.NotSame(t, old, inst.getTmuxSession())
}

// TestResume_UnansweredProbeWhileFinishingRefuses: finishResume probes the
// session again. If tmux does not answer, it must neither close the old
// session (it may be live) nor start a second one; refuse, retryably —
// whichever path led there: a reattach (Resume's own probe said alive), a
// relaunch in place or a rebuild (it said dead, and the session may have
// come back since).
func TestResume_UnansweredProbeWhileFinishingRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first error                       // Resume's own probe
		setup func(*testing.T, *Instance) // what is on disk
	}{
		{"reattach", nil, func(*testing.T, *Instance) {}},
		{"relaunch in place", errors.New("can't find session"), func(*testing.T, *Instance) {}},
		{"rebuild", errors.New("can't find session"), func(t *testing.T, inst *Instance) {
			gw := inst.getGitWorktree()
			gitIn(t, gw.GetRepoPath(), "worktree", "remove", "--force", gw.GetWorktreePath())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(tmux.SetLivenessProbeTimeoutForTest(20 * time.Millisecond))
			inst, srv := newTickPausedInstance(t)
			tc.setup(t, inst)
			var probes int
			srv.mu.Lock()
			srv.probe = func() error {
				srv.mu.Lock()
				probes++
				n := probes
				srv.mu.Unlock()
				if n == 1 {
					return tc.first
				}
				time.Sleep(60 * time.Millisecond) // outlive the probe deadline → Unknown
				return errors.New("signal: killed")
			}
			srv.mu.Unlock()

			err := resumeLikeApp(t, inst)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "did not answer")
			assert.False(t, srv.ran("kill-session"), "a session that may be live must not be killed")
			assert.Empty(t, srv.launchArgs(), "no second session may be started")
			assert.Equal(t, Paused, inst.GetStatus())
		})
	}
}
