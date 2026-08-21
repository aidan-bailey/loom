package tmux

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
)

// TestSessionLiveness_TimeoutIsUnknown is the regression guard for the
// false-dead cascade of 2026-08-21. Under heavy load (parallel cargo
// builds) `tmux has-session` took longer than the probe deadline and was
// killed; the probe reported plain false, the health tick read that as
// death and marked every running session Paused. The sessions were alive
// the whole time. A probe that never got an answer must say so rather
// than assert death.
func TestSessionLiveness_TimeoutIsUnknown(t *testing.T) {
	prev := livenessProbeTimeout
	livenessProbeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { livenessProbeTimeout = prev })

	ts := newTmuxSession("probe", "prog", NewMockPtyFactory(t), cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error {
			time.Sleep(300 * time.Millisecond) // outlive the deadline
			return errors.New("signal: killed")
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	})

	assert.Equal(t, LivenessUnknown, ts.SessionLiveness(),
		"a probe killed at its deadline says nothing about the session")
}

// TestSessionLiveness_AnsweredProbeIsDead pins the other half: when tmux
// actually answers "no such session", that IS evidence of death and must
// stay actionable. Only the no-answer case is inconclusive.
func TestSessionLiveness_AnsweredProbeIsDead(t *testing.T) {
	ts := newTmuxSession("probe", "prog", NewMockPtyFactory(t), cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return errors.New("can't find session: probe") },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	})

	assert.Equal(t, LivenessDead, ts.SessionLiveness())
	assert.False(t, ts.DoesSessionExist(), "boolean probe must keep its meaning for existing callers")
}

// TestSessionLiveness_SuccessIsAlive is the happy path.
func TestSessionLiveness_SuccessIsAlive(t *testing.T) {
	ts := newTmuxSession("probe", "prog", NewMockPtyFactory(t), cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	})

	assert.Equal(t, LivenessAlive, ts.SessionLiveness())
	assert.True(t, ts.DoesSessionExist())
}
