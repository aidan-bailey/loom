package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
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
	// Both sessions start in a directory the sweep owns, so only the
	// socket targeting can spare the decoy.
	mine := workspaceAt(t, t.TempDir())
	wt := filepath.Join(mine.ConfigDir, "worktrees")
	require.NoError(t, tmux.CommandOnSocket(ctx, host, "new-session", "-d", "-s", "loom_decoy", "-c", wt, "sleep 120").Run())
	require.NoError(t, tmux.CommandOnSocket(ctx, private, "new-session", "-d", "-s", "loom_stray", "-c", wt, "sleep 120").Run())

	out, err := tmux.CommandOnSocket(ctx, host, "display-message", "-p", "-t", "loom_decoy", "#{socket_path}").Output()
	require.NoError(t, err)
	// Pretend this process runs inside the host server, exactly like a pane.
	t.Setenv("TMUX", strings.TrimSpace(string(out))+",1,0")
	t.Setenv(tmux.EnvTmuxSocket, private)

	_, err = CleanupOrphanedSessions(map[string]bool{}, ownedScope(mine), internalexec.Default{})
	require.NoError(t, err)

	assert.NoError(t, tmux.CommandOnSocket(ctx, host, "has-session", "-t=loom_decoy").Run(),
		"the enclosing server's loom_* session must survive a sweep aimed at the private socket")
	assert.Error(t, tmux.CommandOnSocket(ctx, private, "has-session", "-t=loom_stray").Run(),
		"the sweep must still clean unclaimed sessions on its own server")
}

// TestCleanupOrphanedSessions_RealTmuxKillsOnlyOwnedSessions replays the
// incident on a private server: two looms share it, and this process loaded
// only its own workspace. The other workspace's live sessions are just as
// unclaimed as a real orphan; only the start directory tells them apart.
// session_path is the directory new-session was given, so a shell that
// later cds elsewhere keeps its owner (pane_current_path would not).
func TestCleanupOrphanedSessions_RealTmuxKillsOnlyOwnedSessions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	sock := fmt.Sprintf("loomtest-own-%d", time.Now().UnixNano())
	t.Setenv("TMUX", "")
	t.Setenv(tmux.EnvTmuxSocket, sock)
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })

	base := t.TempDir()
	mine := workspaceAt(t, filepath.Join(base, "mine"))
	theirs := workspaceAt(t, filepath.Join(base, "theirs"))
	mineWT := filepath.Join(mine.ConfigDir, "worktrees", "stale")
	theirsWT := filepath.Join(theirs.ConfigDir, "worktrees", "live")
	require.NoError(t, os.MkdirAll(mineWT, 0o755))
	require.NoError(t, os.MkdirAll(theirsWT, 0o755))

	start := func(name, dir, command string) {
		t.Helper()
		require.NoError(t, tmux.CommandOnSocket(ctx, sock, "new-session", "-d", "-s", name, "-c", dir, command).Run())
	}
	start("loom_stale", mineWT, "sleep 120")
	start("loom_theirs-agent", theirsWT, "sleep 120")
	start("loom_theirs", theirs.RepoPath, "sleep 120")
	// Started in the other workspace, then cd'd into this one's worktree.
	start("loom_term_theirs-agent", theirsWT, "cd "+mineWT+" && exec sleep 120")
	// Started here, then cd'd away: still this workspace's orphan.
	start("loom_term_stale", mineWT, "cd "+theirsWT+" && exec sleep 120")
	require.Eventually(t, func() bool {
		out, err := tmux.CommandOnSocket(ctx, sock, "display-message", "-p", "-t", "=loom_term_theirs-agent:", "#{pane_current_path}").Output()
		return err == nil && strings.TrimSpace(string(out)) == mineWT
	}, 5*time.Second, 20*time.Millisecond, "precondition: the wandering shell's current path is in this workspace")

	scope := NewSweepScope([]*config.WorkspaceContext{mine}, &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "mine", Path: mine.RepoPath}, {Name: "theirs", Path: theirs.RepoPath},
	}})
	_, err := CleanupOrphanedSessions(map[string]bool{}, scope, internalexec.Default{})
	require.NoError(t, err)

	alive := func(name string) bool {
		return tmux.CommandOnSocket(ctx, sock, "has-session", "-t="+name).Run() == nil
	}
	for _, name := range []string{"loom_theirs-agent", "loom_theirs", "loom_term_theirs-agent"} {
		assert.True(t, alive(name), "%s belongs to the other workspace and must survive", name)
	}
	for _, name := range []string{"loom_stale", "loom_term_stale"} {
		assert.False(t, alive(name), "%s is this workspace's orphan and must be swept", name)
	}
}
