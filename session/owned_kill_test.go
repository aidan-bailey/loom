package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKillOwnedTmuxSession: activateWorkspace clears a leftover session
// under the workspace terminal's title before starting a new one. The
// title is just the workspace's name, and the server is shared, so a
// session of that name started anywhere but this workspace's roots may be
// another loom's live terminal: it is left alone.
func TestKillOwnedTmuxSession(t *testing.T) {
	base := t.TempDir()
	mine := workspaceAt(t, filepath.Join(base, "api"))
	elsewhere := filepath.Join(base, "other", "api")
	require.NoError(t, os.MkdirAll(elsewhere, 0o755))

	t.Run("owned: killed by exact name", func(t *testing.T) {
		srv := &sweepServer{listing: listing(
			"loom_api-v2", mine.RepoPath, // a prefix sibling is not the session
			"loom_api", mine.RepoPath,
		)}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		require.NoError(t, err)
		assert.True(t, killed)
		assert.Equal(t, []string{"=loom_api"}, srv.killed)
	})

	t.Run("started outside the workspace: left running", func(t *testing.T) {
		srv := &sweepServer{listing: listing("loom_api", elsewhere)}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		assert.Error(t, err)
		assert.False(t, killed)
		assert.Empty(t, srv.killed)
	})

	t.Run("unreadable directory: left running", func(t *testing.T) {
		srv := &sweepServer{listing: "loom_api\t\n"}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		assert.Error(t, err)
		assert.False(t, killed)
		assert.Empty(t, srv.killed)
	})

	t.Run("no such session: nothing to do", func(t *testing.T) {
		srv := &sweepServer{listing: listing("loom_api-v2", mine.RepoPath)}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		assert.NoError(t, err)
		assert.False(t, killed)
		assert.Empty(t, srv.killed)
	})

	t.Run("no server: nothing to do", func(t *testing.T) {
		srv := &sweepServer{listErr: errors.New("no server running on /tmp/tmux-1000/default")}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		assert.NoError(t, err)
		assert.False(t, killed)
	})

	t.Run("listing failed: fail closed", func(t *testing.T) {
		srv := &sweepServer{listing: listing("loom_api", mine.RepoPath), listErr: context.DeadlineExceeded}
		killed, err := KillOwnedTmuxSession("api", ownedScope(mine), srv.executor())
		assert.Error(t, err)
		assert.False(t, killed)
		assert.Empty(t, srv.killed)
	})
}

// TestCleanupOrphanedSessions_SkipsUntargetableNames: a session some
// older loom named with ':' or '.' cannot be targeted exactly, and killing
// it by name reaches another: kill-session -t=loom_feat:0 kills loom_feat.
func TestCleanupOrphanedSessions_SkipsUntargetableNames(t *testing.T) {
	mine := workspaceAt(t, filepath.Join(t.TempDir(), "mine"))
	wt := filepath.Join(mine.ConfigDir, "worktrees")

	killed := sweep(t, map[string]bool{"feat": true}, ownedScope(mine), listing(
		"loom_feat", filepath.Join(wt, "feat"), // claimed, live
		"loom_feat:0", filepath.Join(wt, "feat0"),
		"loom_v1.2", filepath.Join(wt, "v12"),
		"loom_stray", filepath.Join(wt, "stray"),
	))

	assert.Equal(t, []string{"=loom_stray"}, killed)
}

// TestIsNoTmuxServer_RealTmux pins the "no server" classification to what
// the installed tmux actually prints, for a socket with no server at all
// and for a stale socket file whose server died.
func TestIsNoTmuxServer_RealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH")
	}
	dir, err := os.MkdirTemp("", "lt")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")

	_, err = tmux.CommandOnSocket(context.Background(), fmt.Sprintf("lt-none-%d", os.Getpid()), "ls").Output()
	require.Error(t, err)
	assert.True(t, isNoTmuxServer(err), "no socket: %v", err)

	// A killed server leaves its socket file behind: the stale case. While
	// it is still exiting tmux says "server exited unexpectedly", which is
	// deliberately not "no server" (its state is not known yet).
	sock := fmt.Sprintf("lt-gone-%d", os.Getpid())
	require.NoError(t, tmux.CommandOnSocket(context.Background(), sock, "new-session", "-d", "-s", "x", "sleep 30").Run())
	require.NoError(t, tmux.CommandOnSocket(context.Background(), sock, "kill-server").Run())
	require.Eventually(t, func() bool {
		_, err = tmux.CommandOnSocket(context.Background(), sock, "ls").Output()
		return isNoTmuxServer(err)
	}, 5*time.Second, 50*time.Millisecond, "killed server: %v", err)

	assert.False(t, isNoTmuxServer(context.DeadlineExceeded))
	assert.False(t, isNoTmuxServer(errors.New("error connecting to /x (Permission denied)")))
	assert.False(t, isNoTmuxServer(nil))
}
