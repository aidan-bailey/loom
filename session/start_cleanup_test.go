package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStartFixture returns a repo with one commit, a config dir, and an
// unstarted instance titled title whose branch is "loom-test/<title>".
// Its tmux session is a fake whose every new-session fails, so Start(true)
// gets as far as the worktree and then fails to launch the agent.
func newStartFixture(t *testing.T, title string) (inst *Instance, repoDir, branch string) {
	t.Helper()
	tmpDir := t.TempDir()
	repoDir = filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	gitIn(t, repoDir, "init", "-b", "main")
	gitIn(t, repoDir, "config", "user.email", "test@example.com")
	gitIn(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0644))
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "commit", "-m", "init")

	inst, err := NewInstance(InstanceOptions{Title: title, Path: repoDir, Program: "claude", ConfigDir: filepath.Join(tmpDir, "config")})
	require.NoError(t, err)
	inst.SetBranchPrefix("loom-test/")
	srv := &fakeTmuxServer{failStart: true}
	inst.setTmuxSession(tmux.NewSessionWithDeps(title, "claude", srv, srv.runner()))
	return inst, repoDir, "loom-test/" + title
}

func branchTip(t *testing.T, repoDir, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestStart_FailedStartKeepsAPreexistingBranch: a new session whose title
// matches a branch an earlier session left behind (a discard or kill that
// kept the branch) checks that branch out. If the agent then fails to
// launch, the cleanup must not delete it: its commits were never this
// session's, and may be unmerged.
func TestStart_FailedStartKeepsAPreexistingBranch(t *testing.T) {
	inst, repoDir, branch := newStartFixture(t, "reused")
	gitIn(t, repoDir, "checkout", "-q", "-b", branch)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "earlier.txt"), []byte("unmerged work\n"), 0644))
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "commit", "-q", "-m", "earlier session's work")
	gitIn(t, repoDir, "checkout", "-q", "main")
	tip := branchTip(t, repoDir, branch)

	require.Error(t, inst.Start(true))

	assert.Equal(t, tip, branchTip(t, repoDir, branch), "a failed start must not delete a branch it did not create")
	gw := inst.getGitWorktree()
	require.NotNil(t, gw)
	assert.NoDirExists(t, gw.GetWorktreePath(), "the worktree the failed start created is removed")
}

// TestKill_KeepsAPreexistingBranchTheSessionReused: a new session whose
// title matches a branch an earlier session left behind (a discard keeps
// the branch) checks that branch out. Killing it later removes its worktree
// but must not delete the branch, whose commits were never this session's,
// and the saved record must say so, or a kill after a restart deletes it.
func TestKill_KeepsAPreexistingBranchTheSessionReused(t *testing.T) {
	inst, repoDir, branch := newStartFixtureOn(t, "reused", &fakeTmuxServer{})
	gitIn(t, repoDir, "checkout", "-q", "-b", branch)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "earlier.txt"), []byte("unmerged work\n"), 0644))
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "commit", "-q", "-m", "earlier session's work")
	gitIn(t, repoDir, "checkout", "-q", "main")
	tip := branchTip(t, repoDir, branch)

	require.NoError(t, inst.Start(true))
	assert.True(t, inst.ToInstanceData().Worktree.IsExistingBranch, "the saved record marks the branch as not this session's")
	wt := inst.getGitWorktree().GetWorktreePath()

	require.NoError(t, inst.Kill())

	assert.NoDirExists(t, wt, "the kill removes the worktree")
	assert.Equal(t, tip, branchTip(t, repoDir, branch), "a kill must not delete a branch the session reused")
}

// TestKill_DeletesTheBranchItsStartCreated pins the other side: a branch the
// session's own start created goes with the kill.
func TestKill_DeletesTheBranchItsStartCreated(t *testing.T) {
	inst, repoDir, branch := newStartFixtureOn(t, "fresh", &fakeTmuxServer{})

	require.NoError(t, inst.Start(true))
	assert.False(t, inst.ToInstanceData().Worktree.IsExistingBranch)
	require.NoError(t, inst.Kill())

	assert.Empty(t, branchTip(t, repoDir, branch), "the branch the start created is deleted")
}

// TestStart_FailedStartRemovesWhatItCreated: when Start created the branch
// itself, a failed launch removes both it and the worktree.
func TestStart_FailedStartRemovesWhatItCreated(t *testing.T) {
	inst, repoDir, branch := newStartFixture(t, "fresh")

	require.Error(t, inst.Start(true))

	assert.Empty(t, branchTip(t, repoDir, branch), "the branch the failed start created is deleted")
	gw := inst.getGitWorktree()
	require.NotNil(t, gw)
	assert.NoDirExists(t, gw.GetWorktreePath())
}
