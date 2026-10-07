package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthTick_UnknownLivenessRepairsNothing is the TUI's half of the
// regression guard for the 2026-08-21 false-dead cascade (the model's
// half, that an inconclusive probe never pauses, is core's
// TestApplyLiveness_UnknownDoesNotPause). Heavy load starved the `tmux
// has-session` probes; each timed-out probe read as death, and the health
// tick paused every running session at once — while tmux held them all
// alive. A probe that never got an answer must change nothing: no pause,
// and no client repair either.
func TestHealthTick_UnknownLivenessRepairsNothing(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")

	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessUnknown},
	}})

	assert.Equal(t, session.Running, inst.GetStatus(),
		"an instance must never be paused on a probe that got no answer")
	assert.False(t, m.panes.Alive(inst.Pane().TmuxSessionName()),
		"an inconclusive probe must not trigger ptmx repair either")
}
