package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliverRoster lands a roster answer the way a finished query does,
// stamped now.
func deliverRoster(m *home, entries map[string]session.RosterEntry) {
	m.Update(coreResultMsg{msg: core.RosterResultForTest(entries, nil, time.Now())})
}

// failRoster lands a failed roster query.
func failRoster(m *home) {
	m.Update(coreResultMsg{msg: core.RosterResultForTest(nil, errAssertRoster, time.Now())})
}

// applyClaudeStatus moves inst to the status its hooks or the roster last
// reported, as the model does when a hook scan or roster answer lands.
func applyClaudeStatus(m *home, inst *session.Instance) {
	if target, ok := m.core.AdoptClaudeStatus(inst); ok {
		_ = inst.TransitionTo(target)
	}
}

// applyHookEvents feeds events to inst as a replayed scan would. The test
// instances start on a mock tmux session, so their launch prepared no
// hooks folder: like an instance restored after a loom restart, they have
// no launch ID yet and adopt the result's.
func applyHookEvents(t *testing.T, inst *session.Instance, events ...hooks.Event) {
	t.Helper()
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: events}))
}

// Claude's report owns the status: output alone (a repaint on a focus
// change) must not promote a reported Ready session to Running.
func TestReportedReadyNotPromotedByOutput(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reported-ready", "claude", "x")
	m := homeWithAppState(t)
	m.ws.Add(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now()})
	applyClaudeStatus(m, inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// A reported status also retires the re-detection chain: the ladder
// re-samples only because one content hash cannot tell "still working"
// from "just finished", and Claude's report says which it is.
func TestReportedStatusSuppressesRedetect(t *testing.T) {
	inst := startedInstanceWithProgram(t, "hook-redetect", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")
	m := homeWithAppState(t)
	m.ws.Add(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	assert.Nil(t, follow)
}
