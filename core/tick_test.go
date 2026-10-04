package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probedRunning builds a started, Running instance titled "a" whose tmux
// session reads alive, held by m's classic workspace: app's
// setupPtmxDeadFixture minus the pane client, which is the TUI's.
func probedRunning(t *testing.T, m *Model) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	hold(m, inst)

	ts := tmux.NewSessionWithDeps("a", "claude", fakePtyFactory{t: t}, aliveExec())
	inst.SetTmuxSession(ts)
	// Marked started, as a restored record whose session runs is.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")
	require.True(t, inst.Pane().TmuxAlive(), "fixture precondition: tmux session must read alive")
	return inst
}

// TestApplyLiveness_UnknownDoesNotPause is the regression guard for the
// 2026-08-21 false-dead cascade. Heavy load starved the `tmux has-session`
// probes; each timed-out probe read as death, and the health tick paused
// every running session at once — while tmux held them all alive. Because
// load hits every probe simultaneously, this failure is fleet-wide by
// construction, and each bogus pause then invited a resume that rebuilt a
// live worktree. An instance must never change state on a probe that
// never got an answer. (The TUI's half, that no client repair runs either,
// is app's TestHealthTick_UnknownLivenessRepairsNothing.)
func TestApplyLiveness_UnknownDoesNotPause(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)

	alive := m.applyLiveness(inst, tmux.LivenessUnknown, fromTick)

	assert.True(t, alive, "an inconclusive probe must leave the instance treated as running")
	assert.Equal(t, session.Running, inst.GetStatus(),
		"an instance must never be paused on a probe that got no answer")
}

// TestApplyLiveness_DeadStillPauses pins the other half: a probe that
// actually got a negative answer is still actionable evidence, so the
// pause path must keep working.
func TestApplyLiveness_DeadStillPauses(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)

	alive := m.applyLiveness(inst, tmux.LivenessDead, fromTick)

	assert.False(t, alive)
	assert.Equal(t, session.Paused, inst.GetStatus(),
		"an answered has-session failure must still pause the instance")
}

// TestApplyLiveness_ADroppedWorkspacesProbeIsIgnored: a probe taken before
// its workspace was dropped lands on an instance no workspace holds; a
// workspace-terminal restart there would relaunch one nothing displays.
func TestApplyLiveness_ADroppedWorkspacesProbeIsIgnored(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)
	m.SetWorkspacesForTest(NewWorkspace(WorkspaceParts{}), nil)

	assert.False(t, m.applyLiveness(inst, tmux.LivenessDead, fromTick))
	assert.Equal(t, session.Running, inst.GetStatus(), "untouched: no workspace holds it")
}

// TestHealthResult_ReportsTheLiveAndRearmsTheTick: the TUI repairs the
// clients of the live ones (Alive) and arms the next tick
// (HealthChecked), whatever the probe found. An inconclusive probe is not
// reported live: it must trigger no client repair either.
func TestHealthResult_ReportsTheLiveAndRearmsTheTick(t *testing.T) {
	m := NewForTest(Options{})
	live := probedRunning(t, m)
	unknown := newInst(t, "unknown")
	hold(m, unknown)

	m.Deliver(HealthResult{Results: []ProbeResult{
		{Instance: live, TmuxLive: tmux.LivenessAlive},
		{Instance: unknown, TmuxLive: tmux.LivenessUnknown},
	}})

	assert.Equal(t, []Event{
		StatusesChanged{},
		Alive{Instances: []*session.Instance{live}, Source: "tick"},
		HealthChecked{},
	}, m.Drain().Events)
}

// setupWorkspaceTerminalDead builds a Running workspace-terminal
// instance with a mock tmux session attached, for driving repeated
// dead-tmux health results against the restart circuit breaker without
// touching a real tmux server.
func setupWorkspaceTerminalDead(t *testing.T) (*Model, *session.Instance) {
	t.Helper()
	m := NewForTest(Options{})

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:               "ws-term",
		Path:                t.TempDir(),
		Program:             "broken-program",
		IsWorkspaceTerminal: true,
	})
	require.NoError(t, err)
	hold(m, inst)
	require.NoError(t, inst.TransitionTo(session.Running))

	ts := tmux.NewSessionWithDeps("ws-term", "broken-program", fakePtyFactory{t: t}, aliveExec())
	inst.SetTmuxSession(ts)

	return m, inst
}

