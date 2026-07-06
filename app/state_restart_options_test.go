package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPausedInstanceHome(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := homeWithAppState(t)
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "restart-me",
		Path:    t.TempDir(),
		Program: "headroom wrap claude --model sonnet --permission-mode auto",
	})
	require.NoError(t, err)
	_ = m.list.AddInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.NoError(t, inst.TransitionTo(session.Paused))
	return m, inst
}

func TestRunRestartWithOptionsSelected_OpensModalSeededFromProgram(t *testing.T) {
	m, _ := newPausedInstanceHome(t)

	runRestartWithOptionsSelected(m)

	assert.Equal(t, stateLaunchOptions, m.state)
	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)
	assert.Equal(t, "sonnet", lo.Options().Model)
	assert.Equal(t, "auto", lo.Options().PermissionMode)
	assert.True(t, lo.Options().HeadroomWrap)
}

func TestRunRestartWithOptionsSelected_ConfirmRecomposesProgramAndResumes(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, cmd := pending(overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"})

	assert.Contains(t, inst.Program, "--model opus")
	assert.NotContains(t, inst.Program, "headroom wrap", "unchecking Headroom Wrap must drop the prefix")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, session.Loading, inst.GetStatus())
	require.NotNil(t, cmd) // the Resume Cmd — not invoked here, just asserting it's returned
}

func TestRunRestartWithOptionsSelected_CancelLeavesInstanceUntouched(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	originalProgram := inst.Program
	runRestartWithOptionsSelected(m)

	_, _ = m.cancelLaunchOptions()

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program)
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.launchOptionsOverlay())
}

func TestRunRestartWithOptionsSelected_BlockedRemoteControlPromptsConfirm(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})

	assert.Equal(t, stateConfirm, m.state)
	assert.NotNil(t, m.pendingConfirmation.Async)
	// Instance must not have been touched yet — only the confirm
	// dialog is up.
	assert.Equal(t, session.Paused, inst.GetStatus())
}

func TestRunRestartWithOptionsSelected_BlockedRemoteControlCancelLeavesInstanceUntouched(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	originalProgram := inst.Program
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})
	require.Equal(t, stateConfirm, m.state)

	co := m.confirmation()
	require.NotNil(t, co)
	co.OnCancel()

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program)
	assert.Equal(t, stateDefault, m.state)
}
