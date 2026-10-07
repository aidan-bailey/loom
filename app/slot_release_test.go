package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveInstance builds a started, Running instance whose tmux session has a
// TUI attach client (a fake PTY) in the test's registry, as a workspace
// load leaves a live session. No tmux server is contacted.
func liveInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	// Paused data comes back started; its session is swapped for a
	// mock-backed one and the instance moved to Running.
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, "claude", fakePtyFactory{t: t}, aliveCmdExecForTest()))
	require.NoError(t, inst.TransitionTo(session.Running))
	require.True(t, attachTestClient(t, inst, fakePtyFactory{t: t}, aliveCmdExecForTest()).PtmxAlive(), "fixture: the client is attached")
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

// referencedViews lists every instance view the model can still reach.
func referencedViews(m *home) []core.InstanceView {
	var out []core.InstanceView
	for _, s := range m.openSlots() {
		out = append(out, s.list.GetInstances()...)
	}
	for _, v := range []*core.InstanceView{m.splitPane.Instance(), m.menu.Instance(), m.pendingMergeTarget} {
		if v != nil {
			out = append(out, *v)
		}
	}
	if m.attachingID != 0 {
		attaching := core.InstanceView{ID: m.attachingID}
		if v, _ := m.viewByID(m.attachingID); v != nil {
			attaching = *v
		}
		out = append(out, attaching)
	}
	return append(out, m.pendingMergeSourceItems...)
}

// reaches reports whether any of refs is inst's: a view of its agent
// session. A dropped instance's ID is forgotten once the model publishes,
// so the views are matched by the session they show.
func reaches(refs []core.InstanceView, inst *session.Instance) bool {
	name := inst.Pane().TmuxSessionName()
	return slices.ContainsFunc(refs, func(v core.InstanceView) bool { return v.TmuxSession == name })
}

// pointAt makes inst the selection the panes and menu render, as
// instanceChanged would (without its terminal-pane tmux side effects).
func pointAt(m *home, inst *session.Instance) {
	m.syncViews()
	m.list.SelectID(idOf(m, inst))
	v, _ := m.viewByID(idOf(m, inst))
	m.splitPane.SetInstance(v)
	m.menu.SetInstance(v)
}

// assertReleased checks that dropped instances' clients leave the registry
// at once, keep their PTY until the returned Cmd runs and lose it once it
// has, and that the instances are no longer reachable.
func assertReleased(t *testing.T, m *home, cmd tea.Cmd, dropped ...*session.Instance) {
	t.Helper()
	for _, inst := range dropped {
		assert.Nil(t, m.panes.Get(inst.Pane().TmuxSessionName()), "%s: still registered after the drop", inst.Title)
		assert.True(t, clientOf(t, inst).PtmxAlive(), "%s: released on the Update goroutine; must wait for the Cmd", inst.Title)
	}
	drainCmd(cmd)
	refs := referencedViews(m)
	for _, inst := range dropped {
		assert.False(t, clientOf(t, inst).PtmxAlive(), "%s: attach client still open after the drop", inst.Title)
		assert.False(t, reaches(refs, inst), "%s: still reachable from the model", inst.Title)
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
		m.slots[1].ws.Add(live)
		m.syncViews()

		cmd := m.applyWorkspaceToggle([]config.Workspace{{Name: "afocus"}})
		require.Equal(t, []string{"afocus"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("closing the focused tab", func(t *testing.T) {
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "a-live")
		m.ws.Add(live)
		m.syncViews()
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}})
		require.Equal(t, []string{"bpeer"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("entering global mode drops every tab", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		m := fleetHome(t)
		m.ctx = cancelledCtx()
		focused, peer := liveInstance(t, "a-live"), liveInstance(t, "b-live")
		m.ws.Add(focused)
		m.slots[1].ws.Add(peer)
		m.syncViews()
		pointAt(m, focused) // the carried-over splitPane must let go of it

		cmd := m.applyWorkspaceToggle(nil)
		require.Empty(t, m.slots)
		assertReleased(t, m, cmd, focused, peer)
	})

	t.Run("the first tab replaces a loaded classic slot", func(t *testing.T) {
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
		m.ctx = cancelledCtx()
		live := liveInstance(t, "c-live")
		m.ws.Add(live)
		m.syncViews()
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle([]config.Workspace{preservedTerminalWorkspace(t, "ws-a")})
		require.Equal(t, []string{"ws-a"}, m.slotNames())
		assertReleased(t, m, cmd, live)
	})

	t.Run("global mode entered from a classic workspace slot", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir())
		m, _ := restoreModeHome(t, &recordingExec{}, `[]`)
		m.wsCtx().Name = "ws-classic" // launched inside a workspace, no tabs
		m.ctx = cancelledCtx()
		live := liveInstance(t, "g-live")
		m.ws.Add(live)
		m.syncViews()
		pointAt(m, live)

		cmd := m.applyWorkspaceToggle(nil)
		assertReleased(t, m, cmd, live)
	})
}

