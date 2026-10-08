package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startHolder starts title's agent session and its terminal-pane shell in
// dir: another workspace's live agent, holding the name of a record this
// workspace has too.
func startHolder(t *testing.T, title, dir string) {
	t.Helper()
	startForeignSession(t, title, dir)
	startForeignSession(t, tmux.TerminalSessionName(title), dir)
}

// assertHolderRuns checks that the holder's two sessions still run where
// they started.
func assertHolderRuns(t *testing.T, title, dir string) {
	t.Helper()
	assert.Equal(t, canonical(t, dir), canonical(t, sessionPath(t, title)), "the other workspace's agent still runs")
	assert.Equal(t, canonical(t, dir), canonical(t, sessionPath(t, tmux.TerminalSessionName(title))), "and so does its terminal-pane shell")
}

// sessionRuns reports whether title's tmux session runs on the test's
// private server.
func sessionRuns(title string) bool {
	return tmux.Command(context.Background(), "has-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(title))).Run() == nil
}

// The positive control of the held-name guard: a record's own session (an
// agent's starts in its worktree) is closed by its kill, with its
// terminal-pane shell, and its worktree removed.
func TestKill_ClosesTheRecordsOwnSession(t *testing.T) {
	isolateTmux(t)
	repo := gitRepo(t)
	inst := pausedWorktreeInst(t, repo, "api", "b/api")
	wt, err := inst.GetGitWorktree()
	require.NoError(t, err)
	startForeignSession(t, "api", wt.GetWorktreePath()) // its own agent, running
	startForeignSession(t, tmux.TerminalSessionName("api"), wt.GetWorktreePath())
	ws := storedWorkspace(t, "b")
	ws.add(inst)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)

	pre, job := m.killInst(ws, inst)
	pre()
	res := job()

	if failed, ok := res.(OpFailed); ok {
		t.Fatalf("the kill failed: %v", failed.Err)
	}
	require.IsType(t, KillResult{}, res)
	assert.False(t, sessionRuns("api"), "its agent's session is closed")
	assert.False(t, sessionRuns(tmux.TerminalSessionName("api")), "and its terminal-pane shell")
	assert.NoDirExists(t, wt.GetWorktreePath())
}

// The same for the discard of an orphan placeholder whose agent still runs
// in its worktree.
func TestDiscard_ClosesALiveOrphansOwnSession(t *testing.T) {
	isolateTmux(t)
	repo := gitRepo(t)
	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	wt := filepath.Join(userDir, "x_18be000000000001")
	runGit(t, repo, "worktree", "add", "-b", "u/x", wt)
	startForeignSession(t, "x", wt) // its own agent, still running
	startForeignSession(t, tmux.TerminalSessionName("x"), wt)

	ws := storedWorkspace(t, "a")
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	summary := m.reconcileOrphans(ws, cfgDir, "true", cmd2.MakeExecutor())
	require.Equal(t, 1, summary.Review, "fixture: the live orphan surfaces as Recoverable")
	placeholder := ws.byTitle("x")
	require.NotNil(t, placeholder)

	pre, job := m.killInst(ws, placeholder)
	pre()
	res := job()

	if failed, ok := res.(OpFailed); ok {
		t.Fatalf("the discard failed (%s): %v", failed.Op, failed.Err)
	}
	require.IsType(t, KillResult{}, res)
	assert.False(t, sessionRuns("x"), "its agent's session is closed")
	assert.False(t, sessionRuns(tmux.TerminalSessionName("x")), "and its terminal-pane shell")
	assert.NoDirExists(t, wt)
}

// A record whose name another workspace's session holds (reconcile paused
// it for that reason) is killed without touching that session: the kill
// cleans up what is the record's own, its worktree, and leaves the other
// workspace's agent and its terminal-pane shell running.
func TestKill_LeavesASessionHoldingTheNameElsewhereRunning(t *testing.T) {
	isolateTmux(t)
	repo := gitRepo(t)
	inst := pausedWorktreeInst(t, repo, "api", "b/api")
	wt, err := inst.GetGitWorktree()
	require.NoError(t, err)
	elsewhere := t.TempDir()
	startHolder(t, "api", elsewhere)
	ws := storedWorkspace(t, "b")
	ws.add(inst)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)

	pre, job := m.killInst(ws, inst)
	pre()
	res := job()

	if failed, ok := res.(OpFailed); ok {
		t.Fatalf("the kill failed: %v", failed.Err)
	}
	require.IsType(t, KillResult{}, res)
	assertHolderRuns(t, "api", elsewhere)
	assert.NoDirExists(t, wt.GetWorktreePath(), "its own worktree is cleaned up")
}

