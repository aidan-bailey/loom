package session

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentPane_SendPromptRefusesAPausedInstance: the prompt is pasted
// into the session by name, and a paused instance's name can belong to a
// live session of the same title in another workspace. A script's
// inst:send_prompt on workspace A's paused "fix" typed into workspace B's
// running "fix". Keys and Enter were already refused; prompts are too.
func TestAgentPane_SendPromptRefusesAPausedInstance(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	record := func(c *exec.Cmd) {
		mu.Lock()
		ran = append(ran, strings.Join(c.Args, " "))
		mu.Unlock()
	}
	rec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { record(c); return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { record(c); return nil, nil },
	}
	inst, err := FromInstanceData(InstanceData{Title: "fix", Status: Paused, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSessionForTest(tmux.NewSessionWithDeps("fix", "claude", fakePtyFactory{t: t}, rec))
	require.True(t, inst.Paused())

	require.Error(t, inst.Pane().SendPrompt("fix the bug"))
	assert.Empty(t, ran, "nothing typed into whatever session holds the name")

	require.NoError(t, inst.TransitionTo(Running))
	require.NoError(t, inst.Pane().SendPrompt("fix the bug"))
	assert.NotEmpty(t, ran, "a running instance's prompt is typed")
}

// TrustPromptOpen reads the pane's screen (capture-pane) and asks the agent
// whether a pasted Enter would answer a trust prompt there. A paused
// instance's name may belong to another workspace's session, so it captures
// nothing; a failed capture is an error, which the caller decides on.
func TestAgentPane_TrustPromptOpen(t *testing.T) {
	screen, captureErr, captures := "", error(nil), 0
	rec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			if cmd_test.TmuxSubcommand(c.Args) == "capture-pane" {
				captures++
				return []byte(screen), captureErr
			}
			return nil, nil
		},
	}
	inst, err := FromInstanceData(InstanceData{Title: "fix", Status: Paused, Program: "claude", IsWorkspaceTerminal: true}, t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSessionForTest(tmux.NewSessionWithDeps("fix", "claude", fakePtyFactory{t: t}, rec))

	screen = "Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit"
	open, err := inst.Pane().TrustPromptOpen()
	require.NoError(t, err)
	assert.False(t, open, "paused: no screen of its own to read")
	assert.Zero(t, captures)

	require.NoError(t, inst.TransitionTo(Running))
	open, err = inst.Pane().TrustPromptOpen()
	require.NoError(t, err)
	assert.True(t, open, "the trust prompt is on screen")
	assert.Equal(t, 1, captures)

	screen = "❯ \n? for shortcuts"
	open, err = inst.Pane().TrustPromptOpen()
	require.NoError(t, err)
	assert.False(t, open, "Claude's input box is no trust prompt")

	captureErr = errors.New("can't find pane: loom_fix")
	_, err = inst.Pane().TrustPromptOpen()
	assert.Error(t, err, "a failed capture says so")
}
