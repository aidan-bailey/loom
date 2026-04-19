package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePtyFactory is a minimal PtyFactory that does not actually start a
// pseudo-terminal. Start returns an open /dev/null handle so callers can
// safely close it without errors, and Close is a no-op. This avoids
// depending on the tmux package's internal test-only mock.
type fakePtyFactory struct {
	t *testing.T
}

func (f fakePtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		f.t.Fatalf("fakePtyFactory: opening /dev/null: %v", err)
	}
	return devNull, nil
}

func (f fakePtyFactory) Close() {}

// newTestStartedInstance returns an Instance that reports isStarted()==true
// and owns a mock-backed TmuxSession whose Close() can be called safely
// any number of times. No git worktree is attached; the instance is
// marked as a workspace terminal so Kill() skips the gitWorktree branch.
func newTestStartedInstance(t *testing.T) *Instance {
	t.Helper()

	ptyFactory := fakePtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	}
	ts := tmux.NewTmuxSessionWithDeps("kill-test", "true", ptyFactory, cmdExec)

	inst := &Instance{
		Title:               "kill-test",
		Status:              Running,
		IsWorkspaceTerminal: true, // skip worktree cleanup path
	}
	inst.setTmuxSession(ts)
	inst.setStarted(true)
	return inst
}

// TestInstance_KillIsIdempotent ensures Kill can be called safely twice
// (INST-05). The second call should be a no-op.
func TestInstance_KillIsIdempotent(t *testing.T) {
	inst := newTestStartedInstance(t)

	assert.NoError(t, inst.Kill())
	// Second Kill should not panic or return an error.
	assert.NoError(t, inst.Kill())

	assert.False(t, inst.isStarted(), "Kill should clear the started flag")
	assert.Nil(t, inst.getTmuxSession(), "Kill should nil out the tmux session")
}

// TestInstance_KillBoundedWithStuckPump is the Instance-level
// regression guard for F3+F7. A stuck pump goroutine inside TmuxSession
// used to propagate into an unbounded wait in Close → Kill → Pause,
// wedging the UI flow that triggered the op and — via the daemon's
// UpdateDiffStats path — every other tracked instance. With the
// bounded pump wait in tmux, Kill must complete within a reasonable
// budget even when the pump never exits.
func TestInstance_KillBoundedWithStuckPump(t *testing.T) {
	inst := newTestStartedInstance(t)
	inst.getTmuxSession().SimulateStuckPumpForTest()

	done := make(chan error, 1)
	go func() { done <- inst.Kill() }()

	select {
	case <-done:
		// Kill returned; assertion elsewhere (no error is OK — tmux
		// Close logs but does not fail on the abandoned pump path).
	case <-time.After(5 * time.Second):
		t.Fatal("Kill blocked on stuck pump — F3 bounded wait regressed")
	}
}

// newTestPausableInstance builds an Instance backed by a real git repo
// and worktree plus a mock-backed TmuxSession. Used by F9 tests to
// exercise Pause/Resume end-to-end with a controllable saveState hook.
func newTestPausableInstance(t *testing.T) *Instance {
	t.Helper()

	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	runInRepo := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%v failed: %s", args, string(out))
	}
	runInRepo("git", "init", "-b", "main")
	runInRepo("git", "config", "user.email", "test@example.com")
	runInRepo("git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0644))
	runInRepo("git", "add", ".")
	runInRepo("git", "commit", "-m", "init")

	branchName := "pause-test-branch"
	worktreePath := filepath.Join(configDir, "worktrees", branchName+"_fixture")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreePath), 0755))
	runInRepo("git", "worktree", "add", "-b", branchName, worktreePath)

	gw := git.NewGitWorktreeFromStorage(repoDir, worktreePath, "pause-test", branchName, "", true, configDir)

	ptyFactory := fakePtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte{}, nil },
	}
	ts := tmux.NewTmuxSessionWithDeps("pause-test", "true", ptyFactory, cmdExec)

	inst := &Instance{
		Title:  "pause-test",
		Status: Running,
	}
	inst.setGitWorktree(gw)
	inst.setTmuxSession(ts)
	inst.setStarted(true)
	return inst
}

// TestInstance_PausePropagatesSaveStateError is the F9 regression guard
// on the pause side. Before the fix a saveState callback returning an
// error during Pause was logged at Warning and Pause returned nil, so
// callers (and the daemon) believed state was durably persisted when it
// wasn't — a silent divergence between in-memory and on-disk state.
func TestInstance_PausePropagatesSaveStateError(t *testing.T) {
	inst := newTestPausableInstance(t)

	wantErr := errors.New("disk full")
	err := inst.Pause(func() error { return wantErr })

	assert.ErrorIs(t, err, wantErr, "Pause must propagate saveState error")
}

