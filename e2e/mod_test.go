//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake Claude (fakeagent) plays the loom mod: `loom session send`
// reaches it through the daemon's mod socket, --wait prints its answer, a
// message sent while it waits at a permission prompt runs after the prompt
// is answered without answering it, and after a daemon stop the mod
// reconnects to the next daemon and a send arrives again. Every message is
// sent while the agent is idle or at its prompt, never while a typed line
// waits too: the fake runs those two in either order.
func TestE2E_SendThroughTheMod(t *testing.T) {
	sb := newSandbox(t, "fake-claude")
	startLoom(t, sb)
	createSession(t, sb, "modded")
	send := func(args ...string) (string, error) {
		return runLoom(t, sb, append([]string{sb.LoomBin(), "session", "send", "modded", "-w", devsandbox.WorkspaceName}, args...)...)
	}
	pane := func() string { return paneText(t, sb, tmux.ToLoomTmuxName("modded")) }

	out, err := send("--text", "work 0", "--wait")
	require.NoError(t, err, out)
	assert.Contains(t, out, "fakeagent finished: work 0", "--wait prints the answer")

	sendToAgent(t, sb, "ask")
	require.NoError(t, sb.WaitFor("permission: Bash", uiTimeout))
	prompt := agent.DefaultRegistry().Lookup("claude").PendingPromptPattern()
	require.Contains(t, pane(), prompt)
	queued, err := send("--text", "work 1")
	require.NoError(t, err, queued)
	assert.Contains(t, queued, `Queued for "modded"`)
	time.Sleep(time.Second)
	still := pane()
	assert.Contains(t, still, prompt, "the queued message left the prompt waiting")
	assert.NotContains(t, still, "answered:", "and did not answer it")
	assert.NotContains(t, still, "working 1/10", "nor did it run")
	sendToAgent(t, sb, "y")
	require.Eventually(t, func() bool { return strings.Contains(pane(), "working 10/10") }, uiTimeout, 100*time.Millisecond,
		"the message runs once the prompt is answered")
	assert.Contains(t, pane(), `answered: "y"`, "by the keystroke typed for it")
	require.NoError(t, sb.WaitFor("fakeagent finished: work 1", uiTimeout), "the turn's hooks reached the TUI")

	rec := servingDaemon(t, sb)
	stopped, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, stopped)
	require.True(t, waitExit(rec.PID, uiTimeout))
	again, err := send("--text", "bell", "--wait") // the CLI's join starts the next daemon
	require.NoError(t, err, again)
	assert.Contains(t, again, "fakeagent finished: bell")
	servingDaemon(t, sb, rec.PID)
}