// TestPrunePanes_ReleasesOnlyInactiveSessions: prunePanes drops and closes,
// off the Update goroutine, the clients of sessions no loaded instance is
// active on, and leaves the rest attached.
func TestPrunePanes_ReleasesOnlyInactiveSessions(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	keep, gone := liveInstance(t, "keep"), liveInstance(t, "gone")
	m.ws.Add(keep)
	m.ws.Add(gone)
	m.syncViews()
	require.NoError(t, gone.TransitionTo(session.Paused))
	m.syncViews()
	assert.Nil(t, releaseSlotCmd(nil))

	cmd := m.prunePanes()
	require.NotNil(t, cmd)
	assert.True(t, clientOf(t, gone).PtmxAlive(), "building the Cmd must not close anything")
	assert.Nil(t, cmd(), "the release reports nothing back to Update")

	assert.False(t, clientOf(t, gone).PtmxAlive())
	assert.Nil(t, m.panes.Get(gone.Pane().TmuxSessionName()))
	assert.True(t, m.panes.Alive(keep.Pane().TmuxSessionName()))
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
	m.slots[1].ws.Add(live)
	m.syncViews()
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "afocus"}}))
	require.False(t, clientOf(t, live).PtmxAlive())

	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{
		{Instance: live, TmuxLive: tmux.LivenessAlive},
	}})
	assert.Nil(t, m.panes.Get(live.Pane().TmuxSessionName()), "a dropped instance must not be re-attached")
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
// sessions the global list holds, and the prune that follows (prunePanes)
// only those of its active rows.
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
		t.Setenv(config.EnvGlobalDir, globalDir)
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
		assert.False(t, shared.PtmxAlive(),
			"the global list holds it, but paused: the prune releases a terminal client no active row shows")
	})
}

// TestPrune_ReleasesTheTerminalClientsOfGoneSessions: kill and pause take
// no TUI step in their job any more (the instance ends its terminal shell
// by name, CloseRelatedSession). The prune after the completion
// (ClientsStale) detaches the terminal pane's client of a session no
// longer active, and keeps an active one's.
func TestPrune_ReleasesTheTerminalClientsOfGoneSessions(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	killed, kept := liveInstance(t, "killed"), liveInstance(t, "kept")
	m.ws.Add(killed)
	m.ws.Add(kept)
	m.syncViews()
	killedTerm, keptTerm := attachedTerminal(t, "killed"), attachedTerminal(t, "kept")
	m.splitPane.Terminal().InjectSessionForTest("killed", killedTerm, t.TempDir())
	m.splitPane.Terminal().InjectSessionForTest("kept", keptTerm, t.TempDir())

	cmd := deliver(t, m, core.KillResult{Instance: killed, Title: killed.Title})
	assert.True(t, killedTerm.PtmxAlive(), "detached on Update; closed only in the Cmd")
	drainCmd(cmd)
	assert.False(t, killedTerm.PtmxAlive(), "the killed session's terminal client is released")
	assert.True(t, keptTerm.PtmxAlive(), "an active session's terminal client is kept")

	drainCmd(m.prunePanes()) // the health tick's
	assert.True(t, keptTerm.PtmxAlive(), "and the tick's prune keeps it too")
}
