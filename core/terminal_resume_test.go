package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pausedTerminal builds a Paused workspace terminal for repo, as a record
// loads (FromInstanceData), its program a stand-in Claude.
func pausedTerminal(t *testing.T, title, repo string) *session.Instance {
	t.Helper()
	term, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: repo, Status: session.Paused, Program: fakeClaude(t), IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	return term
}

// A Paused workspace terminal is resumed by request (the 'r' key). Only a
// workspace's first open relaunches its terminal otherwise, so one whose
// relaunch failed then (the boot's crash restart, while tmux could not
// start a session) or whose breaker tripped stayed Paused until the daemon
// restarted. The resume resets its breaker, relaunches it in its
// repository, gives the TUI a pane (SessionLaunched) and saves it Running.
func TestResume_APausedTerminalRelaunches(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	killSessionAtEnd(t, "main")
	term := pausedTerminal(t, "main", repo)
	for range maxWorkspaceTerminalRestartFailures {
		term.RecordRestartFailure()
	}
	ws := storedWorkspace(t, "main")
	ws.ctx.RepoPath = repo
	ws.add(term)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)
	m.Drain()

	m.Resume(m.idOf(term), 3)
	require.Equal(t, session.Loading, term.GetStatus(), "the spinner shows at once")
	events := runJobs(t, m)

	r := replyTo(events, 3)
	require.NotNil(t, r, "the request is answered")
	require.NoError(t, r.Err)
	assert.Equal(t, session.Running, term.GetStatus())
	assert.Zero(t, term.RestartFailureCountForTest(), "its breaker is reset")
	assert.Contains(t, events, Event(SessionLaunched{ID: m.idOf(term)}), "the TUI attaches its pane")
	assert.Equal(t, canonical(t, repo), canonical(t, sessionPath(t, "main")), "it runs in its repository")
	recs := savedRecords(t, ws.ctx.ConfigDir)
	require.Len(t, recs, 1)
	assert.Equal(t, session.Running, recs[0].Status, "saved Running")
}

// A Paused terminal whose own session still runs (a relaunch whose probe
// timed out after tmux started it) is reattached, not launched again over
// its name.
func TestResume_APausedTerminalsOwnLiveSessionIsReattached(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	startForeignSession(t, "main", repo) // its own: a terminal's starts in its repository
	term := pausedTerminal(t, "main", repo)
	launches := term.Launches()

	require.NoError(t, term.Resume(nil))

	assert.Equal(t, session.Running, term.GetStatus())
	assert.Equal(t, launches, term.Launches(), "nothing launched")
	assert.Equal(t, canonical(t, repo), canonical(t, sessionPath(t, "main")))
}

// Resuming a terminal whose name another session holds (another
// workspace's, or an agent titled after the workspace, whose worktree lies
// below the repository) is refused: the relaunch would kill or adopt that
// session. Nothing changes.
func TestResume_ATerminalNameHeldElsewhereIsRefused(t *testing.T) {
	for _, where := range []string{"another directory", "below its repository"} {
		t.Run(where, func(t *testing.T) {
			isolateTmux(t)
			repo := t.TempDir()
			elsewhere := t.TempDir()
			if where == "below its repository" {
				elsewhere = filepath.Join(repo, ".loom", "worktrees", "main_1")
				require.NoError(t, os.MkdirAll(elsewhere, 0o755))
			}
			startHolder(t, "main", elsewhere)
			term := pausedTerminal(t, "main", repo)

			err := term.Resume(nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "another session's")
			assertHolderRuns(t, "main", elsewhere)
			assert.Equal(t, session.Paused, term.GetStatus())
		})
	}
}

// When tmux does not answer whose the session under the terminal's name
// is, its resume refuses and closes nothing.
func TestResume_ATerminalRefusesWhenTmuxCannotSayWhoseTheSessionIs(t *testing.T) {
	var killed []string
	term := pausedTerminal(t, "main", t.TempDir())
	term.SetTmuxSessionForTest(tmux.NewSessionWithDeps("main", "claude", fakePtyFactory{t: t}, starvedTmux(&killed)))

	err := term.Resume(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not answer")
	assert.Equal(t, session.Paused, term.GetStatus())
	assert.Empty(t, killed)
}
