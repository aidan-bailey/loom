package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureSlotPanes_AttachesActiveInstances: a workspace load attaches a
// client to every active session of the slot, and none to a paused one.
func TestEnsureSlotPanes_AttachesActiveInstances(t *testing.T) {
	m := newTestHome(t)
	live := liveInstance(t, "a-live")
	paused := liveInstance(t, "a-paused")
	require.NoError(t, paused.TransitionTo(session.Paused))
	m.list.AddInstance(live)
	m.list.AddInstance(paused)
	m.panes.Retain(nil) // the fixtures came with clients; start from none

	m.ensureSlotPanes(m.workspaceSlot)

	assert.True(t, m.panes.Alive(live.Pane().TmuxSessionName()))
	assert.Nil(t, m.panes.Get(paused.Pane().TmuxSessionName()), "a paused session gets no client")
}

// TestReplacePane_ClosesTheOldClientOffUpdate: a relaunched session gets a
// fresh client, and the old one is closed by the returned Cmd, not on the
// Update goroutine.
func TestReplacePane_ClosesTheOldClientOffUpdate(t *testing.T) {
	m := newTestHome(t)
	inst := liveInstance(t, "relaunched")
	m.list.AddInstance(inst)
	old := clientOf(t, inst)

	cmd := m.replacePane(inst)

	fresh := m.panes.Get(inst.Pane().TmuxSessionName())
	require.NotNil(t, fresh)
	assert.NotSame(t, old, fresh)
	assert.True(t, fresh.PtmxAlive())
	assert.True(t, old.PtmxAlive(), "closing it waits for the Cmd")
	drainCmd(cmd)
	assert.False(t, old.PtmxAlive())
}

// TestFullScreenAttach_PausesAndRestoresThePaneClient: the foreground
// attach takes the session from the client and gives it back.
func TestFullScreenAttach_PausesAndRestoresThePaneClient(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "fs")
	m.list.AddInstance(inst)
	name := inst.Pane().TmuxSessionName()
	require.True(t, m.panes.Alive(name))

	_, cmd := m.Update(startFullScreenAttachMsg{instance: inst, target: attachTargetAgent})
	require.NotNil(t, cmd, "the foreground attach runs as an ExecProcess")
	assert.False(t, m.panes.Alive(name), "the client lets go of the session for the foreground attach")
	assert.Same(t, inst, m.attachingInstance)

	_, _ = m.Update(attachDoneMsg{instance: inst})
	assert.True(t, m.panes.Alive(name), "and re-attaches when it returns")
	assert.Nil(t, m.attachingInstance)
}
