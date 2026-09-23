package session

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
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitFailing runs every git command for real except the one whose argv
// holds all of args, which fails without running, as a git that could not
// lock refs/stash would.
type gitFailing []string

func (g gitFailing) hit(c *exec.Cmd) bool {
	for _, a := range g {
		if !slices.Contains(c.Args, a) {
			return false
		}
	}
	return true
}

func (g gitFailing) Run(c *exec.Cmd) error {
	if g.hit(c) {
		return errors.New("fatal: cannot lock ref 'refs/stash'")
	}
	return c.Run()
}

func (g gitFailing) Output(c *exec.Cmd) ([]byte, error) {
	if g.hit(c) {
		return nil, errors.New("fatal: cannot lock ref 'refs/stash'")
	}
	return c.Output()
}

func (g gitFailing) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if g.hit(c) {
		return nil, errors.New("fatal: cannot lock ref 'refs/stash'")
	}
	return c.CombinedOutput()
}

// withGitRunner replaces inst's worktree with the same one run through
// runner, keeping its pending stash, and returns it.
func withGitRunner(t *testing.T, inst *Instance, runner internalexec.Executor) *git.GitWorktree {
	t.Helper()
	old := inst.getGitWorktree()
	configDir := filepath.Dir(filepath.Dir(old.GetWorktreePath()))
	gw := git.NewGitWorktreeFromStorageWithRunner(old.GetRepoPath(), old.GetWorktreePath(), "pause-test",
		old.GetBranchName(), old.GetBaseCommitSHA(), true, configDir, runner)
	gw.SetStashRef(old.GetStashRef())
	inst.setGitWorktree(gw)
	return gw
}

// stashEdit writes an edit into gw's worktree and stashes it the way Pause
// does, returning the stash's SHA. The edit stays on disk.
func stashEdit(t *testing.T, gw *git.GitWorktree, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(gw.GetWorktreePath(), "README.md"), []byte(body), 0o644))
	sha, err := gw.StashChanges("paused")
	require.NoError(t, err)
	return sha
}

// TestKill_UndroppableStashIsANotice: a paused session's stash that Kill
// cannot drop used to reach only loom.log. The kill itself succeeded, so it
// is a notice, and it names the stash.
func TestKill_UndroppableStashIsANotice(t *testing.T) {
	inst := newTestPausableInstance(t)
	sha := stashEdit(t, inst.getGitWorktree(), "paused edit\n")
	gw := withGitRunner(t, inst, gitFailing{"stash", "drop"})
	gw.SetStashRef(sha)
	dir := gw.GetWorktreePath()

	err := inst.Kill()

	n, ok := OnlyNotice(err)
	require.True(t, ok, "an undroppable stash must be reported as a notice: %v", err)
	assert.Contains(t, n.Error(), sha)
	assert.False(t, inst.Started(), "the kill itself went through")
	assert.NoDirExists(t, dir)
}

// TestPause_AbortReportsUndroppableStash: when Pause aborts (the session
// survived Close) it drops its own stash; a drop that fails must be in the
// error the user sees, naming the entry left on the stash list.
func TestPause_AbortReportsUndroppableStash(t *testing.T) {
	inst := newTestPausableInstanceWithExec(t, cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				return errors.New("tmux: server not responding")
			}
			return nil // has-session succeeds: the session is still alive
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	})
	gw := withGitRunner(t, inst, gitFailing{"stash", "drop"})
	require.NoError(t, os.WriteFile(filepath.Join(gw.GetWorktreePath(), "README.md"), []byte("edit\n"), 0o644))

	err := inst.Pause(func() error { return nil })

	require.Error(t, err)
	listed := gitIn(t, gw.GetRepoPath(), "stash", "list", "--format=%H")
	require.NotEmpty(t, listed, "the entry the drop could not remove")
	assert.Contains(t, err.Error(), "could not drop")
	assert.Contains(t, err.Error(), listed, "the error names the stale entry")
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// TestResume_RebuildNeverReappliesDroppedStash: the rebuild path applied a
// pending stash by SHA without asking `git stash list`, so a stash the user
// had dropped came back — its commit survives until gc. It must be
// forgotten, as in place, and the user told.
func TestResume_RebuildNeverReappliesDroppedStash(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir, repo := gw.GetWorktreePath(), gw.GetRepoPath()
	sha := stashEdit(t, gw, "stashed edit\n")
	gw.SetStashRef(sha)
	gitIn(t, repo, "worktree", "remove", "--force", dir) // as Pause does
	gitIn(t, repo, "stash", "drop", "stash@{0}")         // the user drops it

	requireStashForgotten(t, resumeLikeApp(t, inst), sha)

	assert.Equal(t, "hi", readFile(t, filepath.Join(dir, "README.md")), "the dropped stash must not be applied")
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
	require.Len(t, srv.launchArgs(), 1)
}

// TestResume_RebuildUnreadableStashListChangesNothing: when the stash list
// cannot be read, whether the stash is wanted is unknown: nothing is
// rebuilt or launched.
func TestResume_RebuildUnreadableStashListChangesNothing(t *testing.T) {
	inst, srv := newTickPausedInstance(t)
	fixture, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir, repo := fixture.GetWorktreePath(), fixture.GetRepoPath()
	sha := stashEdit(t, fixture, "stashed edit\n")
	gitIn(t, repo, "worktree", "remove", "--force", dir)
	gw := withGitRunner(t, inst, gitFailing{"stash", "list"})
	gw.SetStashRef(sha)

	err = resumeLikeApp(t, inst)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot tell whether stash")
	assert.NoDirExists(t, dir, "nothing may be rebuilt")
	assert.Empty(t, srv.launchArgs())
	assert.Equal(t, sha, gw.GetStashRef())
	assert.Equal(t, Paused, inst.GetStatus())
}

