package app

import (
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// aliveCmdExecForTest reports every `has-session` check as successful, so
// TmuxAlive()/DoesSessionExist() see a live session without touching a real
// tmux server.
func aliveCmdExecForTest() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// deadCmdExecForTest fails every `has-session` check, modeling a tmux
// session whose wrapped process already exited (tmux tears the session down
// once its last window's command exits, per default remain-on-exit=off).
func deadCmdExecForTest() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if containsHasSession(cmd) {
				return exec.ErrNotFound
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, exec.ErrNotFound },
	}
}

func containsHasSession(cmd *exec.Cmd) bool {
	for _, a := range cmd.Args {
		if a == "has-session" {
			return true
		}
	}
	return false
}

// setupInlineAttachTerminalDeathFixture builds an instance whose AGENT tmux
// session is alive but whose TERMINAL pane tmux session is dead (as happens
// when the user's shell exits or crashes while the terminal pane is focused
// during inline attach), then focuses the terminal pane in inline-attach
// mode. Returns the home and instance for the caller to drive further.
func setupInlineAttachTerminalDeathFixture(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))

	agentTs := tmux.NewTmuxSessionWithDeps("a", "claude", fakePtyFactory{t: t}, aliveCmdExecForTest())
	inst.SetTmuxSession(agentTs)

	deadTermTs := tmux.NewTmuxSessionWithDeps(tmux.TerminalSessionName("a"), "bash", fakePtyFactory{t: t}, deadCmdExecForTest())
	m.splitPane.InjectTerminalSessionForTest(inst.Title, deadTermTs, inst.Path)

	m.state = stateInlineAttach
	m.splitPane.SetFocusedPane(ui.FocusTerminal)
	return m, inst
}

// TestHandleStateInlineAttachKey_DeadTerminalSessionExitsInteract is the
// regression guard for the terminal-pane "[exited] and unresponsive" bug:
// when the terminal pane (not the agent) is focused during inline attach and
// its own tmux session has died out from under it, the very next keystroke
// must drop back to nav instead of silently forwarding into a dead PTY
// forever. Before the fix, the liveness guard only ever checked the AGENT's
// TmuxAlive(), so a dead terminal session with a live agent never tripped it.
func TestHandleStateInlineAttachKey_DeadTerminalSessionExitsInteract(t *testing.T) {
	m, _ := setupInlineAttachTerminalDeathFixture(t)

	_, _ = handleStateInlineAttachKey(m, tea.KeyPressMsg{Code: 'x', Text: "x"})

	require.Equal(t, stateDefault, m.state,
		"a dead terminal-pane session must exit inline attach even though the agent session is alive")
}

// TestPreviewTick_DeadTerminalSessionExitsInlineAttach is the previewTickMsg
// counterpart of the key-handler regression above: the per-tick liveness
// check that runs continuously during inline attach has the same
// agent-only blind spot.
func TestPreviewTick_DeadTerminalSessionExitsInlineAttach(t *testing.T) {
	m, _ := setupInlineAttachTerminalDeathFixture(t)

	_, _ = m.Update(previewTickMsg{})

	require.Equal(t, stateDefault, m.state,
		"the preview tick must exit inline attach once the focused terminal session is found dead")
}
