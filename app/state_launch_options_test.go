package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingLaunchOptionsHome builds a *home with one not-yet-started
// instance in the list, the Session Launch Options modal open, and
// m.pendingLaunchOptions wired the same way state_new.go's Enter
// branch wires it — capturing instance.Program/Title so confirming
// composes and stashes the result on instance.Program without actually
// invoking Start() (which would need a real git worktree + tmux).
func newPendingLaunchOptionsHome(t *testing.T, initial overlay.LaunchOptions) (*home, *session.Instance) {
	t.Helper()
	m := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "my task",
		Path:      t.TempDir(),
		Program:   "claude",
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)
	m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)

	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
		instance.HeadroomProxy = opts.HeadroomProxy
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, nil
	}
	m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
	m.state = stateLaunchOptions
	m.setOverlay(overlay.NewSessionLaunchOptions(initial, m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)

	return m, instance
}

func TestHandleStateLaunchOptionsKeyConfirmComposesAndClearsPending(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "default"})

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "claude --permission-mode acceptEdits", instance.Program)
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
}

func TestHandleStateLaunchOptionsKeyConfirmSetsHeadroomProxy(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "default", Model: "default", HeadroomProxy: true})

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.True(t, instance.HeadroomProxy)
}

func TestHandleStateLaunchOptionsKeyTogglesBeforeConfirm(t *testing.T) {
	m, instance := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{PermissionMode: "default", Model: "default"})

	// Move to Model row (row 2), cycle it, then confirm.
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, "claude --model sonnet", instance.Program)
}

func TestHandleStateLaunchOptionsKeyEscCancelsAndKillsPendingInstance(t *testing.T) {
	m, _ := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{})
	before := m.list.NumInstances()

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})

	assert.Equal(t, before-1, m.list.NumInstances())
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
}

func TestHandleStateLaunchOptionsKeyCtrlCCancels(t *testing.T) {
	m, _ := newPendingLaunchOptionsHome(t, overlay.LaunchOptions{})
	before := m.list.NumInstances()

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	assert.Equal(t, before-1, m.list.NumInstances())
}

func TestCancelLaunchOptions_UsesStashedCancelClosure(t *testing.T) {
	m := newTestHome(t)
	called := false
	m.pendingLaunchOptionsCancel = func() (tea.Model, tea.Cmd) {
		called = true
		return m, nil
	}
	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		t.Fatal("must not run the confirm closure on cancel")
		return m, nil
	}

	m.cancelLaunchOptions()

	assert.True(t, called, "cancelLaunchOptions must run the stashed pendingLaunchOptionsCancel closure")
	assert.Nil(t, m.pendingLaunchOptions)
	assert.Nil(t, m.pendingLaunchOptionsCancel)
}
