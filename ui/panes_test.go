package ui

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/internal/testpty"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePty is a tmux.PtyFactory whose PTYs are testpty pairs: attaching
// runs no tmux client, and a client stays attached until the test closes
// its peer, as its session ending would. starts records each command it
// was asked to start, and peers each attach's peer.
type fakePty struct {
	t      *testing.T
	mu     *sync.Mutex
	starts *[]string
	peers  *[]*os.File
}

func (f fakePty) Start(c *exec.Cmd) (*os.File, error) {
	attach, peer := testpty.Pair(f.t)
	f.mu.Lock()
	*f.starts = append(*f.starts, strings.Join(c.Args, " "))
	*f.peers = append(*f.peers, peer)
	f.mu.Unlock()
	return attach, nil
}

func (fakePty) Close() {}

// aliveRunner answers every tmux command with success and empty output.
func aliveRunner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// newTestPaneClients returns a registry whose clients attach through
// fakePty, along with a func reporting the recorded PTY starts.
func newTestPaneClients(t *testing.T) (*PaneClients, func() []string) {
	t.Helper()
	p, starts, _ := newTestPaneClientsWithPeers(t)
	return p, starts
}

// newTestPaneClientsWithPeers is newTestPaneClients plus a func returning
// the i-th attach's peer, whose Close ends that client's session.
func newTestPaneClientsWithPeers(t *testing.T) (*PaneClients, func() []string, func(i int) *os.File) {
	t.Helper()
	var mu sync.Mutex
	var starts []string
	var peers []*os.File
	p := NewPaneClients()
	p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
		return tmux.NewAttachClientWithDeps(name, program, fakePty{t: t, mu: &mu, starts: &starts, peers: &peers}, aliveRunner())
	})
	return p, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), starts...)
		}, func(i int) *os.File {
			mu.Lock()
			defer mu.Unlock()
			return peers[i]
		}
}

// runningInstance is a started, Running instance (no tmux contacted).
func runningInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}

func TestPaneClients_NilRegistryHasNoClients(t *testing.T) {
	var p *PaneClients
	assert.Nil(t, p.Get("loom_a"))
	assert.False(t, p.Alive("loom_a"))
	assert.NoError(t, p.Ensure("loom_a", "claude"))
	assert.Nil(t, p.Retain(nil))
	p.SetDefaultSize(80, 24)
}

