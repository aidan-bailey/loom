package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Output from a pane with no attached client promotes nothing: a pane's
// first Output of a burst is sent from its own goroutine and can arrive
// after the pane's Dead, which would show a dead pane's row "working" until
// the health tick. A pane with no client is no opinion, as for detection.
func TestPaneOutput_NoAttachedClientPromotesNothing(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "gone")
	m.ws().AddForTest(inst)
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()
	m.panes.Retain(nil) // its client was released
	require.False(t, m.panes.For(rowOf(t, m, inst)).Attached())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

	_, promoted := m.ladder[idOf(m, inst)]
	assert.False(t, promoted, "output with no attached client leaves the row as it is")
}

func TestPaneOutput_AnAttachedClientsOutputPromotes(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "busy")
	m.ws().AddForTest(inst)
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()
	require.True(t, m.panes.For(rowOf(t, m, inst)).Attached(), "fixture: the client is attached")

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

	require.Contains(t, m.ladder, idOf(m, inst))
	assert.Equal(t, session.Running, m.ladder[idOf(m, inst)].status)
}
