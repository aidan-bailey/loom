package app

import (
	"os"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePtyFactory is a no-op PtyFactory: Start returns a /dev/null
// handle so callers can Close it safely; Close is a no-op. Attached
// to the mock tmux session so nothing touches a real pseudo-terminal.
type fakePtyFactory struct{ t *testing.T }

func (f fakePtyFactory) Start(*exec.Cmd) (*os.File, error) {
	h, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		f.t.Fatalf("fakePtyFactory: /dev/null: %v", err)
	}
	return h, nil
}

func (f fakePtyFactory) Close() {}

// homeWithAppState augments newTestHome with the appState dependency
// needed by intents that funnel through showHelpScreen (checkout,
// fullscreen_attach, show_help). Kept local to this file so other
// tests keep their minimal fixture.
func homeWithAppState(t *testing.T) *home {
	t.Helper()
	h := newTestHome(t)
	h.appState = config.DefaultState()
	return h
}

// addReadyInstance attaches a Running instance so preconditions-gated
// intents (push/kill/checkout/attach/quick_input) have a valid
// selection. A mock TmuxSession is installed with a cmdExec that
// reports tmux `has-session` success, so TmuxAlive() returns true
// without touching a real tmux server.
func addReadyInstance(t *testing.T, h *home) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = h.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))

	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	ts := tmux.NewTmuxSessionWithDeps("a", "true", fakePtyFactory{t: t}, cmdExec)
	inst.SetTmuxSession(ts)
	return inst
}

func TestHandleScriptIntentQuit(t *testing.T) {
	m := newTestHome(t)
	id := script.NewIntentID()
	cmd := m.handleScriptIntent(pendingIntent{
		id:     id,
		intent: script.QuitIntent{},
	})
	require.NotNil(t, cmd)

	// QuitIntent batches tea.Quit with a resume for the awaiting
	// coroutine — if handleQuit's save path returns a non-terminal
	// error Cmd the resume still fires, so the Lua side never hangs.
	batchMsg, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "QuitIntent should produce tea.BatchMsg, got %T", cmd())
	require.Len(t, batchMsg, 2, "batch should carry the quit Cmd and the resume Cmd")

	var sawQuit, sawResume bool
	for _, c := range batchMsg {
		switch msg := c().(type) {
		case tea.QuitMsg:
			sawQuit = true
		case scriptResumeMsg:
			assert.Equal(t, id, msg.id, "resume must target the original intent id")
			sawResume = true
		}
	}
	assert.True(t, sawQuit, "batch must include tea.Quit")
	assert.True(t, sawResume, "batch must include the coroutine resume")
}

func TestHandleScriptIntentPushSelectedConfirm(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.PushSelectedIntent{Confirm: true},
	})
	assert.Equal(t, stateConfirm, m.state, "confirm=true opens confirmation overlay")
}

func TestHandleScriptIntentPushSelectedNoConfirm(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	cmd := m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.PushSelectedIntent{Confirm: false},
	})
	assert.NotEqual(t, stateConfirm, m.state, "confirm=false skips overlay")
	require.NotNil(t, cmd, "no-confirm push still enqueues push Cmd")
}

func TestHandleScriptIntentKillSelectedConfirm(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.KillSelectedIntent{Confirm: true},
	})
	assert.Equal(t, stateConfirm, m.state)
}

func TestHandleScriptIntentKillSelectedNoConfirm(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	cmd := m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.KillSelectedIntent{Confirm: false},
	})
	assert.NotEqual(t, stateConfirm, m.state)
	require.NotNil(t, cmd)
}

func TestHandleScriptIntentCheckout(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.CheckoutIntent{Confirm: true, Help: true},
	})
	// help=true opens the help screen first (unseen flag).
	assert.Equal(t, stateHelp, m.state)
}

func TestHandleScriptIntentResume(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	// Resume only transitions from Paused.
	require.NoError(t, inst.TransitionTo(session.Paused))

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.ResumeIntent{},
	})
	assert.Equal(t, session.Loading, inst.GetStatus(), "resume flips selected to Loading")
}

