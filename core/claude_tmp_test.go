package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preservedTerminalWorkspace lays out a workspace whose only record is a
// workspace terminal written by a newer loom (schema 99): storage preserves
// it undecoded, so loading the workspace creates no terminal and starts no
// tmux session.
func preservedTerminalWorkspace(t *testing.T, name string) config.Workspace {
	t.Helper()
	def := config.Workspace{Name: name, Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	rec, err := json.Marshal(map[string]any{
		"schema_version": 99, "title": name, "program": "claude",
		"is_workspace_terminal": true, "worktree": map[string]any{},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[`+string(rec)+`]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(`{"default_program":"true"}`), 0o644))
	return def
}

// noTmuxExec answers every command with success and no output: the load
// paths' tmux sweeps find nothing to kill.
func noTmuxExec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// TestClaudeTmpSweep_QueuedByEveryLoadPath: both workspace-load paths run
// reconcileOrphans, which queues a sweep of the loaded config dir.
func TestClaudeTmpSweep_QueuedByEveryLoadPath(t *testing.T) {
	t.Run("a workspace served later", func(t *testing.T) {
		def := preservedTerminalWorkspace(t, "ws-sweep")
		m := NewForTest(Options{Registry: &config.WorkspaceRegistry{}, CmdExec: noTmuxExec()})

		_, err := m.ensureLoaded(def)
		require.NoError(t, err)

		assert.Contains(t, m.claudeTmpPending, config.WorkspaceConfigDir(&def))
		assert.True(t, m.maybeClaudeTmpSweep(), "the next health tick dispatches it")
	})

	t.Run("boot", func(t *testing.T) {
		def := preservedTerminalWorkspace(t, "ws-boot")
		m := NewForTest(Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{def}}, CmdExec: noTmuxExec()})

		m.boot()

		assert.Contains(t, m.claudeTmpPending, config.WorkspaceConfigDir(&def))
	})
}

// TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother: a load while a
// sweep runs gets exactly one more pass once it lands.
func TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother(t *testing.T) {
	m := NewForTest(Options{})
	m.requestClaudeTmpSweep(NewWorkspace(WorkspaceParts{}), t.TempDir())
	require.True(t, m.maybeClaudeTmpSweep())
	first := m.Drain().Jobs
	require.Len(t, first, 1)
	assert.Empty(t, m.claudeTmpPending, "dispatching takes the queue")

	m.requestClaudeTmpSweep(NewWorkspace(WorkspaceParts{}), t.TempDir())
	assert.False(t, m.maybeClaudeTmpSweep(), "at most one sweep in flight")

	m.Deliver(first[0]())
	assert.Len(t, m.Drain().Jobs, 1, "the queued load gets one more pass")
	assert.Empty(t, m.claudeTmpPending)
	assert.False(t, m.maybeClaudeTmpSweep(), "and only one")
}

// TestClaudeTmpSweep_SnapshotsTheClaimSet: the claim set is taken when the
// load queues the sweep, on the model's goroutine.
func TestClaudeTmpSweep_SnapshotsTheClaimSet(t *testing.T) {
	cfg := t.TempDir()
	inst, err := session.FromInstanceData(session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "paused",
		Path:          t.TempDir(),
		Branch:        "u/paused",
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:     t.TempDir(),
			WorktreePath: "/wt/paused_18be000000000001",
			BranchName:   "u/paused",
		},
	}, cfg)
	require.NoError(t, err)
	ws := NewWorkspace(WorkspaceParts{})
	ws.add(inst)
	m := NewForTest(Options{})

	m.requestClaudeTmpSweep(ws, cfg)

	assert.True(t, m.claudeTmpPending[cfg].claimed["/wt/paused_18be000000000001"])
}

// TestReconcileOrphans_Locks: a clean orphan left locked "initializing" by
// an add killed long ago is unlocked and cleaned; one the user locked is
// left alone and not counted as cleaned.
func TestReconcileOrphans_Locks(t *testing.T) {
	repo := gitRepo(t)
	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	staleWT := filepath.Join(userDir, "stale_18be000000000001")
	keptWT := filepath.Join(userDir, "kept_18be000000000002")
	runGit(t, repo, "worktree", "add", "-b", "u/stale", staleWT)
	runGit(t, repo, "worktree", "add", "-b", "u/kept", keptWT)
	gitfile, err := os.ReadFile(filepath.Join(staleWT, ".git"))
	require.NoError(t, err)
	lock := filepath.Join(strings.TrimSpace(strings.TrimPrefix(string(gitfile), "gitdir:")), "locked")
	require.NoError(t, os.WriteFile(lock, []byte("initializing"), 0o644))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(lock, old, old))
	runGit(t, repo, "worktree", "lock", "--reason", "keep me", keptWT)

	summary := NewForTest(Options{}).reconcileOrphans(NewWorkspace(WorkspaceParts{}), cfgDir, "true", cmd2.MakeExecutor())

	assert.Equal(t, 1, summary.Cleaned, "only the stale-locked orphan is cleaned")
	assert.Zero(t, summary.Review)
	assert.NoDirExists(t, staleWT)
	assert.DirExists(t, keptWT, "a lock the user set is respected")
}
