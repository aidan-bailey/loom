package session

import (
	"errors"
	"os"
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
			if cmd_test.TmuxSubcommand(c.Args) == "kill-session" {
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

	t.Run("worktree intact: paused, with no crash-restart under the taken name", func(t *testing.T) {
		var killed []string
		intact := filepath.Join(t.TempDir(), "api_789")
		require.NoError(t, os.MkdirAll(intact, 0o755))
		data := InstanceData{Title: "api", Path: base, Status: Running, Program: "claude",
			Worktree: GitWorktreeData{WorktreePath: intact}}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", elsewhere), &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus())
		assert.False(t, inst.CrashRecovered(), "a restart could only fail on the name, or adopt the other session")
		assert.Empty(t, killed)
	})

	t.Run("workspace terminal: a same-named session elsewhere is not restored", func(t *testing.T) {
		var killed []string
		repo := filepath.Join(base, "repo-b")
		data := InstanceData{Title: "api", Path: repo, Status: Running, Program: "claude", IsWorkspaceTerminal: true}
		inst, err := ReconcileAndRestore(data, "", liveServer(listing("loom_api", elsewhere), &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus(), "paused: its workspace's first open decides (ensureTerminal)")
		assert.False(t, inst.CrashRecovered(), "no relaunch under a name known to be taken")
		assert.Empty(t, killed)
	})

	t.Run("workspace terminal: an agent's session under its worktrees dir is not restored", func(t *testing.T) {
		// An agent titled after the workspace holds the terminal's name, and
		// its worktree lies inside the repository (config dir <repo>/.loom):
		// a terminal's own session starts in the repository itself.
		var killed []string
		repo := filepath.Join(base, "repo-b")
		cfgDir := filepath.Join(repo, ".loom")
		agentWT := filepath.Join(cfgDir, "worktrees", "api_1")
		data := InstanceData{Title: "api", Path: repo, Status: Running, Program: "claude", IsWorkspaceTerminal: true}
		inst, err := ReconcileAndRestore(data, cfgDir, liveServer(listing("loom_api", agentWT), &killed))
		require.NoError(t, err)
		assert.Equal(t, Paused, inst.GetStatus(), "not restored onto the agent's session")
		assert.False(t, inst.CrashRecovered())
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

// HeldElsewhere is the check before a kill or replacement of whatever runs
// under a record's name: unlike reconcile's, it fails closed on what tmux
// answers, so only a session started where the record's own starts, or
// none at all, is free. A workspace terminal's starts in its repository
// itself; an agent's in its worktree or below. A listing tmux leaves
// unanswered is no answer (an error), not a held name.
func TestHeldElsewhere_OnlyTheRecordsOwnSessionIsFree(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo-b")
	terminal := SessionHome{Dir: repo, Exact: true}
	agentWT := filepath.Join(repo, ".loom", "worktrees", "api_1")
	agent := SessionHome{Dir: agentWT}
	listed := func(list string, err error) cmd_test.MockCmdExec {
		return cmd_test.MockCmdExec{OutputFunc: func(*exec.Cmd) ([]byte, error) { return []byte(list), err }}
	}
	cases := []struct {
		name string
		home SessionHome
		exec cmd_test.MockCmdExec
		want bool
	}{
		{"no such session", terminal, listed(listing("loom_other", repo), nil), false},
		{"no server", terminal, listed("", errors.New("no server running on /tmp/tmux-1/x")), false},
		{"a terminal's, in its repository", terminal, listed(listing("loom_api", repo), nil), false},
		{"started elsewhere", terminal, listed(listing("loom_api", filepath.Join(base, "repo-a")), nil), true},
		{"an agent's, under the terminal's repository", terminal, listed(listing("loom_api", agentWT), nil), true},
		{"below the terminal's repository", terminal, listed(listing("loom_api", filepath.Join(repo, "sub")), nil), true},
		{"an agent's, in its worktree", agent, listed(listing("loom_api", agentWT), nil), false},
		{"an agent's, below its worktree", agent, listed(listing("loom_api", filepath.Join(agentWT, "sub")), nil), false},
		{"no start directory", terminal, listed("loom_api\t\n", nil), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held, err := HeldElsewhere("api", tc.home, tc.exec)
			require.NoError(t, err)
			assert.Equal(t, tc.want, held)
		})
	}
	held, err := HeldElsewhere("api", SessionHome{}, listed(listing("loom_api", repo), nil))
	require.NoError(t, err)
	assert.True(t, held, "a record with no directory proves nothing")

	_, err = HeldElsewhere("api", terminal, listed("", errors.New("signal: killed")))
	assert.Error(t, err, "an unreadable listing is no answer")
}
