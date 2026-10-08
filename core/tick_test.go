package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
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

// tickedInst builds a started, Running instance titled title, held by m's
// classic workspace, on a mock tmux session (no server contacted) whose
// has-session answers alive until *gone is set; gone may be nil. It runs
// no Claude program, so the tick queues no roster query or hook scan for
// it, and it has no worktree, so a diff refresh runs no git.
func tickedInst(t *testing.T, m *Model, title string, gone *bool) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "aider"})
	require.NoError(t, err)
	hold(m, inst)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if gone != nil && *gone && strings.Contains(cmd.String(), "has-session") {
				return errors.New("can't find session")
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, "aider", fakePtyFactory{t: t}, cmdExec))
	// Marked started, as a restored record whose session runs is.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")
	return inst
}

// TestTick_TheProbeRoundTrip: the tick takes the dirty set MarkOutput
// filled and hands it to one probe of the active instances. Delivered,
// the probe's result pauses the session it found gone, keeps the live
// ones, refreshes the diff of the one with output only, and ends with
// HealthChecked, the event that says the loop re-armed its tick (app's
// TestHealthTick_ProbeRoundTrip applies it).
func TestTick_TheProbeRoundTrip(t *testing.T) {
	m := NewForTest(Options{})
	gone := false
	busy := tickedInst(t, m, "busy", nil)
	idle := tickedInst(t, m, "idle", nil)
	dead := tickedInst(t, m, "dead", &gone)
	// Classic mode polls GitHub for the cwd's repository: hold that poll
	// in flight, so the probe is the tick's only job and nothing runs git
	// or gh.
	m.SetGateForTest("github", true, time.Now())
	probe := func() HealthResult {
		t.Helper()
		m.tickInst(nil)
		out := m.Drain()
		require.Len(t, out.Jobs, 1, "the probe is the tick's only job")
		r, ok := out.Jobs[0]().(HealthResult)
		require.True(t, ok, "the job is the health probe")
		return r
	}

	// A first probe caches every diff, so the next refreshes one only for
	// output (no instance is selected, so none wants a full diff).
	m.Deliver(probe())
	m.Drain()
	busyDiff, idleDiff := busy.GetDiffStats(), idle.GetDiffStats()
	require.NotNil(t, busyDiff, "fixture precondition: the first probe cached the diff")
	require.NotNil(t, idleDiff, "fixture precondition: the first probe cached the diff")

	gone = true
	m.MarkOutput(busy.Pane().TmuxSessionName())
	r := probe()
	assert.Empty(t, m.takeDirty(), "the tick took the dirty set for its probe")
	m.Deliver(r)

	assert.Equal(t, session.Paused, dead.GetStatus(), "a session the probe found gone is paused")
	assert.Equal(t, session.Running, busy.GetStatus())
	assert.Equal(t, session.Running, idle.GetStatus())
	assert.NotSame(t, busyDiff, busy.GetDiffStats(), "output refreshes the diff")
	assert.Same(t, idleDiff, idle.GetDiffStats(), "no output, no refresh")
	assert.Equal(t, []Event{
		StatusesChanged{},
		Alive{IDs: []InstanceID{m.idOf(busy), m.idOf(idle)}, Source: "tick"},
		HealthChecked{},
	}, m.Drain().Events)
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
	m.SetWorkspacesForTest(NewWorkspace(WorkspaceParts{}))

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
		Alive{IDs: []InstanceID{m.idOf(live)}, Source: "tick"},
		HealthChecked{},
	}, m.Drain().Events)
}

// TestHealthResult_LeavesAnInstanceAnOpTookWhileTheProbeRan: a kill
// (Deleting) or a pause or resume (Loading) confirmed while a probe was in
// flight owns the instance by the time the probe lands. Neither the
// probe's answer nor the hooks' reported status may move it: a Ready
// would reopen the busy gate to a second kill or pause, and keep a record
// whose worktree is going away persistable. A dead answer must not pause
// it under the op, and an alive one asks for no client repair.
func TestHealthResult_LeavesAnInstanceAnOpTookWhileTheProbeRan(t *testing.T) {
	for _, held := range []session.Status{session.Deleting, session.Loading} {
		t.Run(held.String(), func(t *testing.T) {
			m := NewForTest(Options{})
			inst := activeInst(t, m, "probed")
			applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now()})
			require.NoError(t, inst.TransitionTo(held), "the op confirmed while the probe ran")
			target, authoritative := m.adoptClaudeStatus(inst)
			require.True(t, authoritative, "fixture precondition: the hooks reported a status")
			require.Equal(t, session.Ready, target, "fixture precondition")

			m.Deliver(HealthResult{Results: []ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessAlive}}})

			assert.Equal(t, held, inst.GetStatus(), "the op owns the instance: the reported status must not move it")
			assert.Equal(t, []Event{
				StatusesChanged{},
				Alive{Source: "tick"},
				HealthChecked{},
			}, m.Drain().Events, "no client repair for an instance the op owns")

			m.Deliver(HealthResult{Results: []ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessDead}}})

			assert.Equal(t, held, inst.GetStatus(), "a dead answer must not pause it under the op")
		})
	}
}

// setupWorkspaceTerminalDead builds a started, Running workspace-terminal
// instance with a mock tmux session attached, for driving repeated
// dead-tmux health results against the restart circuit breaker without
// touching a real tmux server. The mock answers as a server would:
// has-session finds the session from new-session until kill-session, so
// each Restart (a Close, then a Start) relaunches it.
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

	running := true
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			switch s := cmd.String(); {
			case strings.Contains(s, "has-session") && !running:
				return errors.New("no session")
			case strings.Contains(s, "new-session"):
				running = true
			case strings.Contains(s, "kill-session"):
				running = false
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps("ws-term", "broken-program", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
	// Marked started, as a restored record whose session runs is: the
	// probe only probes started instances.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")

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
	// Marked started, as a restored record whose session ran is: the
	// probe only probes started instances.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")

	m.Deliver(HealthResult{Results: []ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessDead}}})

	assert.Contains(t, m.Drain().Events, SessionLaunched{ID: m.idOf(inst)})
}

func TestDeadVerified_ALiveSessionIsReportedForRepair(t *testing.T) {
	m := NewForTest(Options{})
	inst := probedRunning(t, m)

	job := m.verifyDeadInst(inst)
	verified, ok := job().(DeadVerified)
	require.True(t, ok)
	require.Equal(t, tmux.LivenessAlive, verified.TmuxLive)
	m.Deliver(verified)

	assert.Equal(t, session.Running, inst.GetStatus(), "a live session is not paused by a dead client")
	assert.Equal(t, []Event{
		StatusesChanged{},
		Alive{IDs: []InstanceID{m.idOf(inst)}, Source: "dead_event"},
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

// Several clients select a row each: the probe refreshes the full diff of
// every selected instance a loaded workspace holds, not only the last one
// named.
func TestTick_EverySelectedInstanceIsProbedInFull(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	a, b := newInst(t, "a"), newInst(t, "b")
	ws.add(a)
	ws.add(b)
	m.SetWorkspacesForTest(ws)

	m.SetSelection([]InstanceID{m.idOf(a), 99, m.idOf(b)})
	assert.Equal(t, []*session.Instance{a, b}, m.selectedInstances(), "an unknown ID is skipped")

	m.SetSelected(m.idOf(b))
	assert.Equal(t, []*session.Instance{b}, m.selectedInstances(), "SetSelected names one")
	m.SetSelected(0)
	assert.Empty(t, m.selectedInstances(), "0 selects none")
}
