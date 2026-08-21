package session

import (
	"testing"

	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
)

// TestDecideResume covers how Resume may treat the worktree. The rule the
// 2026-08-21 incident bought: never destroy a worktree on evidence that
// does not exist. A probe that timed out cannot rule out a live agent
// working inside the tree, so an inconclusive probe must refuse rather
// than rebuild.
func TestDecideResume(t *testing.T) {
	cases := []struct {
		name           string
		live           tmux.Liveness
		worktreeExists bool
		want           resumeAction
	}{
		{"paused session, worktree gone", tmux.LivenessDead, false, resumeRebuild},
		{"paused session, stale worktree left behind", tmux.LivenessDead, true, resumeRebuild},
		{"live agent in its worktree", tmux.LivenessAlive, true, resumeReattach},
		{"live session whose worktree vanished", tmux.LivenessAlive, false, resumeRebuild},
		{"no answer, worktree present", tmux.LivenessUnknown, true, resumeRefuse},
		{"no answer, nothing to destroy", tmux.LivenessUnknown, false, resumeRebuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideResume(tc.live, tc.worktreeExists))
		})
	}
}
