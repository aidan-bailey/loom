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
	m.ws().AddForTest(inst)
	m.syncViews()
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
	testModel(m).SetGateForTest("github", true, time.Now())

	gone = true
	m.syncViews()
	m.core.Tick(m.list.GetSelectedInstance().ID)
	out := testModel(m).Drain()
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
	for _, ev := range testModel(m).Drain().Events {
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
	m.ws().AddForTest(inst)
	require.False(t, m.panes.For(rowOf(t, m, inst)).HasEmulator(), "fixture precondition: the snapshot path")
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
			m.syncViews()
			tc.result.id, tc.result.title = idOf(m, inst), inst.Title

			deliverScan(t, m, tc.result)

			assert.Equal(t, tc.want, shownStatus(t, m, inst), "the row shows the ladder's status")
			assert.Equal(t, tc.from, inst.GetStatus(), "the ladder is the TUI's overlay: the model's status is unchanged")
			assert.Equal(t, tc.marked, testModel(m).OutputMarkedForTest(inst.Pane().TmuxSessionName()))
		})
	}
}

// TestLadder_SurvivesAFailedPauseOrKill: a pause or kill moves the row to
// Loading or Deleting, and one that fails reverts the model to the
// session's previous status: Running, for a non-Claude session. The
// ladder's status must still be there then. With the client attached
// nothing scrapes an idle pane again, so a pruned entry left the row
// showing Running where it showed Ready (and a Prompting one without its
// badge) until its next output.
func TestLadder_SurvivesAFailedPauseOrKill(t *testing.T) {
	for _, op := range []struct {
		name    string
		request func(m *home, id core.InstanceID)
		busy    session.Status
	}{
		{"pause", func(m *home, id core.InstanceID) { m.core.Pause(id, 0) }, session.Loading},
		{"kill", func(m *home, id core.InstanceID) { m.core.Kill(id, 0) }, session.Deleting},
	} {
		t.Run(op.name, func(t *testing.T) {
			m, inst := snapshotHome(t, "ladder", "bash")
			require.NoError(t, inst.TransitionTo(session.Running))
			m.syncViews()
			id := idOf(m, inst)
			deliverScan(t, m, snapshotStatus{id: id, title: inst.Title})
			require.Equal(t, session.Ready, shownStatus(t, m, inst), "fixture: the ladder shows the idle pane Ready")

			op.request(m, id)
			_ = requestJob(t, m) // the drain: the busy row's views reach the TUI
			require.Equal(t, op.busy, shownStatus(t, m, inst), "the busy row shows the model's status")

			deliver(t, m, core.OpFailed{Instance: inst, Title: inst.Title, Op: op.name, Previous: session.Running, Err: errors.New("boom")})

			assert.Equal(t, session.Running, inst.GetStatus(), "the model reverted to Running")
			assert.Equal(t, session.Ready, shownStatus(t, m, inst), "and the row shows the ladder's Ready again")
		})
	}
}

// TestSnapshotStatus_AFailedCaptureIsNoOpinion: a failed capture leaves
// the status alone, as on the event path. Read as "settled, no prompt",
// its zero result moved a dead session to Ready before the probe paused
// it.
func TestSnapshotStatus_AFailedCaptureIsNoOpinion(t *testing.T) {
	m, inst := snapshotHome(t, "capture-failed", "bash")

	deliverScan(t, m, snapshotStatus{id: idOf(m, inst), title: inst.Title, err: errors.New("can't find pane")})

	assert.Equal(t, session.Running, inst.GetStatus())
	assert.False(t, testModel(m).OutputMarkedForTest(inst.Pane().TmuxSessionName()))
}

// TestSnapshotStatus_AReportedClaudeStatusIsTheModels: the model applies
// Claude's reported status (its tick, hook scans and roster answers), so
// the ladder leaves the instance alone. The output still refreshes the
// diff, as a pane event's does.
func TestSnapshotStatus_AReportedClaudeStatusIsTheModels(t *testing.T) {
	m, inst := snapshotHome(t, "reported", "claude")
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()

	deliverScan(t, m, snapshotStatus{id: idOf(m, inst), title: inst.Title, updated: true})

	assert.Equal(t, session.Ready, inst.GetStatus(),
		"neither the ladder's Running nor the reported Prompting: the model applies the report")
	assert.NotContains(t, m.ladder, idOf(m, inst), "the ladder records nothing for a reported status")
	assert.True(t, testModel(m).OutputMarkedForTest(inst.Pane().TmuxSessionName()),
		"output refreshes the diff whoever reports the status")

	// The model adopts the report on its tick (deliverHealth), the TUI's
	// scan no longer does.
	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessAlive}}})
	assert.Equal(t, "permission: Bash", inst.WaitReason(), "the report was adopted")
}

// TestSnapshotStatus_AnIneligibleInstanceIsSkipped: a scan landing after
// the instance was paused moves nothing and records no output.
func TestSnapshotStatus_AnIneligibleInstanceIsSkipped(t *testing.T) {
	m, inst := snapshotHome(t, "paused", "bash")
	require.NoError(t, inst.TransitionTo(session.Paused))
	m.syncViews()

	deliverScan(t, m, snapshotStatus{id: idOf(m, inst), title: inst.Title, updated: true})

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.False(t, testModel(m).OutputMarkedForTest(inst.Pane().TmuxSessionName()))
}

// TestSnapshotScan_OneAtATime: the tick scans every pane whose client has
// no emulator, and starts no scan while one is in flight; the result runs
// the ladder and lets the next tick scan again.
func TestSnapshotScan_OneAtATime(t *testing.T) {
	m, inst := snapshotHome(t, "scan", "bash")
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()

	scan := m.snapshotScan()
	require.NotNil(t, scan)
	assert.True(t, m.snapshotScanning)
	assert.Nil(t, m.snapshotScan(), "one scan at a time")

	msg, ok := scan().(snapshotStatusMsg)
	require.True(t, ok)
	require.Len(t, msg.results, 1)
	require.Equal(t, idOf(m, inst), msg.results[0].id)
	require.NoError(t, msg.results[0].err)
	require.True(t, msg.results[0].updated, "the first sample hashes new content")
	m.Update(msg)

	assert.False(t, m.snapshotScanning)
	assert.Equal(t, session.Running, shownStatus(t, m, inst))
	assert.True(t, testModel(m).OutputMarkedForTest(inst.Pane().TmuxSessionName()))
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
