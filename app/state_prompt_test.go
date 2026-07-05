package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStatePromptKeySubmitOpensLaunchOptionsInsteadOfStartingImmediately(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.promptAfterName = true

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> statePrompt, opens prompt overlay
	require.Equal(t, statePrompt, m.state)

	ti := m.textInput()
	require.NotNil(t, ti)
	for _, r := range "do the thing" {
		handleStatePromptKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// Initial focus is the textarea (index 0); shift+tab wraps backward
	// to the last stop (the Enter button) regardless of how many stops
	// the branch/profile pickers add.
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateLaunchOptions, m.state)
	require.NotNil(t, m.pendingLaunchOptions)
	assert.NotNil(t, m.launchOptionsOverlay())
}

func TestPromptFlowEndToEndComposesRealClosure(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.promptAfterName = true

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> statePrompt

	for _, r := range "do the thing" {
		handleStatePromptKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> stateLaunchOptions, real closure stashed

	require.Equal(t, stateLaunchOptions, m.state)
	instance := m.list.GetInstances()[m.list.NumInstances()-1]

	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Contains(t, instance.Program, "--model sonnet")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, "do the thing", instance.Prompt)
	assert.Nil(t, m.pendingLaunchOptions)
}

func TestPromptFlowRemoteControlBlockedViaModalPromptsConfirm(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.promptAfterName = true
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> statePrompt

	for _, r := range "do the thing" {
		handleStatePromptKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // -> stateLaunchOptions

	require.Equal(t, stateLaunchOptions, m.state)

	// Row 0 (Remote Control) defaults to enabled from DefaultConfig;
	// confirm without toggling it off, so remoteControlBlocked fires.
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateConfirm, m.state)
	assert.NotNil(t, m.pendingConfirmation.Async)
}
