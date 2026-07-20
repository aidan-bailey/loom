package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/require"
)

// setupWorkspaceTerminalDeadFixture builds a Running workspace-terminal
// instance with a mock tmux session attached, for driving repeated
// metadataReadyMsg{tmuxAlive:false} ticks against the restart circuit
// breaker without touching a real tmux server.
func setupWorkspaceTerminalDeadFixture(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:               "ws-term",
		Path:                t.TempDir(),
		Program:             "broken-program",
		IsWorkspaceTerminal: true,
	})
	require.NoError(t, err)
	m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))

	ts := tmux.NewTmuxSessionWithDeps("ws-term", "broken-program", fakePtyFactory{t: t}, aliveCmdExecForTest())
	inst.SetTmuxSession(ts)

	return m, inst
}

// TestMetadataReadyMsg_WorkspaceTerminalRestartLoop_TripsCircuitBreaker is
// the regression guard for the "prader-rs" incident: a workspace terminal
// whose Program was permanently broken died again on every Restart, and
// nothing ever stopped the tick loop from retrying forever at ~500ms
// cadence. After maxWorkspaceTerminalRestartFailures consecutive dead-tmux
// ticks, the instance must be marked Paused instead of restarted again.
func TestMetadataReadyMsg_WorkspaceTerminalRestartLoop_TripsCircuitBreaker(t *testing.T) {
	m, inst := setupWorkspaceTerminalDeadFixture(t)

	for i := 0; i < maxWorkspaceTerminalRestartFailures-1; i++ {
		_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
			{instance: inst, tmuxAlive: false},
		}})
		require.NotEqual(t, session.Paused, inst.GetStatus(),
			"must keep retrying below the failure threshold, not give up early")
	}

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxAlive: false},
	}})
	require.Equal(t, session.Paused, inst.GetStatus(),
		"circuit breaker should trip once consecutive failures reach the threshold")
}

// TestMetadataReadyMsg_WorkspaceTerminalRestartFailures_ResetOnRecovery
// guards against a slower failure mode: if the counter never reset on a
// healthy tick, an instance that flakes occasionally over a long uptime
// would eventually trip the breaker from unrelated, non-consecutive misses.
func TestMetadataReadyMsg_WorkspaceTerminalRestartFailures_ResetOnRecovery(t *testing.T) {
	m, inst := setupWorkspaceTerminalDeadFixture(t)

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxAlive: false},
	}})
	require.Equal(t, 1, inst.RestartFailureCount())

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxAlive: true, ptmxAlive: true},
	}})
	require.Equal(t, 0, inst.RestartFailureCount(),
		"a healthy tick must reset the consecutive-failure counter")
}
