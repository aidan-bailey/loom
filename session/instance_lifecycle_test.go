package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/session/tmux"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
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
