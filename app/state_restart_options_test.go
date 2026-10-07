package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
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
		CacheTTL1h:    true,
	})
	require.NoError(t, err)
	m.ws.Add(inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	require.NoError(t, inst.TransitionTo(session.Paused))
	m.syncViews()
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
	// HeadroomProxy/CacheTTL1h aren't derivable from Program (see
	// session.HeadroomProxyEnv/CacheTTL1hEnv) — they must be seeded from
	// the instance's own fields instead.
	assert.True(t, lo.Options().HeadroomProxy)
	assert.True(t, lo.Options().CacheTTL1h)
}

func TestRunRestartWithOptionsSelected_ConfirmRecomposesProgramAndResumes(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, cmd := pending(overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"})

	assert.Contains(t, inst.Program(), "--model 'opus'")
	assert.False(t, inst.HeadroomProxy(), "toggling Headroom Proxy off during restart must update the instance field")
	assert.False(t, inst.CacheTTL1h(), "toggling Cache TTL off during restart must update the instance field")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, session.Loading, inst.GetStatus())
	require.NotNil(t, cmd) // the Resume Cmd — not invoked here, just asserting it's returned
}

func TestRunRestartWithOptionsSelected_AsyncSkipsResumeWhenLoadingTransitionFailed(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	// Route through the blocked-RC path so resumeTask lands directly in
	// m.pendingConfirmation instead of being wrapped in the outer
	// tea.Batch(resumeTask.Run(), ...) the direct path returns.
	m.core.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
	runRestartWithOptionsSelected(m)

	pending := m.pendingLaunchOptions
	require.NotNil(t, pending)
	_, _ = pending(overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"})
	require.Equal(t, stateConfirm, m.state)

	// Corrupt the status between the key's gate and the confirm: Status is
	// an exported field, so a concurrent write could leave any value
	// there. The request's precondition (Paused) then refuses it once the
	// user confirms, proving nothing runs Resume on an instance that is no
	// longer Paused, and that a refused request records no options.
	inst.Status = session.Status(99)
	programBefore := inst.Program()

	cmd := m.pendingConfirmation.Run() // runs Sync (the request), returns Async
	require.NotNil(t, cmd)

	// Async is only tea.RequestWindowSize now: the resume is a request,
	// whose job a drain would hand to the runtime. A refused request
	// queues none, so neither a resume outcome (core.OpFailed /
	// core.ResumeResult) nor a skipped-resume check can come of it.
	results := requestResults(t, m)
	require.Empty(t, results, "a refused resume queues no job")
	for _, res := range results {
		switch res.(type) {
		case core.OpFailed:
			t.Fatal("Resume must not have run (and errored)")
		case core.ResumeResult:
			t.Fatal("Resume must not have run (and succeeded)")
		}
	}
	assert.Equal(t, programBefore, inst.Program(), "a refused request applies no launch options")
	assert.Equal(t, session.Status(99), inst.GetStatus(), "status must be untouched by Resume")
}

func TestRunRestartWithOptionsSelected_CancelLeavesInstanceUntouched(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	originalProgram := inst.Program()
	runRestartWithOptionsSelected(m)

	_, _ = m.cancelLaunchOptions()

	assert.Equal(t, session.Paused, inst.GetStatus())
	assert.Equal(t, originalProgram, inst.Program())
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.launchOptionsOverlay())
}

func TestRunRestartWithOptionsSelected_BlockedRemoteControlPromptsConfirm(t *testing.T) {
	m, inst := newPausedInstanceHome(t)
	m.core.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
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
	m.core.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})
	originalProgram := inst.Program()
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
	assert.Equal(t, originalProgram, inst.Program())
	assert.Equal(t, stateDefault, m.state)
}
