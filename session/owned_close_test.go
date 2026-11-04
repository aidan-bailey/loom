package session

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/tmux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startedOn starts an instance titled title on a fake tmux server, then
// answers its session listings with listing (one "name\tdir" line per
// session) and returns the instance, its server and its worktree.
func startedOn(t *testing.T, title string, listing func(wt string) (string, error)) (*Instance, *fakeTmuxServer, string) {
	t.Helper()
	srv := &fakeTmuxServer{}
	inst, _, _ := newStartFixtureOn(t, title, srv)
	require.NoError(t, inst.Start(true))
	wt := inst.getGitWorktree().GetWorktreePath()
	runner := srv.runner()
	runner.OutputFunc = func(*exec.Cmd) ([]byte, error) {
		out, err := listing(wt)
		return []byte(out), err
	}
	inst.setTmuxSession(tmux.NewSessionWithDeps(title, "claude", srv, runner))
	return inst, srv, wt
}

func killed(srv *fakeTmuxServer, rawName string) bool {
	return srv.ran("kill-session", tmux.SessionTarget(tmux.ToLoomTmuxName(rawName)))
}

// TestKillAndPause_CloseOnlyTheirOwnTerminalShell: the terminal pane's
// shell is a tmux session named after the title, which another
// workspace's session of the same title holds as well. Kill and Pause
// closed it by name, ending the other workspace's shell (a dev server, say)
// whenever its agent was not running. They now close it only when it was
// started in the record's own worktree, as the agent's session is judged.
func TestKillAndPause_CloseOnlyTheirOwnTerminalShell(t *testing.T) {
	for _, op := range []struct {
		name string
		do   func(*Instance) error
	}{
		{"kill", func(i *Instance) error { return i.Kill() }},
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
	} {
		t.Run(op.name+": another workspace's shell is left", func(t *testing.T) {
			inst, srv, _ := startedOn(t, "api", func(string) (string, error) {
				return "loom_term_api\t/another/workspace/api\n", nil
			})

			require.NoError(t, op.do(inst))

			assert.False(t, killed(srv, tmux.TerminalSessionName("api")))
		})
		t.Run(op.name+": its own shell is closed", func(t *testing.T) {
			inst, srv, _ := startedOn(t, "api", func(wt string) (string, error) {
				return "loom_api\t" + wt + "\nloom_term_api\t" + wt + "\n", nil
			})

			require.NoError(t, op.do(inst))

			assert.True(t, killed(srv, tmux.TerminalSessionName("api")))
		})
	}
}

// TestPause_LeavesASessionHoldingTheNameElsewhereRunning: Pause closed the
// agent's session by name without asking whose it is, as Kill does. A
// session another workspace started under the name is left running, and
// the record, whose own agent cannot be running under a name another
// session holds, is paused. When tmux cannot list its sessions, whose the
// name is cannot be told, and the pause is refused with nothing changed.
func TestPause_LeavesASessionHoldingTheNameElsewhereRunning(t *testing.T) {
	t.Run("held elsewhere", func(t *testing.T) {
		inst, srv, _ := startedOn(t, "api", func(string) (string, error) {
			return "loom_api\t/another/workspace/api\n", nil
		})

		require.NoError(t, inst.Pause(nil))

		assert.False(t, killed(srv, "api"), "the other workspace's agent runs on")
		assert.Equal(t, Paused, inst.GetStatus())
	})
	t.Run("tmux does not answer", func(t *testing.T) {
		inst, srv, _ := startedOn(t, "api", func(string) (string, error) {
			return "", errors.New("signal: killed")
		})
		saved := false

		err := inst.Pause(func() error { saved = true; return nil })

		require.Error(t, err)
		assert.Contains(t, err.Error(), "nothing was changed")
		assert.False(t, killed(srv, "api"))
		assert.False(t, saved, "not even the checkpoint")
		assert.Equal(t, Running, inst.GetStatus())
	})
	t.Run("its own", func(t *testing.T) {
		inst, srv, _ := startedOn(t, "api", func(wt string) (string, error) {
			return "loom_api\t" + wt + "\n", nil
		})

		require.NoError(t, inst.Pause(nil))

		assert.True(t, killed(srv, "api"))
	})
}

// TestKillAndPause_ListTmuxSessionsOnce: whose the agent's session is and
// whose the terminal pane's shell is are both read from one `tmux ls`; a
// listing each was a subprocess more for every kill and pause, waited on in
// full when tmux is slow.
func TestKillAndPause_ListTmuxSessionsOnce(t *testing.T) {
	for _, op := range []struct {
		name string
		do   func(*Instance) error
	}{
		{"kill", func(i *Instance) error { return i.Kill() }},
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			srv := &fakeTmuxServer{}
			inst, _, _ := newStartFixtureOn(t, "api", srv)
			require.NoError(t, inst.Start(true))
			wt := inst.getGitWorktree().GetWorktreePath()
			listings := 0
			runner := srv.runner()
			runner.OutputFunc = func(c *exec.Cmd) ([]byte, error) {
				if cmd_test.TmuxSubcommand(c.Args) == "ls" {
					listings++
				}
				return []byte("loom_api\t" + wt + "\nloom_term_api\t" + wt + "\n"), nil
			}
			inst.setTmuxSession(tmux.NewSessionWithDeps("api", "claude", srv, runner))

			require.NoError(t, op.do(inst))

			assert.Equal(t, 1, listings)
			assert.True(t, killed(srv, "api"))
			assert.True(t, killed(srv, tmux.TerminalSessionName("api")))
		})
	}
}
