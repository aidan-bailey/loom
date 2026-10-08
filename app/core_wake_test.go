package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardWakes_OneMessagePerWakeUntilTheLoopStops: each wake reaches
// the program as a coreWakeMsg, and the forwarder ends when its channel
// closes.
func TestForwardWakes_OneMessagePerWakeUntilTheLoopStops(t *testing.T) {
	wakes := make(chan struct{}, 1)
	var got []tea.Msg
	done := make(chan struct{})
	go func() {
		forwardWakes(wakes, func(msg tea.Msg) { got = append(got, msg) })
		close(done)
	}()
	wakes <- struct{}{}
	wakes <- struct{}{}
	close(wakes)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardWakes did not end when its channel closed")
	}
	assert.Equal(t, []tea.Msg{coreWakeMsg{}, coreWakeMsg{}}, got)
}

// tickRearmed stands in for the TUI tick's next firing.
type tickRearmed struct{}

// TestTUITick_RearmsItselfAndTicksNoModel: the TUI's tick re-arms itself
// and leaves the model's half to the model's loop. The tick's Cmd is
// swapped for one that answers at once: tea.Batch hands a lone Cmd back
// as itself, so running the real one would sleep a whole period.
func TestTUITick_RearmsItselfAndTicksNoModel(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)
	m.syncViews()
	prev := tickUpdateMetadataCmd
	tickUpdateMetadataCmd = func() tea.Msg { return tickRearmed{} }
	t.Cleanup(func() { tickUpdateMetadataCmd = prev })

	_, cmd := m.Update(tickUpdateMetadataMessage{})
	assert.Contains(t, runCmds(t, cmd), tea.Msg(tickRearmed{}), "the TUI's tick re-arms itself")
	assert.Empty(t, loopOf(m).JobsForTest(), "the TUI's tick starts no probe: the model ticks on its own loop")
}

// TestSelection_ReachesTheModel: Update publishes the selected row to the
// model, whose probe refreshes its full diff.
func TestSelection_ReachesTheModel(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	m.syncViews()
	m.Update(coreWakeMsg{})
	assert.Equal(t, []core.InstanceID{idOf(m, inst)}, testModel(m).SelectedForTest())
}

// TestRealLoop_AJobsResultReachesTheTUIByWake: on the production stack
// (rpc.InProcess), a request's job runs on a goroutine of its own, its
// result lands on the loop, the server publishes on the loop's wake, and
// the client's wake brings it to the TUI, with no test-driven delivery.
// The kill request's own publish wakes the TUI first (its Deleting), so the
// test waits for the wake that carries the failure, not the first one. Run
// it under -race.
func TestRealLoop_AJobsResultReachesTheTUIByWake(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m) // never started: the kill's job fails (no worktree)
	m.errBox.SetSize(400, 1)
	m.syncViews()
	model := testModel(m)
	stackOf(m).stop()
	testStacks.Delete(m.core)
	c, stop, err := rpc.InProcess(model)
	require.NoError(t, err)
	t.Cleanup(stop)
	m.core, m.wakes = c, c.Wakes()
	m.aliveProbe = func(string) bool { return true } // the TUI's probe must not read the model's instances while its loop runs

	_, _ = runKillSelectedNoConfirm(m)
	// The first wake is the request's own publish (the row's Deleting), long
	// before the job has run; wait for the wake that brings the failure.
	const notice = "cannot get git worktree"
	deadline := time.After(10 * time.Second)
	for !strings.Contains(m.errBox.String(), notice) {
		select {
		case <-m.wakes:
			_, _ = m.Update(coreWakeMsg{})
		case <-deadline:
			t.Fatalf("the kill's failure never reached the error bar; it holds %q", m.errBox.String())
		}
	}
}
