package session

import (
	"os/exec"
	"testing"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/assert"
)

func TestCheckTmuxAlive_SessionExists(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil }, // has-session succeeds
	}
	assert.True(t, CheckTmuxAlive("test-session", cmdExec))
}

func TestCheckTmuxAlive_SessionDead(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			return &exec.ExitError{}
		},
	}
	assert.False(t, CheckTmuxAlive("test-session", cmdExec))
}

func TestCheckWorktreeExists_Exists(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, CheckWorktreeExists(dir))
}

func TestCheckWorktreeExists_Missing(t *testing.T) {
	assert.False(t, CheckWorktreeExists("/nonexistent/path/worktree"))
}

func TestReconcileInstance_DeadTmux_MarkedPaused(t *testing.T) {
	// Simulate: instance was Running, tmux is dead, no worktree
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return &exec.ExitError{} }, // tmux dead
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	data := InstanceData{
		Title:   "dead-session",
		Path:    t.TempDir(),
		Branch:  "test-branch",
		Status:  Running,
		Program: "claude",
	}

	instance, err := ReconcileAndRestore(data, "", cmdExec)
	assert.NoError(t, err)
	assert.Equal(t, Paused, instance.GetStatus())
}

func TestReconcileInstance_Paused_NoChange(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	data := InstanceData{
		Title:   "paused-session",
		Path:    t.TempDir(),
		Branch:  "test-branch",
		Status:  Paused,
		Program: "claude",
	}

	instance, err := ReconcileAndRestore(data, "", cmdExec)
	assert.NoError(t, err)
	assert.Equal(t, Paused, instance.GetStatus())
}

func TestReconcileInstance_WsTerminal_DeadTmux(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	data := InstanceData{
		Title:               "ws-terminal",
		Path:                t.TempDir(),
		Status:              Running,
		Program:             "claude",
		IsWorkspaceTerminal: true,
	}

	instance, err := ReconcileAndRestore(data, "", cmdExec)
	assert.NoError(t, err)
	assert.True(t, instance.CrashRecovered)
}

func TestCleanupOrphanedSessions(t *testing.T) {
	killedSessions := []string{}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			// Track kill-session calls
			for i, arg := range c.Args {
				if arg == "kill-session" && i+2 < len(c.Args) {
					killedSessions = append(killedSessions, c.Args[i+2])
				}
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			// Simulate tmux ls output: two cs sessions, one claimed, one orphaned
			return []byte("claudesquad_claimed: 1 windows\nclaudesquad_orphan: 1 windows\nother_session: 1 windows\n"), nil
		},
	}

	claimedTitles := map[string]bool{"claimed": true}
	err := CleanupOrphanedSessions(claimedTitles, cmdExec)
	assert.NoError(t, err)
	assert.Contains(t, killedSessions, "claudesquad_orphan")
	assert.NotContains(t, killedSessions, "claudesquad_claimed")
	assert.NotContains(t, killedSessions, "other_session")
}

func TestCleanupOrphanedSessions_NoTmux(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			return nil, &exec.ExitError{} // no tmux server
		},
	}
	err := CleanupOrphanedSessions(nil, cmdExec)
	assert.NoError(t, err)
}

func TestDetermineRecoveryAction(t *testing.T) {
	tests := []struct {
		name      string
		status    Status
		tmuxAlive bool
		wtExists  bool
		isWsTerm  bool
		expected  RecoveryAction
	}{
		{"paused_no_change", Paused, false, false, false, ActionNoChange},
		{"running_all_healthy", Running, true, true, false, ActionRestore},
		{"running_tmux_dead_wt_exists", Running, false, true, false, ActionRestart},
		{"running_tmux_dead_wt_gone", Running, false, false, false, ActionMarkPaused},
		{"running_tmux_alive_wt_gone", Running, true, false, false, ActionKillAndPause},
		{"ws_terminal_tmux_dead", Running, false, false, true, ActionRestartWsTerminal},
		{"ready_tmux_dead", Ready, false, true, false, ActionRestart},
		{"prompting_tmux_dead", Prompting, false, false, false, ActionMarkPaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := DetermineRecoveryAction(tt.status, tt.tmuxAlive, tt.wtExists, tt.isWsTerm)
			assert.Equal(t, tt.expected, action)
		})
	}
}
