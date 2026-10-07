package app

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthTick_ProbeRoundTrip drives the model's half of the health tick
// through the TUI: the probe core.Model.Tick queues runs as a Cmd, its
// result lands through Update's handler and pauses the session it found
// gone, and HealthChecked, the event every probe ends with, re-arms the
// tick. tickUpdateMetadataCmd sleeps before it ticks (3s; 500ms on the
// snapshot path), so the re-arm is compared with it, never run. core's
// TestTick_TheProbeRoundTrip covers the dirty set and the diff refresh.
func TestHealthTick_ProbeRoundTrip(t *testing.T) {
	m := newTestHome(t)
	inst, err := session.NewInstance(session.InstanceOptions{Title: "probed", Path: t.TempDir(), Program: "aider"})
	require.NoError(t, err)
	m.ws.Add(inst)
	gone := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if gone && containsHasSession(cmd) {
				return errors.New("can't find session")
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps("probed", "aider", fakePtyFactory{t: t}, cmdExec))
	// Marked started, as a restored record whose session runs is.
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture precondition")
	// Classic mode polls GitHub for the cwd's repository: hold that poll
	// in flight, so the probe is the tick's only job and nothing runs git
	// or gh.
	m.core.SetGateForTest("github", true, time.Now())

	gone = true
	m.core.Tick(m.list.GetSelectedInstance())
	out := m.core.Drain()
	require.Len(t, out.Jobs, 1, "the probe is the tick's only job")
	msg, ok := coreCmd(out.Jobs[0])().(coreResultMsg)
	require.True(t, ok)
	require.IsType(t, core.HealthResult{}, msg.msg)

	// Update's handler delivers the result; the events it produced are then
	// applied one by one, as drainCore does, to pick out the re-arm.
	m.update(msg)
	assert.Equal(t, session.Paused, inst.GetStatus(), "the probe found the session gone")
	var rearm tea.Cmd
	checked := false
	for _, ev := range m.core.Drain().Events {
		cmd := m.applyCoreEvent(ev)
		if _, ok := ev.(core.HealthChecked); ok {
			rearm, checked = cmd, true
		}
	}
	require.True(t, checked, "every probe ends with HealthChecked")
	require.NotNil(t, rearm, "HealthChecked re-arms the tick")
	assert.Equal(t, reflect.ValueOf(tickUpdateMetadataCmd).Pointer(), reflect.ValueOf(rearm).Pointer(),
		"the re-arm is the tick's own Cmd")
}

// snapshotHome returns a home holding one started instance running
// program on the snapshot path: its pane client has no emulator.
func snapshotHome(t *testing.T, title, program string) (*home, *session.Instance) {
	t.Helper()
	inst := startedInstanceWithProgram(t, title, program, "$ ")
	m := homeWithAppState(t)
	m.ws.Add(inst)
	require.False(t, m.panes.For(inst).HasEmulator(), "fixture precondition: the snapshot path")
	return m, inst
}

// deliverScan lands one pane's snapshot scan, as a scan the tick started
// would, and checks the scan is over whatever it found.
func deliverScan(t *testing.T, m *home, r snapshotStatus) {
	t.Helper()
	m.snapshotScanning = true
	m.Update(snapshotStatusMsg{results: []snapshotStatus{r}})
	assert.False(t, m.snapshotScanning, "a landed scan is over")
}

// TestSnapshotStatus_Ladder: the snapshot path's ladder is the event
// path's. Still changing → Running, the output recorded for the next
// tick's diff refresh; settled with a prompt → Prompting; settled → Ready.
func TestSnapshotStatus_Ladder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		from   session.Status
		result snapshotStatus
		want   session.Status
		marked bool
	}{
		{"output", session.Ready, snapshotStatus{updated: true}, session.Running, true},
		{"prompt", session.Running, snapshotStatus{hasPrompt: true}, session.Prompting, false},
		{"settled", session.Running, snapshotStatus{}, session.Ready, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, inst := snapshotHome(t, "ladder", "bash")
			require.NoError(t, inst.TransitionTo(tc.from))
			tc.result.instance = inst

			deliverScan(t, m, tc.result)

			assert.Equal(t, tc.want, inst.GetStatus())
			assert.Equal(t, tc.marked, m.core.OutputMarkedForTest(inst.Pane().TmuxSessionName()))
		})
	}
}

