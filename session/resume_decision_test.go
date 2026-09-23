package session

import (
	"testing"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
)

// TestDecideResume covers how Resume may treat the worktree. The rule the
// 2026-08-21 incident bought: never destroy a worktree on evidence that
// does not exist. A probe that timed out cannot rule out a live agent
// working inside the tree, so an inconclusive probe must refuse rather
// than rebuild. And a dead agent does not make an intact tree disposable:
// an agent that exited on its own leaves uncommitted work that only
// exists there, so resume relaunches in place instead of rebuilding.
func TestDecideResume(t *testing.T) {
	cases := []struct {
		name string
		live tmux.Liveness
		tree git.TreeState
		want resumeAction
	}{
		{"paused session, worktree gone", tmux.LivenessDead, git.TreeAbsent, resumeRebuild},
		{"agent exited, intact worktree left behind", tmux.LivenessDead, git.TreeIntact, resumeRelaunchInPlace},
		{"agent exited, gutted worktree left behind", tmux.LivenessDead, git.TreeGutted, resumeRebuild},
		{"agent exited, worktree git cannot vouch for", tmux.LivenessDead, git.TreeUnverified, resumeRefuse},
		{"live agent in its worktree", tmux.LivenessAlive, git.TreeIntact, resumeReattach},
		{"live agent in a gutted worktree", tmux.LivenessAlive, git.TreeGutted, resumeReattach},
		{"live agent, worktree unverified", tmux.LivenessAlive, git.TreeUnverified, resumeReattach},
		{"live session whose worktree vanished", tmux.LivenessAlive, git.TreeAbsent, resumeRebuild},
		{"no answer, intact worktree", tmux.LivenessUnknown, git.TreeIntact, resumeRefuse},
		{"no answer, gutted worktree", tmux.LivenessUnknown, git.TreeGutted, resumeRefuse},
		{"no answer, worktree unverified", tmux.LivenessUnknown, git.TreeUnverified, resumeRefuse},
		{"no answer, nothing to destroy", tmux.LivenessUnknown, git.TreeAbsent, resumeRebuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideResume(tc.live, tc.tree))
		})
	}
}
