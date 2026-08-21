package session

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"

	"github.com/stretchr/testify/assert"
)

// TestCheckTmuxAlive_TimeoutDoesNotReportDead is the startup-path half of
// the false-dead fix. A timed-out probe here maps to
// `!tmuxAlive && worktreeExists` → ActionRestart, so loom tries to create
// a second tmux session for a title that already has a live one and fails
// with "duplicate session: already exists" — the documented tell of this
// bug. A probe that got no answer is not evidence of death.
func TestCheckTmuxAlive_TimeoutDoesNotReportDead(t *testing.T) {
	prev := reconcileTmuxTimeout
	reconcileTmuxTimeout = 20 * time.Millisecond
	t.Cleanup(func() { reconcileTmuxTimeout = prev })

	mockExec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error {
			time.Sleep(200 * time.Millisecond) // outlive the deadline
			return errors.New("signal: killed")
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}

	assert.True(t, CheckTmuxAlive("busy-session", mockExec),
		"a probe that never answered must not be reported as a dead session")
}

// TestCheckTmuxAlive_AnsweredNegativeIsDead pins the other half: tmux
// actually answering "no such session" stays actionable.
func TestCheckTmuxAlive_AnsweredNegativeIsDead(t *testing.T) {
	mockExec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return errors.New("can't find session") },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}

	assert.False(t, CheckTmuxAlive("gone-session", mockExec))
}
