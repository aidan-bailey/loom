package app

import (
	"context"
	"os"
	"path/filepath"
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
	require.NoError(t, inst.Pane().RepairPtmx())
	require.True(t, inst.Pane().PtmxAlive(), "fixture: the preview PTY is attached")
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
		assert.True(t, inst.Pane().PtmxAlive(), "%s: released on the Update goroutine; must wait for the Cmd", inst.Title)
	}
	drainCmd(cmd)
	refs := referencedInstances(m)
	for _, inst := range dropped {
		assert.False(t, inst.Pane().PtmxAlive(), "%s: preview PTY still attached after the drop", inst.Title)
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
	assert.True(t, live.Pane().PtmxAlive(), "building the Cmd must not release anything")
	assert.Nil(t, cmd(), "the release reports nothing back to Update")
	assert.False(t, live.Pane().PtmxAlive())
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
	require.False(t, live.Pane().PtmxAlive())

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: live, tmuxLive: tmux.LivenessAlive, ptmxAlive: false},
	}})
	assert.False(t, live.Pane().PtmxAlive(), "a dropped instance must not be re-attached")
}

// attachedTerminal is a terminal-pane shell session with its attach client
// open (a fake PTY); no tmux server is contacted.
func attachedTerminal(t *testing.T, title string) *tmux.TmuxSession {
	t.Helper()
	ts := tmux.NewTmuxSessionWithDeps(tmux.TerminalSessionName(title), "sh", fakePtyFactory{t: t}, aliveCmdExecForTest())
	require.NoError(t, ts.Restore())
	require.True(t, ts.PtmxAlive(), "fixture: the terminal's attach client is open")
	return ts
}

// TestDroppedSlot_ReleasesTerminalPaneClients: a dropped slot's terminal
// pane kept an attach client open on every loom_term_* shell it had shown.
// The release detaches them (the shells keep running). The pane
// enterGlobalMode carries into the global slot keeps only the clients of
// sessions the global list holds.
func TestDroppedSlot_ReleasesTerminalPaneClients(t *testing.T) {
	isolateTmux(t)

	t.Run("closing a tab", func(t *testing.T) {
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		term := attachedTerminal(t, "b1")
		m.slots[1].splitPane.Terminal().InjectSessionForTest("b1", term, t.TempDir())

		cmd := m.applyWorkspaceToggle([]config.Workspace{{Name: "afocus"}})
		assert.True(t, term.PtmxAlive(), "released on the Update goroutine; must wait for the Cmd")
		drainCmd(cmd)
		assert.False(t, term.PtmxAlive(), "the closed tab's terminal client must be detached")
	})

	t.Run("entering global mode keeps only the carried pane's clients global can show", func(t *testing.T) {
		globalDir := t.TempDir()
		t.Setenv("LOOM_HOME", globalDir)
		// The global list holds a paused "shared" session.
		require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.StateFileName),
			[]byte(`{"instances":[{"title":"shared","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/loom-test-shared"}}]}`), 0o644))
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		carriedPane := m.splitPane
		stale, shared, dropped := attachedTerminal(t, "f1"), attachedTerminal(t, "shared"), attachedTerminal(t, "b1")
		m.slots[0].splitPane.Terminal().InjectSessionForTest("f1", stale, t.TempDir())
		m.slots[0].splitPane.Terminal().InjectSessionForTest("shared", shared, t.TempDir())
		m.slots[1].splitPane.Terminal().InjectSessionForTest("b1", dropped, t.TempDir())

		drainCmd(m.applyWorkspaceToggle(nil))
		require.Same(t, carriedPane, m.splitPane, "the global slot carries the focused tab's panes")
		require.NotNil(t, m.list.GetInstanceByTitle("shared"))
		assert.False(t, dropped.PtmxAlive(), "the other tab's terminal client must be detached")
		assert.False(t, stale.PtmxAlive(), "the carried pane's client for a closed session must be detached")
		assert.True(t, shared.PtmxAlive(), "a terminal the global list can show again stays")
	})
}
