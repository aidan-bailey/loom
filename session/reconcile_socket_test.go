package session

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer pins the
// dev-sandbox safety property: with LOOM_TMUX_SOCKET set, the orphan sweep
// only touches that server — even when $TMUX names another server holding
// unclaimed loom_* sessions (a dev build started from a loom pane).
func TestCleanupOrphanedSessions_PrivateSocketSparesEnclosingServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	host, private := "loomtest-host-"+suffix, "loomtest-priv-"+suffix
	for _, sock := range []string{host, private} {
		sock := sock
		t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	}
	require.NoError(t, tmux.CommandOnSocket(ctx, host, "new-session", "-d", "-s", "loom_decoy", "sleep 120").Run())
	require.NoError(t, tmux.CommandOnSocket(ctx, private, "new-session", "-d", "-s", "loom_stray", "sleep 120").Run())

	out, err := tmux.CommandOnSocket(ctx, host, "display-message", "-p", "-t", "loom_decoy", "#{socket_path}").Output()
	require.NoError(t, err)
	// Pretend this process runs inside the host server, exactly like a pane.
	t.Setenv("TMUX", strings.TrimSpace(string(out))+",1,0")
	t.Setenv(tmux.EnvTmuxSocket, private)

	require.NoError(t, CleanupOrphanedSessions(map[string]bool{}, internalexec.Default{}))

	assert.NoError(t, tmux.CommandOnSocket(ctx, host, "has-session", "-t=loom_decoy").Run(),
		"the enclosing server's loom_* session must survive a sweep aimed at the private socket")
	assert.Error(t, tmux.CommandOnSocket(ctx, private, "has-session", "-t=loom_stray").Run(),
		"the sweep must still clean unclaimed sessions on its own server")
}
