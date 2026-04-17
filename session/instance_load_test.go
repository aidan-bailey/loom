package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFromInstanceData_NoPTY_ForRunning asserts that FromInstanceData
// does not spawn a tmux PTY attachment for Running-status instances.
// Callers must opt into the PTY via EnsureRunning. This fixes DAEMON-05:
// the daemon's per-tick reload no longer restarts every instance.
func TestFromInstanceData_NoPTY_ForRunning(t *testing.T) {
	data := InstanceData{
		Title:               "running-ws",
		Status:              Running,
		IsWorkspaceTerminal: true, // skip worktree setup
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.False(t, inst.isStarted(), "FromInstanceData must not start a Running instance")
	assert.Nil(t, inst.getTmuxSession(), "FromInstanceData must not create a TmuxSession for Running")
}

// TestFromInstanceData_Paused_PreservesShape asserts that paused
// instances still come back fully constructed (started=true, TmuxSession
// object present, no PTY attachment) — unchanged from prior behaviour.
func TestFromInstanceData_Paused_PreservesShape(t *testing.T) {
	data := InstanceData{
		Title:               "paused-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, inst.isStarted(), "Paused instance should be marked started")
	assert.NotNil(t, inst.getTmuxSession(), "Paused instance should have a TmuxSession object")
}

// TestEnsureRunning_NoOpForPaused asserts EnsureRunning does not spawn a
// PTY for paused instances.
func TestEnsureRunning_NoOpForPaused(t *testing.T) {
	data := InstanceData{
		Title:               "paused-ws",
		Status:              Paused,
		IsWorkspaceTerminal: true,
		Program:             "true",
	}

	inst, err := FromInstanceData(data, t.TempDir())
	assert.NoError(t, err)

	priorTs := inst.getTmuxSession()
	assert.NoError(t, inst.EnsureRunning())
	assert.Same(t, priorTs, inst.getTmuxSession(),
		"EnsureRunning must not replace the TmuxSession for paused instances")
}
