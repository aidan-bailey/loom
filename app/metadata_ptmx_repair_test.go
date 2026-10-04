package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/require"
)

// setupPtmxDeadFixture builds a started, Running instance whose tmux
// session is alive (TmuxAlive true) but has no attach client in the
// registry — the exact "session healthy, Loom's attach client gone" state
// produced by a failed reattach after full-screen attach.
func setupPtmxDeadFixture(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	m.ws.Add(inst)

	ts := tmux.NewSessionWithDeps("a", "claude", fakePtyFactory{t: t}, aliveCmdExecForTest())
	inst.SetTmuxSession(ts)
	// Marked started, as a restored record whose session runs is.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")
	require.False(t, m.panes.Alive(inst.Pane().TmuxSessionName()), "fixture precondition: no client attached")
	require.True(t, inst.Pane().TmuxAlive(), "fixture precondition: tmux session must read alive")

	return m, inst
}

// TestHealthTick_RepairsDeadPtmx is the regression guard for the
// "PTY is not available" bug: a session that TmuxAlive reports as healthy
// but whose ptmx is nil was never retried by anything, forever. The
// health tick must now notice the client is not attached (core.Alive)
// and re-attach it.
func TestHealthTick_RepairsDeadPtmx(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)

	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessAlive},
	}})

	require.True(t, m.panes.Alive(inst.Pane().TmuxSessionName()), "metadata tick should have repaired the dead ptmx")
}

// TestHealthTick_SkipsRepairDuringFullScreenAttach guards against a
// race with an in-progress full-screen attach: PausePreview legitimately
// nils ptmx for the duration of tea.ExecProcess, and a re-attach racing that
// window would fight the foreground attach over the same tmux session.
func TestHealthTick_SkipsRepairDuringFullScreenAttach(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)
	m.attachingInstance = inst

	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessAlive},
	}})

	require.False(t, m.panes.Alive(inst.Pane().TmuxSessionName()), "repair must not run for the instance currently mid full-screen attach")
}