// TestHealthResult_WorkspaceTerminalRestartLoop_TripsCircuitBreaker is
// the regression guard for the "prader-rs" incident: a workspace terminal
// whose Program was permanently broken died again on every Restart, and
// nothing ever stopped the tick loop from retrying forever at ~500ms
// cadence. After maxWorkspaceTerminalRestartFailures consecutive dead-tmux
// ticks, the instance must be marked Paused instead of restarted again.
func TestHealthResult_WorkspaceTerminalRestartLoop_TripsCircuitBreaker(t *testing.T) {
	m, inst := setupWorkspaceTerminalDead(t)

	for i := 0; i < maxWorkspaceTerminalRestartFailures-1; i++ {
		m.Deliver(HealthResult{Results: []ProbeResult{
			{Instance: inst, TmuxLive: tmux.LivenessDead},
		}})
		require.NotEqual(t, session.Paused, inst.GetStatus(),
			"must keep retrying below the failure threshold, not give up early")
	}

	m.Deliver(HealthResult{Results: []ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessDead},
	}})
	require.Equal(t, session.Paused, inst.GetStatus(),
		"circuit breaker should trip once consecutive failures reach the threshold")
}

// TestHealthResult_WorkspaceTerminalRestartFailures_ResetOnRecovery
// guards against a slower failure mode: if the counter never reset on a
// healthy tick, an instance that flakes occasionally over a long uptime
// would eventually trip the breaker from unrelated, non-consecutive misses.
func TestHealthResult_WorkspaceTerminalRestartFailures_ResetOnRecovery(t *testing.T) {
	m, inst := setupWorkspaceTerminalDead(t)

	m.Deliver(HealthResult{Results: []ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessDead},
	}})
	require.Equal(t, 1, inst.RestartFailureCount())

	m.Deliver(HealthResult{Results: []ProbeResult{
		{Instance: inst, TmuxLive: tmux.LivenessAlive},
	}})
	require.Equal(t, 0, inst.RestartFailureCount(),
		"a healthy tick must reset the consecutive-failure counter")
}

// TestHealthResult_ARestartedWorkspaceTerminalGetsAFreshClient: the
// relaunched session replaced the one any client watched, so the TUI
// replaces that client (SessionLaunched).
func TestHealthResult_ARestartedWorkspaceTerminalGetsAFreshClient(t *testing.T) {
	m := NewForTest(Options{})
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:               "ws-restart",
		Path:                t.TempDir(),
		Program:             "sh",
		IsWorkspaceTerminal: true,
	})
	require.NoError(t, err)
	hold(m, inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	// The session is gone until a new-session relaunches it.
	running := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			switch s := cmd.String(); {
			case strings.Contains(s, "has-session") && !running:
				return errors.New("no session")
			case strings.Contains(s, "new-session"):
				running = true
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps("ws-restart", "sh", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))

	m.Deliver(HealthResult{Results: []ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessDead}}})

	assert.Contains(t, m.Drain().Events, SessionLaunched{Instance: inst})
}

func TestDeadVerified_ALiveSessionIsReportedForRepair(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)

	job := m.VerifyDead(inst)
	verified, ok := job().(DeadVerified)
	require.True(t, ok)
	require.Equal(t, tmux.LivenessAlive, verified.TmuxLive)
	m.Deliver(verified)

	assert.Equal(t, session.Running, inst.GetStatus(), "a live session is not paused by a dead client")
	assert.Equal(t, []Event{
		StatusesChanged{},
		Alive{Instances: []*session.Instance{inst}, Source: "dead_event"},
		InstancesChanged{},
	}, m.Drain().Events)
}

func TestMarkOutputFeedsTheNextProbesDirtySet(t *testing.T) {
	m := NewForTest(Options{})
	m.MarkOutput("loom_a")

	assert.Equal(t, map[string]bool{"loom_a": true}, m.takeDirty())
	assert.Empty(t, m.takeDirty(), "each tick window starts clean")
}

// TestDeadVerified_AnInconclusiveProbeAsksForNoRepair: like the tick's, a
// Dead event's probe that got no answer leaves the instance alone and
// reports nothing live.
func TestDeadVerified_AnInconclusiveProbeAsksForNoRepair(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)

	m.Deliver(DeadVerified{Instance: inst, TmuxLive: tmux.LivenessUnknown})

	assert.Equal(t, session.Running, inst.GetStatus())
	assert.Equal(t, []Event{StatusesChanged{}, InstancesChanged{}}, m.Drain().Events)
}
