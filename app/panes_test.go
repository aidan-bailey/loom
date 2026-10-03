package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

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

// peerPty is fakePtyFactory keeping each attach's peer: closing one ends
// that client's session, and writing to it is the session's output.
type peerPty struct {
	t     *testing.T
	mu    *sync.Mutex
	peers *[]*os.File
}

func (f peerPty) Start(*exec.Cmd) (*os.File, error) {
	attach, peer := testpty.Pair(f.t)
	f.mu.Lock()
	*f.peers = append(*f.peers, peer)
	f.mu.Unlock()
	return attach, nil
}

func (peerPty) Close() {}

// relaunchedUnderItsName builds an active instance whose registered client
// was watching a session that has ended (its pump read EOF) while a new
// session runs under the same name, as when activateWorkspace kills and
// recreates a workspace terminal another slot's client still watches.
// peer(i) is the i-th attach's peer: 0 the dead session's, 1 the next.
func relaunchedUnderItsName(t *testing.T, title string) (inst *session.Instance, peer func(i int) *os.File) {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, "claude", fakePtyFactory{t: t}, aliveCmdExecForTest()))
	require.NoError(t, inst.TransitionTo(session.Running))
	var mu sync.Mutex
	var peers []*os.File
	peer = func(i int) *os.File {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(peers) {
			return nil
		}
		return peers[i]
	}
	old := attachTestClient(t, inst, peerPty{t: t, mu: &mu, peers: &peers}, aliveCmdExecForTest())
	require.True(t, old.HasEmulator(), "fixture: the pane renders from the emulator")
	require.NoError(t, peer(0).Close()) // the session ends; the mock tmux reports one of its name running
	require.Eventually(t, func() bool { return !old.Attached() }, 2*time.Second, 5*time.Millisecond,
		"fixture: the client's pump read EOF")
	require.True(t, old.PtmxAlive(), "fixture: its PTY handle is still open")
	return inst, peer
}

// requireShowsNewSession checks that inst's pane is attached to the
// relaunched session: a second PTY was opened for it, and what that
// session prints reaches the pane.
func requireShowsNewSession(t *testing.T, m *home, inst *session.Instance, peer func(i int) *os.File) {
	t.Helper()
	assert.True(t, m.panes.Alive(inst.Pane().TmuxSessionName()))
	next := peer(1)
	require.NotNil(t, next, "no new attach to the relaunched session")
	_, err := next.WriteString("relaunched session\r\n")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		screen, _ := m.panes.For(inst).EmulatorScreen()
		return strings.Contains(screen, "relaunched session")
	}, 2*time.Second, 5*time.Millisecond, "the new session's output must reach the pane")
}

// TestExitedClient_HealsOntoTheRelaunchedSession: a client whose pump hit
// EOF still holds an open PTY. Read as alive, it was kept by every attach
// and repair point while its pane showed "[exited]", and the session
// relaunched under its name ran with no client at all. Each healing path
// re-attaches it.
func TestExitedClient_HealsOntoTheRelaunchedSession(t *testing.T) {
	isolateTmux(t)

	t.Run("ensurePane", func(t *testing.T) {
		m := newTestHome(t)
		inst, peer := relaunchedUnderItsName(t, "relaunched")
		m.list.AddInstance(inst)

		m.ensurePane(inst)

		requireShowsNewSession(t, m, inst, peer)
	})

	t.Run("a workspace load", func(t *testing.T) {
		m := newTestHome(t)
		inst, peer := relaunchedUnderItsName(t, "relaunched")
		m.list.AddInstance(inst)

		m.ensureSlotPanes(m.workspaceSlot)

		requireShowsNewSession(t, m, inst, peer)
	})

	t.Run("the Dead event", func(t *testing.T) {
		m := newTestHome(t)
		inst, peer := relaunchedUnderItsName(t, "relaunched")
		m.list.AddInstance(inst)

		_, cmd := m.Update(ptyDeadMsg{session: inst.Pane().TmuxSessionName()})
		require.NotNil(t, cmd)
		verified, ok := cmd().(deadVerifiedMsg)
		require.True(t, ok)
		require.Equal(t, tmux.LivenessAlive, verified.tmuxLive, "the relaunched session is alive")
		_, _ = m.Update(verified)

		assert.Equal(t, session.Running, inst.GetStatus())
		requireShowsNewSession(t, m, inst, peer)
	})

	t.Run("the health tick", func(t *testing.T) {
		m := newTestHome(t)
		inst, peer := relaunchedUnderItsName(t, "relaunched")
		m.list.AddInstance(inst)

		active := m.activeInstances()
		_, _ = m.Update(gatherMetadataCmd(active, nil, nil, nil, m.paneSnapshot(active))())

		requireShowsNewSession(t, m, inst, peer)
	})
}