// TestSnapshotStatus_AFailedCaptureIsNoOpinion: a failed capture leaves
// the status alone, as on the event path. Read as "settled, no prompt",
// its zero result moved a dead session to Ready before the probe paused
// it.
func TestSnapshotStatus_AFailedCaptureIsNoOpinion(t *testing.T) {
	m, inst := snapshotHome(t, "capture-failed", "bash")

	deliverScan(t, m, snapshotStatus{instance: inst, err: errors.New("can't find pane")})

	assert.Equal(t, session.Running, inst.GetStatus())
	assert.False(t, m.core.OutputMarkedForTest(inst.Pane().TmuxSessionName()))
}

// TestSnapshotStatus_AReportedClaudeStatusIsTheModels: the model applies
// Claude's reported status (its tick, hook scans and roster answers), so
// the ladder leaves the instance alone. The output still refreshes the
// diff, as a pane event's does.
func TestSnapshotStatus_AReportedClaudeStatusIsTheModels(t *testing.T) {
	m, inst := snapshotHome(t, "reported", "claude")
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})
	require.NoError(t, inst.TransitionTo(session.Ready))

	deliverScan(t, m, snapshotStatus{instance: inst, updated: true})

	assert.Equal(t, session.Ready, inst.GetStatus(),
		"neither the ladder's Running nor the reported Prompting: the model applies the report")
	assert.Equal(t, "permission: Bash", inst.WaitReason(), "the report was adopted")
	assert.True(t, m.core.OutputMarkedForTest(inst.Pane().TmuxSessionName()),
		"output refreshes the diff whoever reports the status")
}

// TestSnapshotStatus_AnIneligibleInstanceIsSkipped: a scan landing after
// the instance was paused moves nothing and records no output.
func TestSnapshotStatus_AnIneligibleInstanceIsSkipped(t *testing.T) {
	m, inst := snapshotHome(t, "paused", "bash")
	require.NoError(t, inst.TransitionTo(session.Paused))

	deliverScan(t, m, snapshotStatus{instance: inst, updated: true})

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.False(t, m.core.OutputMarkedForTest(inst.Pane().TmuxSessionName()))
}

// TestSnapshotScan_OneAtATime: the tick scans every pane whose client has
// no emulator, and starts no scan while one is in flight; the result runs
// the ladder and lets the next tick scan again.
func TestSnapshotScan_OneAtATime(t *testing.T) {
	m, inst := snapshotHome(t, "scan", "bash")
	require.NoError(t, inst.TransitionTo(session.Ready))

	scan := m.snapshotScan()
	require.NotNil(t, scan)
	assert.True(t, m.snapshotScanning)
	assert.Nil(t, m.snapshotScan(), "one scan at a time")

	msg, ok := scan().(snapshotStatusMsg)
	require.True(t, ok)
	require.Len(t, msg.results, 1)
	require.Same(t, inst, msg.results[0].instance)
	require.NoError(t, msg.results[0].err)
	require.True(t, msg.results[0].updated, "the first sample hashes new content")
	m.Update(msg)

	assert.False(t, m.snapshotScanning)
	assert.Equal(t, session.Running, inst.GetStatus())
	assert.True(t, m.core.OutputMarkedForTest(inst.Pane().TmuxSessionName()))
	assert.NotNil(t, m.snapshotScan(), "the next tick scans again")
}

// TestSnapshotScan_NothingToScan: no scan without a pane to scan: no
// active instance, a pane with no client (no screen, so no opinion), or a
// client with an emulator, whose status rides pane events instead.
func TestSnapshotScan_NothingToScan(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		m := homeWithAppState(t)
		assert.Nil(t, m.snapshotScan())
		assert.False(t, m.snapshotScanning)
	})
	t.Run("no client", func(t *testing.T) {
		m, _ := setupPtmxDeadFixture(t)
		assert.Nil(t, m.snapshotScan())
		assert.False(t, m.snapshotScanning)
	})
	t.Run("emulator", func(t *testing.T) {
		m, inst := snapshotHome(t, "emulated", "bash")
		clientOf(t, inst).SetEmulatorForTest(vt.NewXVT(80, 24))
		assert.Nil(t, m.snapshotScan())
		assert.False(t, m.snapshotScanning)
	})
}