// TestInstance_PauseHappyPathStillReturnsNil keeps the success path
// honest: a saveState that returns nil must not leak spurious errors.
func TestInstance_PauseHappyPathStillReturnsNil(t *testing.T) {
	inst := newTestPausableInstance(t)
	assert.NoError(t, inst.Pause(func() error { return nil }))
	assert.Equal(t, Paused, inst.GetStatus())
}

// TestInstance_ResumePropagatesSaveStateError is the F9 regression
// guard on the resume side. Same failure mode as Pause: the saveState
// error was previously swallowed with a Warning log, masking
// persistence problems behind a successful Resume.
func TestInstance_ResumePropagatesSaveStateError(t *testing.T) {
	inst := newTestPausableInstance(t)
	require.NoError(t, inst.Pause(nil), "precondition: pause succeeds")

	wantErr := errors.New("disk full")
	err := inst.Resume(func() error { return wantErr })

	assert.ErrorIs(t, err, wantErr, "Resume must propagate saveState error")
}

// TestInstance_ResumeSurfacesBranchGoneHint is the F10 regression
// guard on the Resume side. When a paused instance's branch is
// deleted externally (via `git branch -D` or similar) and has no
// origin remote, Resume must return an error that (a) preserves the
// typed `git.ErrBranchGone` sentinel so callers can classify it, and
// (b) tells the operator how to recover — by killing the instance,
// not by mystery-debugging a "failed to setup git worktree" blob.
func TestInstance_ResumeSurfacesBranchGoneHint(t *testing.T) {
	inst := newTestPausableInstance(t)
	require.NoError(t, inst.Pause(nil), "precondition: pause succeeds")

	// Simulate the user running `git branch -D <branch>` out-of-band.
	gw := inst.getGitWorktree()
	cmd := exec.Command("git", "-C", gw.GetRepoPath(), "branch", "-D", gw.GetBranchName())
	out, cmdErr := cmd.CombinedOutput()
	require.NoError(t, cmdErr, "precondition: branch delete: %s", string(out))

	err := inst.Resume(func() error { return nil })

	require.Error(t, err)
	assert.ErrorIs(t, err, git.ErrBranchGone, "typed sentinel must propagate so UI can classify")
	assert.Contains(t, err.Error(), "kill", "Resume error must hint the kill-to-recover affordance")
}

// TestInstance_StartIsIdempotent verifies Start is a no-op on an
// already-started instance (INST-04). A second Start must not replace
// the tmux session and orphan the first.
func TestInstance_StartIsIdempotent(t *testing.T) {
	inst := newTestStartedInstance(t) // already started once

	// Capture current tmuxSession pointer.
	firstSession := inst.getTmuxSession()
	assert.NotNil(t, firstSession)

	// Second Start should not create a new tmux session.
	assert.NoError(t, inst.Start(true))
	assert.Same(t, firstSession, inst.getTmuxSession(),
		"Start should not replace the tmux session if already started")
}

// TestCombineErrorsIsUnwrapable guards that combineErrors uses errors.Join
// so every underlying cause remains discoverable via errors.Is. The prior
// fmt.Errorf("%s", ...) implementation stringified the causes and broke
// the unwrap chain — a silent failure mode for any caller that classifies
// errors (e.g. git.ErrBranchGone → UI recovery hint).
func TestCombineErrorsIsUnwrapable(t *testing.T) {
	sentinelA := errors.New("first cause")
	sentinelB := errors.New("second cause")
	inst := &Instance{}

	// Zero and single-error inputs behave naturally.
	assert.NoError(t, inst.combineErrors(nil))
	single := inst.combineErrors([]error{sentinelA})
	assert.ErrorIs(t, single, sentinelA)

	// Multi-error join must expose each cause to errors.Is.
	joined := inst.combineErrors([]error{sentinelA, sentinelB})
	require.Error(t, joined)
	assert.ErrorIs(t, joined, sentinelA, "first cause must survive Join")
	assert.ErrorIs(t, joined, sentinelB, "second cause must survive Join")

	// Wrapped causes must also survive.
	wrapped := fmt.Errorf("step failed: %w", sentinelA)
	joined2 := inst.combineErrors([]error{wrapped, sentinelB})
	assert.ErrorIs(t, joined2, sentinelA, "errors.Is must traverse both Join and Wrap")
	assert.ErrorIs(t, joined2, sentinelB)
}
