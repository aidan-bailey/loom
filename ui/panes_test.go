package ui

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// devNullPty is a tmux.PtyFactory whose PTYs are /dev/null: attaching runs
// no tmux client, and the output pump hits EOF at once. starts records
// each command it was asked to start.
type devNullPty struct {
	mu     *sync.Mutex
	starts *[]string
}

func (f devNullPty) Start(c *exec.Cmd) (*os.File, error) {
	f.mu.Lock()
	*f.starts = append(*f.starts, strings.Join(c.Args, " "))
	f.mu.Unlock()
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

func (devNullPty) Close() {}

// aliveRunner answers every tmux command with success and empty output.
func aliveRunner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// newTestPaneClients returns a registry whose clients attach through
// devNullPty, along with a func reporting the recorded PTY starts.
func newTestPaneClients(t *testing.T) (*PaneClients, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var starts []string
	p := NewPaneClients()
	p.SetClientFactoryForTest(func(name, program string) *tmux.TmuxSession {
		return tmux.NewAttachClientWithDeps(name, program, devNullPty{mu: &mu, starts: &starts}, aliveRunner())
	})
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), starts...)
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
	// Stage 1A transition (until Package C): with nothing registered, the
	// instance's own client is the pane.
	assert.Same(t, inst.TmuxSession(), p.For(inst).Client())

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
	assert.False(t, pane.PtmxAlive())
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
