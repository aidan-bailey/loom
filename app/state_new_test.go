package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingTitleEntryHome mirrors what runNewInstance (app/intents.go)
// does when 'n' is pressed: append a blank, unstarted instance and
// enter stateNew — without needing the full repoPath()/configDir()
// plumbing runNewInstance itself depends on.
func newPendingTitleEntryHome(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "",
		Path:      t.TempDir(),
		Program:   m.appConfig.DefaultProgram,
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)
	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	return m
}

func TestHandleStateNewKeyEnterOpensLaunchOptionsInsteadOfStartingImmediately(t *testing.T) {
	m := newPendingTitleEntryHome(t)

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateLaunchOptions, m.state)
	require.NotNil(t, m.pendingLaunchOptions)
	assert.NotNil(t, m.launchOptionsOverlay())
}

func TestNewInstanceFlowEndToEndComposesRealClosure(t *testing.T) {
	m := newPendingTitleEntryHome(t)

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // opens the real modal + stashes the real closure

	require.Equal(t, stateLaunchOptions, m.state)
	instance := m.list.GetInstances()[m.list.NumInstances()-1]

	// Move to Model row (row 2), cycle it, then confirm through the real
	// handleStateLaunchOptionsKey -> real pendingLaunchOptions closure
	// (not newPendingLaunchOptionsHome's substitute).
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Contains(t, instance.Program, "--model sonnet")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
	assert.Equal(t, session.Loading, instance.GetStatus())
}

func TestNewInstanceFlowRemoteControlBlockedViaModalPromptsConfirm(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // opens the real modal

	require.Equal(t, stateLaunchOptions, m.state)

	// Row 0 (Remote Control) defaults to enabled from DefaultConfig;
	// confirm without toggling it off, so remoteControlBlocked fires.
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateConfirm, m.state)
	assert.NotNil(t, m.pendingConfirmation.Async)
}
