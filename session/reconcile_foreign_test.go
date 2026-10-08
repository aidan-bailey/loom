package session

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveServer is a tmux server on which the record's session is alive,
// listed with the given start directory; it records kill-session targets.
func liveServer(list string, killed *[]string) cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if len(c.Args) >= 2 && c.Args[1] == "kill-session" {
				*killed = append(*killed, c.Args[len(c.Args)-1])
			}
			return nil // has-session: alive
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte(list), nil },
	}
}

// Session names are unique per tmux server, not per workspace: another
// workspace's live session can carry this record's name. Reconcile must
// neither kill it nor restore this record onto it.
func TestReconcile_ASessionStartedElsewhereIsNotTheRecords(t *testing.T) {
	base := t.TempDir()
	own := filepath.Join(base, "repo-b", ".loom", "worktrees", "api_123")
	elsewhere := filepath.Join(base, "repo-a", ".loom", "worktrees", "api_456")

	t.Run("worktree gone: paused, the other session left running", func(t *testing.T) {
		var killed []string
		data := InstanceData{Title: "api", Path: base, Status: Running, Program: "claude",
			Worktree: GitWorktreeData{WorktreePath: own}}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", elsewhere), &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus())
		assert.Empty(t, killed, "another workspace's session is never killed")
	})

	t.Run("worktree gone, started in its own worktree: killed as before", func(t *testing.T) {
		var killed []string
		data := InstanceData{Title: "api", Path: base, Status: Running, Program: "claude",
			Worktree: GitWorktreeData{WorktreePath: own}}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", own), &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus())
		assert.Equal(t, []string{"=loom_api"}, killed)
	})

	t.Run("start directory unreadable: the old behaviour", func(t *testing.T) {
		var killed []string
		data := InstanceData{Title: "api", Path: base, Status: Running, Program: "claude",
			Worktree: GitWorktreeData{WorktreePath: own}}
		inst, err := ReconcileAndRestore(data, "", liveServer("loom_api\t\n", &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus())
		assert.Equal(t, []string{"=loom_api"}, killed)
	})

	t.Run("workspace terminal: a same-named session elsewhere is not restored", func(t *testing.T) {
		var killed []string
		repo := filepath.Join(base, "repo-b")
		data := InstanceData{Title: "api", Path: repo, Status: Running, Program: "claude", IsWorkspaceTerminal: true}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", elsewhere), &killed))
		require.NoError(t, err)
		assert.True(t, inst.CrashRecovered(), "treated as dead: the load relaunches it, which fails on the name")
		assert.Empty(t, killed)
	})

	t.Run("workspace terminal in its own repo: restored", func(t *testing.T) {
		var killed []string
		repo := filepath.Join(base, "repo-b")
		data := InstanceData{Title: "api", Path: repo, Status: Running, Program: "claude", IsWorkspaceTerminal: true}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", repo), &killed))
		require.NoError(t, err)
		assert.False(t, inst.CrashRecovered())
		assert.Empty(t, killed)
	})
}
