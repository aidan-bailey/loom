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
