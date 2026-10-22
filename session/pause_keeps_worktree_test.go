package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstance_PauseKeepsTheWorktree is the regression guard for a pause
// losing ignored files. Pause used to remove the worktree and Resume
// rebuild it from the branch, carrying uncommitted work across in a stash;
// the stash honours .gitignore, so every ignored file (a /draft-plan run's
// .superpowers/ state, .env, .claude/settings.local.json) was lost. Pause
// now stops the agent and leaves the tree alone, and Resume relaunches the
// agent in it.
func TestInstance_PauseKeepsTheWorktree(t *testing.T) {
	inst, srv := newRunningOnFakeTmux(t)
	gw, err := inst.GetGitWorktree()
	require.NoError(t, err)
	dir := gw.GetWorktreePath()
	exclude := filepath.Join(gw.GetRepoPath(), ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
	require.NoError(t, os.WriteFile(exclude, []byte(".superpowers/\n"), 0o644))
	ignored := filepath.Join(dir, ".superpowers", "drafts", "STATE.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(ignored), 0o755))
	require.NoError(t, os.WriteFile(ignored, []byte("phase: drafting\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("tracked edit\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("never committed\n"), 0o644))
	require.Empty(t, gitIn(t, dir, "status", "--porcelain", "--", ".superpowers"), "precondition: the file is ignored")

	require.NoError(t, inst.Pause(nil))

	assert.Equal(t, Paused, inst.GetStatus())
	assert.Equal(t, "phase: drafting\n", readFile(t, ignored), "Pause keeps ignored files")
	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	assert.Empty(t, gw.GetStashRef(), "nothing is stashed")
	assert.Empty(t, gitIn(t, gw.GetRepoPath(), "stash", "list"))

	require.NoError(t, resumeLikeApp(t, inst))

	assert.Equal(t, "phase: drafting\n", readFile(t, ignored), "Resume keeps ignored files")
	assert.Equal(t, "tracked edit\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "never committed\n", readFile(t, filepath.Join(dir, "notes.txt")))
	launches := srv.launchArgs()
	require.Len(t, launches, 1, "resume relaunches the agent")
	assert.Equal(t, dir, argAfter(launches[0], "-c"), "in the kept worktree")
}

// TestPause_SavesLoadingBeforeItStopsTheAgent: with the worktree kept, a
// record still saved Running over a dead session and an intact tree is one
// reconcile crash-restarts, so a pause that crashed after its kill would be
// undone. Pause saves first, while the caller has the instance in Loading,
// which reconcile marks Paused whether or not the session survived.
func TestPause_SavesLoadingBeforeItStopsTheAgent(t *testing.T) {
	var events []string
	srv := &fakeTmuxServer{}
	runner := srv.runner()
	inst := newTestPausableInstanceWithExec(t, cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if slices.Contains(c.Args, "kill-session") {
				events = append(events, "kill")
			}
			return runner.RunFunc(c)
		},
		OutputFunc: runner.OutputFunc,
	})
	require.NoError(t, inst.TransitionTo(Loading), "as core.Model.Pause does")
	var saved []Status
	save := func() error {
		saved = append(saved, inst.ToInstanceData().Status)
		events = append(events, "save")
		return nil
	}

	require.NoError(t, inst.Pause(save))

	require.NotEmpty(t, events)
	assert.Equal(t, "save", events[0], "the checkpoint is saved before the agent's session is killed")
	assert.Equal(t, Loading, saved[0])
	assert.Equal(t, ActionMarkPaused, DetermineRecoveryAction(saved[0], false, true, false),
		"a crash after the kill reconciles to Paused")
	assert.Equal(t, ActionKillAndPause, DetermineRecoveryAction(saved[0], true, true, false),
		"and a crash before it too")
	assert.Equal(t, Paused, saved[len(saved)-1])
}
