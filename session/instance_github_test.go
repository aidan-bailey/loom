package session

import (
	"testing"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstance_IssueRoundTripsThroughInstanceData(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetIssue(42)
	assert.Equal(t, 42, inst.IssueNumber())

	data := inst.ToInstanceData()
	assert.Equal(t, 42, data.Issue)

	back, err := FromInstanceData(data, "")
	require.NoError(t, err)
	assert.Equal(t, 42, back.IssueNumber())
}

func TestInstance_GitHubStateIsTransient(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.False(t, inst.GitHubState().Known)
	inst.SetGitHubState(github.State{Known: true, HasPR: true, PRNumber: 5})
	assert.Equal(t, 5, inst.GitHubState().PRNumber)
	// Not serialized: a fresh decode has no state.
	back, err := FromInstanceData(inst.ToInstanceData(), "")
	require.NoError(t, err)
	assert.False(t, back.GitHubState().Known)
}

func TestInstance_ParityAccessors(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	_, _, ok := inst.Parity()
	assert.False(t, ok)
	inst.setParity(3, 1, true)
	a, b, ok := inst.Parity()
	assert.True(t, ok)
	assert.Equal(t, 3, a)
	assert.Equal(t, 1, b)
}

// newParityCandidate returns a *started* Instance backed by a real,
// one-commit git repo (used as both repo and worktree path) and a real
// GitWorktree pointing "HEAD" at itself, seeded with a known-good parity
// value. Unlike a bare NewInstance (no gitWorktree, so UpdateParity always
// bottoms out at its "gw == nil" branch regardless of which guard fires),
// this makes every guard in UpdateParity load-bearing: skip it, and
// execution reaches a git.AheadBehind call that actually succeeds
// ("HEAD"..."HEAD" is always 0/0), flipping ok to true. That is what lets
// TestInstance_UpdateParitySkipsAndClears's subtests each prove their one
// guard is necessary, not just that *some* early return happened to fire.
func newParityCandidate(t *testing.T) *Instance {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "commit", "--allow-empty", "-qm", "init")

	inst, err := NewInstance(InstanceOptions{Title: "t", Path: repo, Program: "claude"})
	require.NoError(t, err)
	inst.setStarted(true)
	inst.setGitWorktree(git.NewGitWorktreeFromStorage(repo, repo, "t", "HEAD", "", false, ""))
	inst.setParity(3, 1, true)
	return inst
}

// UpdateParity's skip paths must CLEAR parity to unknown, not merely
// decline to set it — a stale "↑3 ↓1" on a paused or terminal session
// is worse than no badge. Each subtest starts from a fully "live" instance
// (real worktree, started, seeded parity) and flips only the one attribute
// its guard checks, so a dropped guard reaches the real git.AheadBehind
// call and flips ok to true instead of silently no-op'ing on a nil
// worktree — see newParityCandidate.
func TestInstance_UpdateParitySkipsAndClears(t *testing.T) {
	t.Run("empty base", func(t *testing.T) {
		inst := newParityCandidate(t)
		inst.UpdateParity("")
		_, _, ok := inst.Parity()
		assert.False(t, ok, "no base ref: nothing to count against")
	})

	t.Run("workspace terminal", func(t *testing.T) {
		inst := newParityCandidate(t)
		inst.IsWorkspaceTerminal = true
		inst.UpdateParity("HEAD")
		_, _, ok := inst.Parity()
		assert.False(t, ok, "workspace terminals have no session branch")
	})

	t.Run("unstarted instance", func(t *testing.T) {
		inst := newParityCandidate(t)
		inst.setStarted(false)
		inst.UpdateParity("HEAD")
		_, _, ok := inst.Parity()
		assert.False(t, ok, "no worktree until started")
	})
}