// Discarding an orphan placeholder (a kill of a Recoverable row) whose name
// another workspace's session holds leaves that session running too.
func TestDiscard_LeavesASessionHoldingTheNameElsewhereRunning(t *testing.T) {
	isolateTmux(t)
	repo := gitRepo(t)
	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))
	wt := filepath.Join(userDir, "x_18be000000000001")
	runGit(t, repo, "worktree", "add", "-b", "u/x", wt)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "UNSAVED.txt"), []byte("wip"), 0o644))
	elsewhere := t.TempDir()
	startHolder(t, "x", elsewhere)

	ws := storedWorkspace(t, "a")
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	summary := m.reconcileOrphans(ws, cfgDir, "true", cmd2.MakeExecutor())
	require.Equal(t, 1, summary.Review, "fixture: the orphan surfaces as Recoverable")
	placeholder := ws.byTitle("x")
	require.NotNil(t, placeholder)
	require.Equal(t, session.Recoverable, placeholder.GetStatus())

	pre, job := m.killInst(ws, placeholder)
	pre()
	res := job()

	if failed, ok := res.(OpFailed); ok {
		t.Fatalf("the discard failed (%s): %v", failed.Op, failed.Err)
	}
	require.IsType(t, KillResult{}, res)
	assertHolderRuns(t, "x", elsewhere)
	assert.NoDirExists(t, wt, "its worktree is removed")
}

// Resuming a record whose name another workspace's session holds is
// refused: reattaching would make that session this record's, and no
// relaunch can take the name while it runs. Nothing changes.
func TestResume_RefusesASessionHoldingTheNameElsewhere(t *testing.T) {
	isolateTmux(t)
	repo := gitRepo(t)
	inst := pausedWorktreeInst(t, repo, "api", "b/api")
	wt, err := inst.GetGitWorktree()
	require.NoError(t, err)
	elsewhere := t.TempDir()
	startHolder(t, "api", elsewhere)

	err = inst.Resume(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "another session's")
	assertHolderRuns(t, "api", elsewhere)
	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.DirExists(t, wt.GetWorktreePath(), "its worktree is untouched")
}

// starvedTmux is a tmux server that reads every session alive but whose
// listing never answers (killed at its deadline under load); it records
// the targets of the kill-session commands it is sent.
func starvedTmux(killed *[]string) cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				*killed = append(*killed, c.Args[len(c.Args)-1])
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, errors.New("signal: killed") },
	}
}

// When tmux does not answer whose the session under a record's name is, a
// kill or a resume can't tell its own session from another workspace's:
// both refuse and change nothing (no session closed, nothing reattached).
func TestKillAndResume_RefuseWhenTmuxCannotSayWhoseTheSessionIs(t *testing.T) {
	t.Run("kill", func(t *testing.T) {
		var killed []string
		inst, err := session.NewInstance(session.InstanceOptions{Title: "api", Path: t.TempDir(), Program: "aider"})
		require.NoError(t, err)
		inst.SetTmuxSession(tmux.NewSessionWithDeps("api", "aider", fakePtyFactory{t: t}, starvedTmux(&killed)))
		require.NoError(t, inst.EnsureRunning())

		err = inst.Kill()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "nothing was changed")
		assert.Empty(t, killed, "no session closed")
		assert.True(t, inst.Started(), "the instance stays as it was, for a retry")
	})

	t.Run("resume", func(t *testing.T) {
		var killed []string
		inst := pausedWorktreeInst(t, gitRepo(t), "api", "b/api")
		inst.SetTmuxSession(tmux.NewSessionWithDeps("api", "claude", fakePtyFactory{t: t}, starvedTmux(&killed)))

		err := inst.Resume(nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not answer")
		assert.Equal(t, session.Paused, inst.GetStatus())
		assert.Empty(t, killed)
	})
}
