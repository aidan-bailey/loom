package tmux

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommand_DefaultServerLeavesArgvUnchanged(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "")
	cmd := Command(context.Background(), "has-session", "-t=loom_x")
	assert.Equal(t, []string{"tmux", "has-session", "-t=loom_x"}, cmd.Args)
}

func TestCommand_PrivateSocketPrependsL(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "loomdev-demo")
	cmd := Command(context.Background(), "ls")
	assert.Equal(t, []string{"tmux", "-L", "loomdev-demo", "ls"}, cmd.Args)
}

func TestCommandOnSocket_IgnoresEnv(t *testing.T) {
	t.Setenv(EnvTmuxSocket, "from-env")
	cmd := CommandOnSocket(context.Background(), "explicit", "ls")
	assert.Equal(t, []string{"tmux", "-L", "explicit", "ls"}, cmd.Args)
}

func TestCommandOnSocket_DoesNotMutateCallerArgs(t *testing.T) {
	args := make([]string, 1, 8) // spare capacity would expose an in-place append
	args[0] = "ls"
	_ = CommandOnSocket(context.Background(), "s", args...)
	assert.Equal(t, []string{"ls"}, args)
}

func TestCommand_BoundToContext(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, Command(ctx, "ls").Run(), context.Canceled)
}