func TestPaneClients_EnsureAttachesOnce(t *testing.T) {
	p, starts := newTestPaneClients(t)
	p.SetDefaultSize(100, 30)

	require.NoError(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	require.NotNil(t, c)
	t.Cleanup(func() { _ = c.PausePreview() })
	assert.True(t, c.PtmxAlive())

	require.NoError(t, p.Ensure("loom_a", "claude"))
	assert.Same(t, c, p.Get("loom_a"))
	// ui's TestMain runs tmux on a private socket, so argv may begin
	// "tmux -L <sock>"; the attach itself is the suffix.
	require.Len(t, starts(), 1, "one attach")
	assert.True(t, strings.HasSuffix(starts()[0], "attach-session -t =loom_a"), "by exact name: %q", starts()[0])
}

func TestPaneClients_EnsureReattachesAPausedClient(t *testing.T) {
	p, starts := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	require.NoError(t, c.PausePreview()) // as a full-screen attach does

	require.NoError(t, p.Ensure("loom_a", "claude"))
	t.Cleanup(func() { _ = c.PausePreview() })

	assert.Same(t, c, p.Get("loom_a"), "the same client, re-attached")
	assert.True(t, c.PtmxAlive())
	assert.Len(t, starts(), 2)
}

// TestPaneClients_EnsureReattachesAnExitedClient: a client whose pump hit
// EOF when its session ended still holds an open PTY, but it shows the
// dead session forever. Ensure re-attaches it, the same object, as it does
// a client paused for a full-screen attach: a new session of the same
// name may be running.
func TestPaneClients_EnsureReattachesAnExitedClient(t *testing.T) {
	p, starts, peer := newTestPaneClientsWithPeers(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	t.Cleanup(func() { _ = c.PausePreview() })

	require.NoError(t, peer(0).Close()) // the session ends
	require.Eventually(t, func() bool { return !p.Alive("loom_a") }, 2*time.Second, 5*time.Millisecond)
	require.True(t, c.PtmxAlive(), "precondition: its PTY handle is still open")

	require.NoError(t, p.Ensure("loom_a", "claude"))

	assert.Same(t, c, p.Get("loom_a"), "the same client, re-attached")
	assert.True(t, p.Alive("loom_a"))
	assert.Len(t, starts(), 2)
}

func TestPaneClients_ReplaceHandsBackTheOldClient(t *testing.T) {
	p, _ := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	old := p.Get("loom_a")

	got, err := p.Replace("loom_a", "claude")
	require.NoError(t, err)
	fresh := p.Get("loom_a")
	t.Cleanup(func() { _ = old.PausePreview(); _ = fresh.PausePreview() })

	assert.Same(t, old, got)
	assert.NotSame(t, old, fresh)
	assert.True(t, fresh.PtmxAlive())
	assert.True(t, old.PtmxAlive(), "closing the old one is the caller's, off the Update goroutine")
}

func TestPaneClients_RetainDropsTheUnwanted(t *testing.T) {
	p, _ := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	require.NoError(t, p.Ensure("loom_b", "claude"))
	b := p.Get("loom_b")
	t.Cleanup(func() { _ = p.Get("loom_a").PausePreview(); _ = b.PausePreview() })

	dropped := p.Retain(map[string]bool{"loom_a": true})

	assert.Equal(t, []*tmux.TmuxSession{b}, dropped)
	assert.Nil(t, p.Get("loom_b"))
	assert.NotNil(t, p.Get("loom_a"))
	assert.True(t, b.PtmxAlive(), "closing it is the caller's")
}

func TestPaneClients_ForGuardsTheInstance(t *testing.T) {
	p, _ := newTestPaneClients(t)
	assert.Nil(t, p.For(nil).Client())

	unstarted, err := session.NewInstance(session.InstanceOptions{Title: "new", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	assert.Nil(t, p.For(unstarted).Client(), "not started: no pane")

	paused, err := session.FromInstanceData(session.InstanceData{Title: "paused", Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, p.For(paused).Client(), "paused: no pane")

	inst := runningInstance(t, "live")
	name := inst.Pane().TmuxSessionName()
	assert.Nil(t, p.For(inst).Client(), "nothing attached: no pane")

	require.NoError(t, p.Ensure(name, "claude"))
	t.Cleanup(func() { _ = p.Get(name).PausePreview() })
	assert.Same(t, p.Get(name), p.For(inst).Client(), "the registered client wins")
}

func TestPane_ZeroValueIsInert(t *testing.T) {
	var pane Pane
	s, err := pane.Preview()
	assert.NoError(t, err)
	assert.Empty(t, s)
	_, ok := pane.EmulatorScreen()
	assert.False(t, ok)
	_, ok = pane.CaptureHistory()
	assert.False(t, ok)
	assert.False(t, pane.IsAlternateScreen())
	_, ok = pane.CursorState()
	assert.False(t, ok)
	_, ok = pane.PaneTitle()
	assert.False(t, ok)
	assert.False(t, pane.HasEmulator())
	assert.False(t, pane.Attached())
	assert.NoError(t, pane.SetPreviewSize(80, 24))
	assert.Error(t, pane.SendKeysRaw([]byte("x")), "dropping keys silently would hide a dead inline attach")
	assert.NoError(t, pane.Paste("x"))
	assert.NoError(t, pane.ForwardWheel(true, 1))
	assert.NoError(t, pane.ForwardMouse(0, 1, 1, true))
	pane.ForwardFocus(true)
	assert.Nil(t, pane.GetContentHash())
	updated, hasPrompt, err := pane.DetectStatus()
	assert.False(t, updated)
	assert.False(t, hasPrompt)
	assert.NoError(t, err)
	_, ok = pane.scrollSource()
	assert.False(t, ok)
}

// failFirstPty is a fakePty whose first Start fails, as an attach to a
// session tmux has not finished creating can.
type failFirstPty struct {
	fakePty
	failed *bool
}

func (f failFirstPty) Start(c *exec.Cmd) (*os.File, error) {
	f.mu.Lock()
	first := !*f.failed
	*f.failed = true
	f.mu.Unlock()
	if first {
		return nil, errors.New("attach failed")
	}
	return f.fakePty.Start(c)
}

func TestPaneClients_FailedAttachStaysRegisteredAndRetries(t *testing.T) {
	var mu sync.Mutex
	var starts []string
	var peers []*os.File
	failed := false
	p := NewPaneClients()
	p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
		return tmux.NewAttachClientWithDeps(name, program, failFirstPty{fakePty{t: t, mu: &mu, starts: &starts, peers: &peers}, &failed}, aliveRunner())
	})

	require.Error(t, p.Ensure("loom_a", "claude"))
	c := p.Get("loom_a")
	require.NotNil(t, c, "a client whose attach failed stays registered")
	assert.False(t, c.PtmxAlive())

	require.NoError(t, p.Ensure("loom_a", "claude"))
	t.Cleanup(func() { _ = c.PausePreview() })
	assert.Same(t, c, p.Get("loom_a"), "the retry re-attaches the same client")
	assert.True(t, c.PtmxAlive())
}

func TestPaneClients_EnsureAfterRetainBuildsANewClient(t *testing.T) {
	p, starts := newTestPaneClients(t)
	require.NoError(t, p.Ensure("loom_a", "claude"))
	released := p.Get("loom_a")
	require.Equal(t, []*tmux.TmuxSession{released}, p.Retain(nil))
	require.NoError(t, released.PausePreview()) // as releaseClientsCmd does

	require.NoError(t, p.Ensure("loom_a", "claude"))
	fresh := p.Get("loom_a")
	t.Cleanup(func() { _ = fresh.PausePreview() })

	assert.NotSame(t, released, fresh, "a released client is never re-attached")
	assert.False(t, released.PtmxAlive())
	assert.True(t, fresh.PtmxAlive())
	assert.Len(t, starts(), 2)
}

// TestPaneClients_ConcurrentReadsDuringEnsureAndRetain is for -race: Cmd
// goroutines read the registry (For, Get) while Update attaches and
// prunes.
func TestPaneClients_ConcurrentReadsDuringEnsureAndRetain(t *testing.T) {
	p, _ := newTestPaneClients(t)
	inst := runningInstance(t, "racy")
	name := inst.Pane().TmuxSessionName()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = p.For(inst).Attached()
				_ = p.Get(name)
				_ = p.Alive(name)
			}
		}()
	}

	var released []*tmux.TmuxSession
	for range 50 {
		require.NoError(t, p.Ensure(name, "claude"))
		released = append(released, p.Retain(nil)...)
	}
	close(stop)
	readers.Wait()
	for _, c := range released {
		_ = c.PausePreview()
	}
	assert.Len(t, released, 50)
}
