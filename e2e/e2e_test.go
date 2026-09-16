//go:build e2e

// Package e2e drives a real dev build of loom inside an isolated sandbox.
// Run with: go test -tags e2e ./e2e/...
package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const uiTimeout = 30 * time.Second

func newSandbox(t *testing.T, profile string) *devsandbox.Sandbox {
	t.Helper()
	for _, bin := range []string{"tmux", "git", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	sb, err := devsandbox.Open(fmt.Sprintf("e2e%d", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			screen, _ := sb.Screen(false)
			t.Logf("last screen:\n%s\nsandbox logs:\n%s", screen, sb.TailLogs(40))
		}
		_ = sb.Down()
	})
	require.NoError(t, sb.Up(devsandbox.UpOptions{SourceWorktree: root, DefaultProfile: profile}))
	require.NoError(t, sb.Build(root))
	return sb
}

func startLoom(t *testing.T, sb *devsandbox.Sandbox) {
	t.Helper()
	require.NoError(t, sb.Start(devsandbox.StartOptions{}))
	require.NoError(t, sb.WaitFor(devsandbox.WorkspaceName, uiTimeout))
}

func createSession(t *testing.T, sb *devsandbox.Sandbox, title string) {
	t.Helper()
	require.NoError(t, sb.SendKeys("n"))
	// "n" is dispatched through the Lua script engine (script/defaults.lua),
	// which hops through a goroutine before the model actually enters the
	// title-entry state. Typing immediately can race that hop and land on
	// the previous (stateDefault) keymap instead, so wait for the prompt
	// the title-entry state renders before sending the title.
	require.NoError(t, sb.WaitFor("enter a name for the instance", uiTimeout))
	require.NoError(t, sb.SendText(title))
	require.NoError(t, sb.SendKeys("Enter"))
	require.NoError(t, sb.WaitFor("Session Launch Options", uiTimeout))
	require.NoError(t, sb.SendKeys("Enter"))
	// The title is already on screen while typing, so wait on the agent's
	// tmux session and the fake agent's banner to know the start finished.
	require.Eventually(t, func() bool {
		return tmux.CommandOnSocket(context.Background(), sb.Socket(),
			"has-session", "-t="+tmux.ToLoomTmuxName(title)).Run() == nil
	}, uiTimeout, 200*time.Millisecond, "agent session for %q never started", title)
	require.NoError(t, sb.WaitFor("commands: work N", uiTimeout))
	// Starting a session auto-attaches the agent pane in "capturing input"
	// mode (app/app.go's instanceStartedMsg handler, confirmed by driving
	// the sandbox interactively: the footer reads "CAPTURING INPUT" right
	// after start). Detach so later driver keystrokes (quick input, quit)
	// reach loom's normal keymap instead of the agent's pty.
	require.NoError(t, sb.SendKeys("C-q"))
}

func TestE2E_SandboxLeavesOtherServersAlone(t *testing.T) {
	sb := newSandbox(t, "")
	ctx := context.Background()
	decoy := fmt.Sprintf("loomtest-decoy-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, decoy, "kill-server").Run() })
	require.NoError(t, tmux.CommandOnSocket(ctx, decoy, "new-session", "-d", "-s", "loom_decoy", "sleep 300").Run())
	path, err := tmux.CommandOnSocket(ctx, decoy, "display-message", "-p", "-t", "loom_decoy", "#{socket_path}").Output()
	require.NoError(t, err)
	// Behave like a shell inside the decoy server: the driver's tmux client
	// (and the private server it starts) inherit this $TMUX.
	t.Setenv("TMUX", strings.TrimSpace(string(path))+",1,0")

	startLoom(t, sb) // startup has run the orphan sweep

	assert.NoError(t, tmux.CommandOnSocket(ctx, decoy, "has-session", "-t=loom_decoy").Run(),
		"a sandboxed loom must never sweep another server's loom_* sessions")
}

func TestE2E_FakeAiderPromptSurfacesAsAwaitingInput(t *testing.T) {
	sb := newSandbox(t, "fake-aider")
	startLoom(t, sb)
	createSession(t, sb, "asker")

	require.NoError(t, sb.SendKeys("a"))
	// "a" is also Lua-dispatched (see createSession); wait for the quick
	// input bar's own footer before typing into it.
	require.NoError(t, sb.WaitFor("Enter to send to agent", uiTimeout))
	require.NoError(t, sb.SendText("ask"))
	require.NoError(t, sb.SendKeys("Enter"))

	require.NoError(t, sb.WaitFor("awaiting input", uiTimeout))
}

func TestE2E_SessionSurvivesRestart(t *testing.T) {
	sb := newSandbox(t, "")
	startLoom(t, sb)
	createSession(t, sb, "keeper")

	require.NoError(t, sb.Stop(10*time.Second))
	require.False(t, sb.DriverRunning())
	ctx := context.Background()
	require.NoError(t, tmux.CommandOnSocket(ctx, sb.Socket(), "has-session", "-t="+tmux.ToLoomTmuxName("keeper")).Run(),
		"quitting loom must leave the agent session running")

	startLoom(t, sb)
	require.NoError(t, sb.WaitFor("keeper", uiTimeout))
}
