package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPausedInstanceHome(t *testing.T) (*home, *session.Instance) {
	t.Helper()
	m := homeWithAppState(t)
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:         "restart-me",
		Path:          t.TempDir(),
		Program:       "claude --model sonnet --permission-mode auto",
		HeadroomProxy: true,
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
	// HeadroomProxy isn't derivable from Program (see
	// session.HeadroomProxyEnv) — it must be seeded from the instance's
	// own field instead.
	assert.True(t, lo.Options().HeadroomProxy)
}

func TestRunRestartWithOptionsSelected_ConfirmRecomposesProgramAndResumes(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, cmd := pending(overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"})

	assert.Contains(t, inst.Program, "--model opus")
	assert.False(t, inst.HeadroomProxy, "toggling Headroom Proxy off during restart must update the instance field")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, session.Loading, inst.GetStatus())
	require.NotNil(t, cmd) // the Resume Cmd — not invoked here, just asserting it's returned
}

func TestRunRestartWithOptionsSelected_AsyncSkipsResumeWhenLoadingTransitionFailed(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	// Route through the blocked-RC path so resumeTask lands directly in
	// m.pendingConfirmation instead of being wrapped in the outer
	// tea.Batch(resumeTask.Run(), ...) the direct path returns.
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})
	require.Equal(t, stateConfirm, m.state)

	// Every legal Status permits transitioning to Loading (see
	// allowedTransitions in session/instance.go) — TransitionTo(Loading)
	// only fails when the instance's current Status isn't one of the
	// known enum values, e.g. clobbered by a concurrent write between
	// the precondition check and Sync's write. Status is an exported
	// field, so corrupt it directly to force that failure once the user
	// confirms, proving Async bails instead of blindly calling Resume on
	// an instance that never actually transitioned.
	inst.Status = session.Status(99)

	cmd := m.pendingConfirmation.Run() // runs Sync, returns Async
	require.NotNil(t, cmd)

	// Async is tea.Batch(tea.RequestWindowSize, resumeFunc) — calling it
	// returns a tea.BatchMsg (the sub-commands to run), not an
	// already-resolved message. Run every sub-command and confirm none
	// of them is the resume outcome (transitionFailedMsg/resumeDoneMsg);
	// a tea.WindowSizeMsg from the RequestWindowSize half is expected
	// and fine.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok, "Async must be a batch (RequestWindowSize + the resume check)")
	for _, sub := range batch {
		require.NotNil(t, sub)
		switch sub().(type) {
		case transitionFailedMsg:
			t.Fatal("Resume must not have run (and errored)")
		case resumeDoneMsg:
			t.Fatal("Resume must not have run (and succeeded)")
		}
	}
	assert.Equal(t, session.Status(99), inst.GetStatus(), "status must be untouched by Resume")
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

	// Dispatch a real cancel keypress through the actual confirm-state
	// handler (not co.OnCancel() directly) — handleStateConfirmKey
	// unconditionally runs m.pendingConfirmation.Run() once the overlay
	// reports closed, for both confirm AND cancel, so this is the only
	// way to prove OnCancel's pendingConfirmation neutralization
	// actually prevents the resume task from running on a real cancel.
	handleStateConfirmKey(m, tea.KeyPressMsg{Code: 'n', Text: "n"})

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program)
	assert.Equal(t, stateDefault, m.state)
}
