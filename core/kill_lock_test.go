package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKill_LockedWorktreeKeepsTheRowAndShowsTheRemedy: Kill refuses a
// worktree the user locked (git.ErrWorktreeLocked) before it touches
// anything. The kill used to be reported done, so the row vanished, its
// record was deleted from storage and the lock's remedy never reached the
// user. It now fails like a Recoverable discard: OpFailed reverts the
// status, the record stays and the error, naming `git worktree unlock`, is
// shown, so D can be pressed again. A running agent is left running: its
// tmux session is never closed.
func TestKill_LockedWorktreeKeepsTheRowAndShowsTheRemedy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running bool
	}{
		{"paused", false},
		{"running agent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t)
			wt := filepath.Join(t.TempDir(), "locked_18be000000000001")
			runGit(t, repo, "worktree", "add", "-b", "u/locked", wt)
			runGit(t, repo, "worktree", "lock", "--reason", "keep me", wt)

			inst, err := session.FromInstanceData(session.InstanceData{
				SchemaVersion: session.CurrentSchemaVersion,
				Title:         "locked", Status: session.Paused, Program: "claude",
				Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: wt, BranchName: "u/locked", SessionName: "locked"},
			}, t.TempDir())
			require.NoError(t, err)
			previous := session.Paused
			var mu sync.Mutex
			var ran [][]string
			if tc.running {
				rec := cmd_test.MockCmdExec{
					RunFunc: func(c *exec.Cmd) error {
						mu.Lock()
						ran = append(ran, slices.Clone(c.Args))
						mu.Unlock()
						return nil
					},
					OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
				}
				inst.SetTmuxSession(tmux.NewSessionWithDeps("locked", "claude", fakePtyFactory{t: t}, rec))
				require.NoError(t, inst.TransitionTo(session.Running))
				previous = session.Running
			}
			ws := storedWorkspace(t, "a")
			ws.add(inst)
			require.NoError(t, ws.Storage().SaveInstances([]*session.Instance{inst}))
			m := NewForTest(Options{})
			m.SetWorkspacesForTest(ws)

			pre, job := m.killInst(ws, inst)
			pre()
			res := job()

			failed, ok := res.(OpFailed)
			require.True(t, ok, "a lock-refused kill fails the op, got %T", res)
			assert.Equal(t, "delete", failed.Op)
			assert.Same(t, inst, failed.Instance)
			assert.Equal(t, previous, failed.Previous)
			require.ErrorIs(t, failed.Err, git.ErrWorktreeLocked)
			// The git command that lifts the lock, as LockedError words it:
			// one path, shell-quoted, run from the tree itself.
			assert.Contains(t, failed.Err.Error(), "git -C '"+wt+"' worktree unlock .", "the unlock command reaches the user")
			assert.NotContains(t, failed.Err.Error(), repo, "the repository is not in the way")

			m.Deliver(res)
			events := m.Drain().Events
			assert.Contains(t, events, Event(Reactivated{ID: m.idOf(inst)}), "the TUI gives a reverted kill its client back")
			assert.True(t, slices.ContainsFunc(events, func(e Event) bool {
				n, ok := e.(Notice)
				return ok && n.Err != nil && strings.Contains(n.Err.Error(), "worktree unlock")
			}), "and shows the remedy")
			assert.Contains(t, ws.instances(), inst, "the row stays")
			assert.Equal(t, previous, inst.GetStatus(), "reverted to what it was")
			if tc.running {
				mu.Lock()
				defer mu.Unlock()
				assert.False(t, slices.ContainsFunc(ran, func(argv []string) bool { return slices.Contains(argv, "kill-session") }),
					"the running agent's tmux session is never closed")
			}
			assert.DirExists(t, wt, "the locked worktree is untouched")
			out, err := exec.Command("git", "-C", repo, "branch", "--list", "u/locked").Output()
			require.NoError(t, err)
			assert.Contains(t, string(out), "u/locked", "and so is its branch")
			// DeleteInstance reports ErrInstanceNotFound for a record the
			// kill already removed.
			require.NoError(t, ws.Storage().DeleteInstance("locked"), "the record was not deleted from storage")
		})
	}
}

// TestKill_DiscardsARecoverableOrphanWhoseTmuxSessionIsGone: D on a
// Recoverable orphan whose agent is already dead. Its placeholder still
// holds a tmux session object, and closing that fails with "can't find
// session". Kill used to report the failure, which a discard turns into
// OpFailed (the row kept, "discard failed"); an answered "no such session"
// is the state a kill is after, so the discard now completes: the worktree
// is removed and the branch kept.
func TestKill_DiscardsARecoverableOrphanWhoseTmuxSessionIsGone(t *testing.T) {
	for _, tc := range []struct {
		name         string
		otherSession bool
	}{
		{"tmux server running other sessions", true},
		{"no session of its name", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			if tc.otherSession {
				// A server that is up but has no session of the orphan's
				// name: tmux answers "can't find session" rather than "no
				// server running". The bystander must survive.
				require.NoError(t, tmux.Command(context.Background(),
					"new-session", "-d", "-s", "bystander", "sleep", "300").Run())
				t.Cleanup(func() {
					_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget("bystander")).Run()
				})
			}

			repo := gitRepo(t)
			cfgDir := t.TempDir()
			userDir := filepath.Join(cfgDir, "worktrees", "u")
			require.NoError(t, os.MkdirAll(userDir, 0o755))
			wt := filepath.Join(userDir, "x_18be000000000001")
			runGit(t, repo, "worktree", "add", "-b", "u/x", wt)
			// Uncommitted work is what makes reconcileOrphans surface it as
			// a Recoverable placeholder instead of cleaning it.
			require.NoError(t, os.WriteFile(filepath.Join(wt, "UNSAVED.txt"), []byte("wip"), 0o644))

			ws := storedWorkspace(t, "a")
			m := NewForTest(Options{})
			m.SetWorkspacesForTest(ws)
			summary := m.reconcileOrphans(ws, cfgDir, "true", cmd2.MakeExecutor())
			require.Equal(t, 1, summary.Review, "fixture: the orphan surfaces as Recoverable")
			placeholder := ws.byTitle("x")
			require.NotNil(t, placeholder)
			require.Equal(t, session.Recoverable, placeholder.GetStatus())
			require.Error(t, tmux.Command(context.Background(), "has-session",
				"-t", tmux.SessionTarget(tmux.ToLoomTmuxName("x"))).Run(), "fixture: no tmux session for the orphan")

			pre, job := m.killInst(ws, placeholder)
			pre()
			res := job()

			if failed, ok := res.(OpFailed); ok {
				t.Fatalf("the discard failed (%s): %v", failed.Op, failed.Err)
			}
			require.IsType(t, KillResult{}, res)
			assert.NoDirExists(t, wt, "the worktree is removed")
			out, err := exec.Command("git", "-C", repo, "branch", "--list", "u/x").Output()
			require.NoError(t, err)
			assert.Contains(t, string(out), "u/x", "the branch is kept")
			if tc.otherSession {
				assert.NoError(t, tmux.Command(context.Background(), "has-session",
					"-t", tmux.SessionTarget("bystander")).Run(), "an unrelated session is left running")
			}
		})
	}
}
