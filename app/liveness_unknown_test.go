package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyLiveness_UnknownDoesNotPause is the regression guard for the
// 2026-08-21 false-dead cascade. Heavy load starved the `tmux has-session`
// probes; each timed-out probe read as death, and the health tick paused
// every running session at once — while tmux held them all alive. Because
// load hits every probe simultaneously, this failure is fleet-wide by
// construction, and each bogus pause then invited a resume that rebuilt a
// live worktree. An instance must never change state on a probe that
// never got an answer.
func TestApplyLiveness_UnknownDoesNotPause(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")

	alive := m.applyLiveness(inst, tmux.LivenessUnknown, false)

	assert.True(t, alive, "an inconclusive probe must leave the instance treated as running")
	assert.Equal(t, session.Running, inst.GetStatus(),
		"an instance must never be paused on a probe that got no answer")
	assert.False(t, inst.PtmxAlive(),
		"an inconclusive probe must not trigger ptmx repair either")
}

// TestApplyLiveness_DeadStillPauses pins the other half: a probe that
// actually got a negative answer is still actionable evidence, so the
// pause path must keep working.
func TestApplyLiveness_DeadStillPauses(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)

	alive := m.applyLiveness(inst, tmux.LivenessDead, false)

	assert.False(t, alive)
	assert.Equal(t, session.Paused, inst.GetStatus(),
		"an answered has-session failure must still pause the instance")
}