// TestResume_RebuildUndroppableStashIsANotice: the rebuild restored the
// stash, but its entry could not be dropped. The resume went through; the
// user hears what is left on the stash list.
func TestResume_RebuildUndroppableStashIsANotice(t *testing.T) {
	inst, _ := newTickPausedInstance(t)
	fixture, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir, repo := fixture.GetWorktreePath(), fixture.GetRepoPath()
	sha := stashEdit(t, fixture, "stashed edit\n")
	gitIn(t, repo, "worktree", "remove", "--force", dir)
	gw := withGitRunner(t, inst, gitFailing{"stash", "drop"})
	gw.SetStashRef(sha)

	err = resumeLikeApp(t, inst)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "restored but not dropped must be a notice: %v", err)
	assert.ErrorIs(t, n, git.ErrStashNotDropped)
	assert.Contains(t, n.Error(), sha)
	assert.Equal(t, "stashed edit\n", readFile(t, filepath.Join(dir, "README.md")), "the work was restored")
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// TestResume_InPlaceUndroppableRedundantStashIsANotice: the tree already
// holds the stash (an interrupted pause), so the entry is redundant; one
// that cannot be dropped is reported instead of only logged.
func TestResume_InPlaceUndroppableRedundantStashIsANotice(t *testing.T) {
	inst, _ := newTickPausedInstance(t)
	sha := stashEdit(t, inst.getGitWorktree(), "stashed edit\n")
	gw := withGitRunner(t, inst, gitFailing{"stash", "drop"})
	gw.SetStashRef(sha)

	err := resumeLikeApp(t, inst)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "%v", err)
	assert.Contains(t, n.Error(), sha)
	assert.Empty(t, gw.GetStashRef())
	assert.Equal(t, Running, inst.GetStatus())
}

// newStartFixtureOn is newStartFixture with the fake tmux server supplied.
func newStartFixtureOn(t *testing.T, title string, srv *fakeTmuxServer) (inst *Instance, repoDir, branch string) {
	t.Helper()
	inst, repoDir, branch = newStartFixture(t, title)
	inst.setTmuxSession(tmux.NewTmuxSessionWithDeps(title, "claude", srv, srv.runner()))
	return inst, repoDir, branch
}

// TestStart_UnconfirmedDeathKeepsTheWorktree: under load the existence
// poll reads every unanswered probe as "not yet" and Start times out with
// the agent already launched. Removing the worktree then would orphan a
// running agent in a deleted tree, so it stays until the session is known
// to be gone.
func TestStart_UnconfirmedDeathKeepsTheWorktree(t *testing.T) {
	t.Cleanup(tmux.SetLivenessProbeTimeoutForTest(20 * time.Millisecond))
	srv := &fakeTmuxServer{probe: func() error {
		time.Sleep(60 * time.Millisecond) // outlives the probe deadline
		return errors.New("signal: killed")
	}}
	inst, repoDir, branch := newStartFixtureOn(t, "loaded", srv)

	err := inst.Start(true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "may still be running")
	require.Len(t, srv.launchArgs(), 1, "precondition: the agent was launched")
	gw := inst.getGitWorktree()
	assert.DirExists(t, gw.GetWorktreePath(), "the tree the agent may be running in is kept")
	assert.NotEmpty(t, branchTip(t, repoDir, branch), "and so is its branch")
	assert.Contains(t, err.Error(), gw.GetWorktreePath())
}

// TestStart_LiveSessionAfterFailedAttachKeepsTheWorktree: the session came
// up but the attach failed, and so did the cleanup kill. The agent is
// running; its tree stays.
func TestStart_LiveSessionAfterFailedAttachKeepsTheWorktree(t *testing.T) {
	srv := &fakeTmuxServer{failAttach: true, failKill: true}
	inst, repoDir, branch := newStartFixtureOn(t, "attachfail", srv)

	err := inst.Start(true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "may still be running")
	assert.DirExists(t, inst.getGitWorktree().GetWorktreePath())
	assert.NotEmpty(t, branchTip(t, repoDir, branch))
}

// TestStart_TakenNameStillCleansUp: a name already in use is refused
// before anything launches, so the fresh tree is scratch and goes.
func TestStart_TakenNameStillCleansUp(t *testing.T) {
	srv := &fakeTmuxServer{probe: func() error { return nil }} // loom_taken is alive
	inst, repoDir, branch := newStartFixtureOn(t, "taken", srv)

	err := inst.Start(true)

	require.Error(t, err)
	assert.ErrorIs(t, err, tmux.ErrSessionExists)
	assert.Empty(t, srv.launchArgs())
	assert.NoDirExists(t, inst.getGitWorktree().GetWorktreePath())
	assert.Empty(t, branchTip(t, repoDir, branch), "the branch this start created goes too")
	assert.False(t, strings.Contains(err.Error(), "may still be running"))
}
