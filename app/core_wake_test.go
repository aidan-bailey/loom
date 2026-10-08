package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/core"
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
	assert.Equal(t, idOf(m, inst), testModel(m).SelectedForTest())
}

// TestRealLoop_AJobsResultReachesTheTUIByWake: on a production loop, a
// request's job runs on a goroutine of its own, its result lands on the
// loop, and the loop's wake brings it to the TUI, with no test-driven
// delivery. Run it under -race.
func TestRealLoop_AJobsResultReachesTheTUIByWake(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m) // never started: the kill's job fails (no worktree)
	m.errBox.SetSize(400, 1)
	m.syncViews()
	held := loopOf(m)
	model := held.ModelForTest()
	held.Stop()
	l := core.Start(model)
	t.Cleanup(l.Stop)
	m.core, m.wakes = l, l.Wakes()
	m.aliveProbe = func(string) bool { return true } // the TUI's probe must not read the model's instances while its loop runs

	_, _ = runKillSelectedNoConfirm(m)
	select {
	case <-m.wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("the kill's result never woke the TUI")
	}
	_, _ = m.Update(coreWakeMsg{})
	require.NotNil(t, m.errBox)
	assert.NotEmpty(t, m.errBox.String(), "the model's notice of the failed kill reached the error bar")
}
