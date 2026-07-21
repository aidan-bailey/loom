package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJumpWaiting_CrossesToPeerWorkspace(t *testing.T) {
	focus := fleetSlot(t, "focused", "f-idle") // idle
	peer := fleetSlot(t, "peer")               // one prompting instance below
	peer.background = true
	waiter := &session.Instance{Title: "p-wait", Status: session.Ready}
	require.NoError(t, waiter.TransitionTo(session.Prompting))
	peer.list.AddInstance(waiter)

	m := &home{
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		focusedSlot: 0, registry: &config.WorkspaceRegistry{},
		tabBar: ui.NewWorkspaceTabBar(), overview: ui.NewOverview(),
		slots:     []workspaceSlot{focus, peer},
		list:      focus.list,
		splitPane: focus.splitPane, storage: focus.storage,
		appConfig: focus.appConfig, appState: focus.appState,
	}
	// The only waiting agent is in the background peer workspace.
	m.jumpWaiting(1)
	assert.Equal(t, 1, m.focusedSlot, "focus crossed to the peer workspace")
	assert.False(t, m.slots[1].background, "landing promoted the peer slot")
	assert.Equal(t, "p-wait", m.list.GetSelectedInstance().Title)
}