func TestHandleScriptIntentNewInstance(t *testing.T) {
	m := newTestHome(t)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.NewInstanceIntent{Prompt: false},
	})
	assert.Equal(t, stateNew, m.state)
}

func TestHandleScriptIntentNewInstancePrompt(t *testing.T) {
	m := newTestHome(t)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.NewInstanceIntent{Prompt: true},
	})
	assert.Equal(t, stateNew, m.state)
	assert.True(t, m.promptAfterName)
}

func TestHandleScriptIntentShowHelp(t *testing.T) {
	m := homeWithAppState(t)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.ShowHelpIntent{},
	})
	assert.Equal(t, stateHelp, m.state)
}

func TestHandleScriptIntentInlineAttach(t *testing.T) {
	cases := []struct {
		name string
		pane script.AttachPane
	}{
		{"agent", script.AttachPaneAgent},
		{"terminal", script.AttachPaneTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := homeWithAppState(t)
			addReadyInstance(t, m)

			m.handleScriptIntent(pendingIntent{
				id:     script.NewIntentID(),
				intent: script.InlineAttachIntent{Pane: tc.pane},
			})
			assert.Equal(t, stateInlineAttach, m.state)
		})
	}
}

// TestHandleScriptIntentInlineAttachAgentResetsScroll covers the bug
// fix: pressing ctrl+a while the agent pane is scrolled-back used to
// leave the pane scrolled while keystrokes flowed to live tmux. The
// intent handler now drops scroll mode before flipping into stateInlineAttach.
func TestHandleScriptIntentInlineAttachAgentResetsScroll(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)

	// Drive the agent pane into scroll mode. The split pane needs the
	// instance set so PageUp routes through to the focused (default:
	// agent) pane. The mock tmux returns empty content for the full-
	// history capture, but enterScrollMode still sets isScrolling=true.
	m.splitPane.SetInstance(inst)
	m.splitPane.PageUp()
	require.True(t, m.splitPane.IsAgentInScrollMode(), "test setup: agent should be scrolled")

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.InlineAttachIntent{Pane: script.AttachPaneAgent},
	})
	assert.False(t, m.splitPane.IsAgentInScrollMode(), "inline-attach must reset agent scroll")
	assert.Equal(t, stateInlineAttach, m.state)
}

func TestHandleScriptIntentFullscreenAttach(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)

	m.handleScriptIntent(pendingIntent{
		id:     script.NewIntentID(),
		intent: script.FullscreenAttachIntent{Pane: script.AttachPaneAgent},
	})
	// Help screen opens first (unseen attach-help flag).
	assert.Equal(t, stateHelp, m.state)
}

func TestHandleScriptIntentQuickInput(t *testing.T) {
	cases := []struct {
		name string
		pane script.AttachPane
	}{
		{"agent", script.AttachPaneAgent},
		{"terminal", script.AttachPaneTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestHome(t)
			addReadyInstance(t, m)

			m.handleScriptIntent(pendingIntent{
				id:     script.NewIntentID(),
				intent: script.QuickInputIntent{Pane: tc.pane},
			})
			assert.Equal(t, stateQuickInteract, m.state)
		})
	}
}

// TestHandleScriptIntentResumeMessageEmitted verifies every non-Quit
// intent batches a scriptResumeMsg so the awaiting coroutine unblocks.
// Quit is exempt because tea.Quit ends the program.
func TestHandleScriptIntentResumeMessageEmitted(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)
	id := script.NewIntentID()

	cmd := m.handleScriptIntent(pendingIntent{
		id:     id,
		intent: script.ShowHelpIntent{},
	})
	require.NotNil(t, cmd)

	// Drain the batch and look for the resume message. tea.Batch returns
	// a BatchMsg slice of Cmds; walk them and fire each to inspect.
	msg := cmd()
	foundResume := false
	assertResume := func(m tea.Msg) {
		if rm, ok := m.(scriptResumeMsg); ok && rm.id == id {
			foundResume = true
		}
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			assertResume(sub())
		}
	} else {
		assertResume(msg)
	}
	assert.True(t, foundResume, "expected scriptResumeMsg in batch")
}
