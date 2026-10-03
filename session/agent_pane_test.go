package session

import (
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
	inst.SetTmuxSession(tmux.NewSessionWithDeps("fix", "claude", fakePtyFactory{t: t}, rec))
	require.True(t, inst.Paused())

	require.Error(t, inst.Pane().SendPrompt("fix the bug"))
	assert.Empty(t, ran, "nothing typed into whatever session holds the name")

	require.NoError(t, inst.TransitionTo(Running))
	require.NoError(t, inst.Pane().SendPrompt("fix the bug"))
	assert.NotEmpty(t, ran, "a running instance's prompt is typed")
}
