package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKillAction_LockedWorktreeKeepsTheRowAndShowsTheRemedy: Kill refuses a
// worktree the user locked (git.ErrWorktreeLocked) and removes nothing. The
// kill action used to log that and report the kill done, so the row
// vanished, its record was deleted from storage and the lock's remedy never
// reached the user. It now fails the transition like a Recoverable discard
// does: the status is restored (and, when Kill could not close the agent's
// tmux session either, that agent's pane client with it),
// the record stays in storage and the error, naming `git worktree unlock`,
// is shown, so D can be pressed again.
func TestKillAction_LockedWorktreeKeepsTheRowAndShowsTheRemedy(t *testing.T) {
	for _, tc := range []struct {
		name string
		// running builds the instance on a live mock tmux session: an agent
		// whose tmux session Kill's close did not end. In production a
		// lock-refused Kill has usually closed it already.
		running bool
	}{
		{"paused", false},
		{"running agent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			repo := t.TempDir()
			runGit(t, repo, "init", "-q")
			require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-qm", "init")
			wt := filepath.Join(t.TempDir(), "locked_18be000000000001")
			runGit(t, repo, "worktree", "add", "-b", "u/locked", wt)
			runGit(t, repo, "worktree", "lock", "--reason", "keep me", wt)

			m := newTestHome(t)
			inst, err := session.FromInstanceData(session.InstanceData{
				SchemaVersion: session.CurrentSchemaVersion,
				Title:         "locked", Status: session.Paused, Program: "claude",
				Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: wt, BranchName: "u/locked", SessionName: "locked"},
			}, t.TempDir())
			require.NoError(t, err)
			previous := session.Paused
			if tc.running {
				inst.SetTmuxSession(tmux.NewSessionWithDeps("locked", "claude", fakePtyFactory{t: t}, aliveCmdExecForTest()))
				require.NoError(t, inst.TransitionTo(session.Running))
				previous = session.Running
			}
			m.list.AddInstance(inst)
			require.NoError(t, m.storage.SaveInstances([]*session.Instance{inst}))
			name := inst.Pane().TmuxSessionName()

			preAction, killAction := killActionFor(m, inst)
			preAction()
			msg := killAction()

			failed, ok := msg.(transitionFailedMsg)
			require.True(t, ok, "a lock-refused kill fails the transition, got %T", msg)
			assert.Equal(t, "delete", failed.op)
			assert.Same(t, inst, failed.inst)
			assert.Equal(t, previous, failed.previousStatus)
			require.ErrorIs(t, failed.err, git.ErrWorktreeLocked)
			// The git command that lifts the lock, as LockedError words it:
			// one path, shell-quoted, run from the tree itself.
			assert.Contains(t, failed.err.Error(), "git -C '"+wt+"' worktree unlock .",
				"the unlock command reaches the user")
			assert.NotContains(t, failed.err.Error(), repo, "the repository is not in the way")

			// The returned Cmds are not run: the error toast's timer sleeps
			// for as long as the message is long.
			_, _ = m.Update(msg)
			assert.Contains(t, m.errBox.String(), "worktree unlock", "and is shown")
			assert.Contains(t, m.list.GetInstances(), inst, "the row stays")
			assert.Equal(t, previous, inst.GetStatus(), "reverted to what it was")
			if tc.running {
				assert.True(t, m.panes.Alive(name), "the revert gives a running agent its pane client back")
			}
			assert.DirExists(t, wt, "the locked worktree is untouched")
			out, err := exec.Command("git", "-C", repo, "branch", "--list", "u/locked").Output()
			require.NoError(t, err)
			assert.Contains(t, string(out), "u/locked", "and so is its branch")
			// DeleteInstance reports ErrInstanceNotFound for a record the
			// kill action already removed.
			require.NoError(t, m.storage.DeleteInstance("locked"), "the record was not deleted from storage")
		})
	}
}
