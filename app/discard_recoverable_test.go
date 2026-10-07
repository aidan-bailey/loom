package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiscardRecoverable_WhoseTmuxSessionIsGone: D on a Recoverable orphan
// whose agent is already dead. Its placeholder still holds a tmux session
// object, and closing that fails with "can't find session". Kill used to
// report the failure, which a discard turns into transitionFailedMsg (the
// row kept, "discard failed"); an answered "no such session" is the state a
// kill is after, so the discard now completes: the worktree is removed, the
// branch kept and the row dropped.
func TestDiscardRecoverable_WhoseTmuxSessionIsGone(t *testing.T) {
	for _, tc := range []struct {
		name         string
		otherSession bool
	}{
		{"tmux server running other sessions", true},
		{"no tmux server", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			if tc.otherSession {
				// A server that is up but has no session of the orphan's
				// name: tmux answers "can't find session" rather than
				// "no server running". The bystander must survive.
				require.NoError(t, tmux.Command(context.Background(),
					"new-session", "-d", "-s", "bystander", "sleep", "300").Run())
			}

			repo := t.TempDir()
			runGit(t, repo, "init", "-q")
			require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-qm", "init")

			cfgDir := t.TempDir()
			userDir := filepath.Join(cfgDir, "worktrees", "u")
			require.NoError(t, os.MkdirAll(userDir, 0o755))
			wt := filepath.Join(userDir, "x_18be000000000001")
			runGit(t, repo, "worktree", "add", "-b", "u/x", wt)
			// Uncommitted work is what makes reconcileOrphans surface it
			// as a Recoverable placeholder instead of cleaning it.
			require.NoError(t, os.WriteFile(filepath.Join(wt, "UNSAVED.txt"), []byte("wip"), 0o644))

			m := newTestHome(t)
			summary := m.reconcileOrphans(cfgDir, "true", m.list, nil, cmd2.MakeExecutor())
			require.Equal(t, 1, summary.review, "fixture: the orphan surfaces as Recoverable")
			placeholder := m.list.GetInstanceByTitle("x")
			require.NotNil(t, placeholder)
			require.Equal(t, session.Recoverable, placeholder.GetStatus())
			require.Error(t, tmux.Command(context.Background(), "has-session",
				"-t", tmux.SessionTarget(tmux.ToLoomTmuxName("x"))).Run(), "fixture: no tmux session for the orphan")

			// The path the D key takes (runKillSelected runs these two after
			// its confirmation).
			preAction, killAction := killActionFor(m, placeholder)
			preAction()
			msg := killAction()

			if failed, ok := msg.(transitionFailedMsg); ok {
				t.Fatalf("the discard failed (%s): %v", failed.op, failed.err)
			}
			require.IsType(t, killInstanceMsg{}, msg)
			assert.NoDirExists(t, wt, "the worktree is removed")
			out, err := exec.Command("git", "-C", repo, "branch", "--list", "u/x").Output()
			require.NoError(t, err)
			assert.Contains(t, string(out), "u/x", "the branch is kept")

			_, cmd := m.Update(msg)
			drainCmd(cmd)
			assert.NotContains(t, m.list.GetInstances(), placeholder, "the row is dropped")
			if tc.otherSession {
				assert.NoError(t, tmux.Command(context.Background(), "has-session",
					"-t", tmux.SessionTarget("bystander")).Run(), "an unrelated session is left running")
			}
		})
	}
}
