package app

import (
	"context"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveInstance builds a started, Running instance whose tmux session has a
// preview attach client (a fake PTY), as LoadAndReconcile leaves a live
// session. No tmux server is contacted.
func liveInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	// Paused data comes back started; its session is swapped for a
	// fake-PTY one and the instance moved to Running.
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewTmuxSessionWithDeps(title, "claude", fakePtyFactory{t: t}, aliveCmdExecForTest()))
	require.NoError(t, inst.TransitionTo(session.Running))
	require.NoError(t, inst.RepairPtmx())
	require.True(t, inst.PtmxAlive(), "fixture: the preview PTY is attached")
	return inst
}

// drainCmd runs cmd and, recursively, every Cmd of a tea.BatchMsg it
// produces — off the Update goroutine, as the Bubble Tea runtime would —
// discarding the messages.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmd(c)
		}
	}
}

// referencedInstances lists every instance the model can still reach.
func referencedInstances(m *home) []*session.Instance {
	var out []*session.Instance
	for _, s := range m.openSlots() {
		out = append(out, s.list.GetInstances()...)
	}
	out = append(out, m.splitPane.Instance(), m.menu.Instance(),
		m.attachingInstance, m.pendingAttachTarget, m.pendingMergeTarget)
	return append(out, m.pendingMergeSourceItems...)
}

// pointAt makes inst the selection the panes and menu render, as
// instanceChanged would (without its terminal-pane tmux side effects).
func pointAt(m *home, inst *session.Instance) {
	m.list.SetSelectedInstance(slices.Index(m.list.GetInstances(), inst))
	m.splitPane.SetInstance(inst)
	m.menu.SetInstance(inst)
}

// assertReleased checks that dropped instances keep their preview PTY
// until the returned Cmd runs, lose it once it has, and are no longer
// reachable from the model.
func assertReleased(t *testing.T, m *home, cmd tea.Cmd, dropped ...*session.Instance) {
	t.Helper()
	for _, inst := range dropped {
		assert.True(t, inst.PtmxAlive(), "%s: released on the Update goroutine; must wait for the Cmd", inst.Title)
	}
	drainCmd(cmd)
	refs := referencedInstances(m)
	for _, inst := range dropped {
		assert.False(t, inst.PtmxAlive(), "%s: preview PTY still attached after the drop", inst.Title)
		assert.NotContains(t, refs, inst, "%s: still reachable from the model", inst.Title)
	}
	require.NoError(t, m.checkSlotInvariant())
}

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// TestDroppedSlot_ReleasesPreviewPTYs: dropping a slot used to leave each
// of its live instances' tmux attach client (PTY, pump, emulator) running,
// so reopening the workspace attached a second client to every live
// session. Every drop site must hand back a Cmd that releases them.
func TestDroppedSlot_ReleasesPreviewPTYs(t *testing.T) {
	isolateTmux(t)

	t.Run("closing a background tab", func(t *testing.T) {
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "b-live")
		m.slots[1].list.AddInstance(live)

		cmd := m.applyWorkspaceToggle([]config.Workspace{{Name: "afocus"}})
		require.Equal(t, []string{"afocus"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("closing the focused tab", func(t *testing.T) {
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "a-live")
		m.list.AddInstance(live)
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}})
		require.Equal(t, []string{"bpeer"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("entering global mode drops every tab", func(t *testing.T) {
		t.Setenv("LOOM_HOME", t.TempDir())
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		focused, peer := liveInstance(t, "a-live"), liveInstance(t, "b-live")
		m.list.AddInstance(focused)
		m.slots[1].list.AddInstance(peer)
		pointAt(m, focused) // the carried-over splitPane must let go of it

		cmd := m.applyWorkspaceToggle(nil)
		require.Empty(t, m.slots)
		assertReleased(t, m, cmd, focused, peer)
	})

	t.Run("the first tab replaces a loaded classic slot", func(t *testing.T) {
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "c-live")
		m.list.AddInstance(live)
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle([]config.Workspace{preservedTerminalWorkspace(t, "ws-a")})
		require.Equal(t, []string{"ws-a"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("global mode re-entered from global mode", func(t *testing.T) {
		t.Setenv("LOOM_HOME", t.TempDir())
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "g-live")
		m.list.AddInstance(live)
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle(nil)
		assertReleased(t, m, cmd, live)
	})
}

// TestReleaseInstancesCmd_OnlyAttachedLiveInstances: the release covers
// started, non-paused instances with an attached preview PTY, and is nil
// when there are none.
func TestReleaseInstancesCmd_OnlyAttachedLiveInstances(t *testing.T) {
	isolateTmux(t)
	unstarted, err := session.NewInstance(session.InstanceOptions{Title: "new", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	paused, err := session.FromInstanceData(session.InstanceData{
		Title: "paused", Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, releaseInstancesCmd([]*session.Instance{unstarted, paused}), "nothing attached, nothing to release")
	assert.Nil(t, releaseSlotCmd(nil))

	live := liveInstance(t, "live")
	cmd := releaseInstancesCmd([]*session.Instance{unstarted, paused, live})
	require.NotNil(t, cmd)
	assert.True(t, live.PtmxAlive(), "building the Cmd must not release anything")
	assert.Nil(t, cmd(), "the release reports nothing back to Update")
	assert.False(t, live.PtmxAlive())
}

// TestDroppedSlot_StaleProbeDoesNotReattach: a metadata probe taken before
// a slot was dropped can land after its release, reporting the released
// PTY as dead. The self-heal used to RepairPtmx it, re-attaching an
// instance nothing displays.
func TestDroppedSlot_StaleProbeDoesNotReattach(t *testing.T) {
	isolateTmux(t)
	m := fleetHome(t)
	m.ctx = cancelledCtx()
	live := liveInstance(t, "b-live")
	m.slots[1].list.AddInstance(live)
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "afocus"}}))
	require.False(t, live.PtmxAlive())

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: live, tmuxLive: tmux.LivenessAlive, ptmxAlive: false},
	}})
	assert.False(t, live.PtmxAlive(), "a dropped instance must not be re-attached")
}
