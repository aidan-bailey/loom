package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestEnclosingSessionName_AsksTheTmuxEnvServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	sock := fmt.Sprintf("loomtest-encl-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = CommandOnSocket(ctx, sock, "kill-server").Run() })
	require.NoError(t, CommandOnSocket(ctx, sock, "new-session", "-d", "-s", "loom_term_probe", "sleep 60").Run())
	path, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{socket_path}").Output()
	require.NoError(t, err)
	pane, err := CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "loom_term_probe", "#{pane_id}").Output()
	require.NoError(t, err)

	t.Setenv("TMUX", strings.TrimSpace(string(path))+",1,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(pane)))
	t.Setenv(EnvTmuxSocket, "ignored-by-design") // the question is about the enclosing server

	name, err := EnclosingSessionName()
	require.NoError(t, err)
	assert.Equal(t, "loom_term_probe", name)
}
