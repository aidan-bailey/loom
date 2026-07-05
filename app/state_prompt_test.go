package app

import (
	"testing"

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
