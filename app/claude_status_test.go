package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliverRoster lands a roster answer the way a finished query does,
// stamped now.
func deliverRoster(m *home, entries map[string]session.RosterEntry) {
	m.Update(rosterReadyMsg{entries: entries, at: time.Now()})
}

// failRoster lands a failed roster query.
func failRoster(m *home) {
	m.Update(rosterReadyMsg{err: errAssertRoster, at: time.Now()})
}

// applyHookEvents feeds events to inst as a replayed scan would. The test
// instances start on a mock tmux session, so their launch prepared no
// hooks folder: like an instance restored after a loom restart, they have
// no launch ID yet and adopt the result's.
func applyHookEvents(t *testing.T, inst *session.Instance, events ...hooks.Event) {
	t.Helper()
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: events}))
}

// A query that started before a Stop and landed after it must not undo
// the Stop: its busy is older than the hook's Ready.
func TestRosterAnswerOlderThanHookEventIsDropped(t *testing.T) {
	inst := startedInstanceWithProgram(t, "stale-roster", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	queryStarted := time.Now()
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: queryStarted.Add(100 * time.Millisecond)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Update(rosterReadyMsg{entries: rosterFor(inst, session.RosterStatusBusy), at: queryStarted})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// A lead that stops while waiting for a teammate's reply, then picks it
// up with no hook event, is corrected by the next roster answer.
func TestNewerRosterAnswerCorrectsIntermediateStop(t *testing.T) {
	inst := startedInstanceWithProgram(t, "mid-stop", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	deliverRoster(m, rosterFor(inst, session.RosterStatusBusy))

	assert.Equal(t, session.Running, inst.GetStatus())
}

func TestFailedRosterKeepsHookStatus(t *testing.T) {
	inst := startedInstanceWithProgram(t, "hook-survives", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)

	failRoster(m)

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
}

// Claude's report owns the status: output alone (a repaint on a focus
// change) must not promote a reported Ready session to Running.
func TestReportedReadyNotPromotedByOutput(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reported-ready", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now()})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// Answering a prompt makes output but fires no hook, so output on a
// Prompting session asks the roster, no more often than
// promptingRosterSpacing.
func TestPromptingOutputQueriesRosterSpaced(t *testing.T) {
	inst := startedInstanceWithProgram(t, "prompt-roster", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Prompting))

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.True(t, m.gate(gateRoster).inFlight, "output on a Prompting session queries the roster")

	m.gate(gateRoster).inFlight = false
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.False(t, m.gate(gateRoster).inFlight, "a second query inside promptingRosterSpacing is not dispatched")

	m.gate(gateRoster).last = time.Now().Add(-promptingRosterSpacing)
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateRoster).inFlight)
}

// A reported status also retires the re-detection chain: the ladder
// re-samples only because one content hash cannot tell "still working"
// from "just finished", and Claude's report says which it is.
func TestReportedStatusSuppressesRedetect(t *testing.T) {
	inst := startedInstanceWithProgram(t, "hook-redetect", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	assert.Nil(t, follow)
}
