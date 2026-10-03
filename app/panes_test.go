package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
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

// TestKillAndPause_CloseTheClientOnlyAfterTheRegistryDrops: a client is
// closed only once the registry has dropped it. The kill and pause Cmds
// used to close it while it was still registered, so Update could
// re-attach it (the tick's repair of a same-named session) or release it
// (the tick's prune) at the same time, racing its pump. Now the Cmd leaves
// the client alone, and the completion's prune drops it from the registry
// and closes it in the returned Cmd.
func TestKillAndPause_CloseTheClientOnlyAfterTheRegistryDrops(t *testing.T) {
	isolateTmux(t)
	for _, op := range []struct {
		name   string
		action func(m *home, inst *session.Instance) tea.Cmd
		done   func(t *testing.T, msg tea.Msg)
	}{
		{"kill", func(m *home, inst *session.Instance) tea.Cmd {
			preAction, killAction := killActionFor(m, inst)
			preAction()
			return killAction
		}, func(t *testing.T, msg tea.Msg) { require.IsType(t, killInstanceMsg{}, msg) }},
		{"pause", func(m *home, inst *session.Instance) tea.Cmd {
			require.NoError(t, inst.TransitionTo(session.Loading)) // as the pause's confirm does
			return pauseActionFor(m, inst)
		}, func(t *testing.T, msg tea.Msg) { require.IsType(t, pauseInstanceMsg{}, msg, "%v", msg) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			m := newTestHome(t)
			inst := startedInstanceWithProgram(t, "victim-"+op.name, "claude", "idle")
			m.list.AddInstance(inst)
			name := inst.Pane().TmuxSessionName()
			c := clientOf(t, inst)
			require.True(t, c.PtmxAlive(), "fixture: the client is attached")

			msg := op.action(m, inst)() // off the Update goroutine, as the runtime runs it
			op.done(t, msg)
			assert.Same(t, c, m.panes.Get(name), "the Cmd leaves the registry to Update")
			assert.True(t, c.PtmxAlive(), "the Cmd must not close a registered client")

			_, cmd := m.Update(msg)
			assert.Nil(t, m.panes.Get(name), "the completion drops it from the registry")
			assert.True(t, c.PtmxAlive(), "and closes it only in the returned Cmd")
			drainCmd(cmd)
			assert.False(t, c.PtmxAlive())
		})
	}
}

// TestTransitionFailed_ReattachesARevertedInstance: a pause or kill that
// fails reverts the instance to active, and a tick may have pruned its
// client while it was Loading or Deleting. The revert gives it one back.
func TestTransitionFailed_ReattachesARevertedInstance(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "reverted")
	m.list.AddInstance(inst)
	name := inst.Pane().TmuxSessionName()
	require.NoError(t, inst.TransitionTo(session.Loading)) // the pause's confirm
	drainCmd(m.prunePanes())                               // a tick while it ran
	require.Nil(t, m.panes.Get(name), "precondition: pruned while Loading")

	_, _ = m.Update(transitionFailedMsg{inst: inst, title: inst.Title, op: "pause",
		previousStatus: session.Running, err: errors.New("the session survived")})

	assert.Equal(t, session.Running, inst.GetStatus())
	assert.True(t, m.panes.Alive(name), "the reverted instance gets its client back")
}

// TestScriptResume_ReplacesThePaneClient: session lifecycle attaches no
// client, so a Lua inst:resume() must hand the resumed instance to Update
// for a fresh one. Without it the pane stayed blank until the next tick,
// or, after a liveness pause the prune had not reached yet, stayed on the
// dead session's client forever: its PTY still reads open.
func TestScriptResume_ReplacesThePaneClient(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "resume.lua"), []byte(`
cs.bind("Z", function(ctx)
  ctx:selected():resume()
end)
`), 0o644))
	m := newTestHome(t)
	m.scripts = nil
	initScriptsIn(m, dir, false)
	inst := startedInstanceWithProgram(t, "lua-resumed", "claude", "idle")
	m.list.AddInstance(inst)
	name := inst.Pane().TmuxSessionName()
	old := clientOf(t, inst)
	// The health tick found the agent gone and marked it Paused; its
	// client has not been pruned yet.
	require.NoError(t, inst.TransitionTo(session.Paused))

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	done, ok := cmd().(scriptDoneMsg)
	require.True(t, ok)
	require.NoError(t, done.err)
	require.Equal(t, session.Running, inst.GetStatus(), "precondition: the script resumed it")
	require.Equal(t, []*session.Instance{inst}, done.resumedInstances)

	_, release := m.Update(done)

	fresh := m.panes.Get(name)
	require.NotNil(t, fresh)
	assert.NotSame(t, old, fresh, "the resumed session gets a fresh client")
	assert.True(t, fresh.PtmxAlive())
	assert.True(t, old.PtmxAlive(), "the old one is closed by the returned Cmd, not on Update")
	drainCmd(release)
	assert.False(t, old.PtmxAlive())
	assert.Same(t, fresh, m.panes.Get(name))
}
