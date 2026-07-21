package app

import (
	"fmt"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ensureFleetLoaded should emit one activation per registered workspace
// that is not already a slot and not already loading, and mark each as
// loading. This test checks the loading set + that already-present slots
// are skipped.
func TestEnsureFleetLoaded_SkipsOpenAndLoading(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
		{Name: "c", Path: "/tmp/c"},
	}}
	// "a" already a slot; "b" already loading; only "c" should be queued.
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}}}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"b": true}

	cmd := m.ensureFleetLoaded()
	require.NotNil(t, cmd, "a workspace remained to load")
	assert.True(t, m.fleetLoading["c"], "c marked loading")
	assert.True(t, m.fleetEngaged, "overview engaged")
	assert.False(t, m.fleetLoading["a"], "already-open workspace not queued")
}

func TestEnsureFleetLoaded_NothingToLoad(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{{Name: "a"}}}
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}}}
	m.focusedSlot = 0
	cmd := m.ensureFleetLoaded()
	assert.Nil(t, cmd, "no workspaces left to load → no Cmd")
	assert.True(t, m.fleetEngaged)
}

func TestHandleWorkspaceActivated_AppendsBackgroundSlot(t *testing.T) {
	m := newTestHome(t)
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}, list: m.list}}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"b": true}

	st := config.LoadStateFrom(t.TempDir())
	stor, err := session.NewStorage(st, t.TempDir())
	require.NoError(t, err)
	msg := workspaceActivatedMsg{
		name: "b", wsCtx: &config.WorkspaceContext{Name: "b"},
		storage: stor, appConfig: config.DefaultConfig(), appState: st,
		instances: []*session.Instance{{Title: "sess", Status: session.Ready}},
	}
	m.handleWorkspaceActivated(msg)

	require.Len(t, m.slots, 2)
	assert.Equal(t, "b", m.slots[1].wsCtx.Name)
	assert.True(t, m.slots[1].background, "fleet-loaded slot is background")
	assert.False(t, m.fleetLoading["b"], "loading flag cleared")
	assert.Equal(t, 0, m.focusedSlot, "focus unchanged by a background load")
	assert.NotEqual(t, m.list, m.slots[1].list, "background slot has its own list")
}

func TestHandleWorkspaceActivated_ErrorRecorded(t *testing.T) {
	m := newTestHome(t)
	m.fleetLoading = map[string]bool{"b": true}
	m.handleWorkspaceActivated(workspaceActivatedMsg{name: "b", err: assertErr})
	assert.Empty(t, m.slots)
	assert.False(t, m.fleetLoading["b"])
	assert.Error(t, m.fleetLoadErrors["b"])
}

func TestHandleWorkspaceActivated_DuplicateDiscarded(t *testing.T) {
	m := newTestHome(t)
	m.slots = []workspaceSlot{
		{wsCtx: &config.WorkspaceContext{Name: "a", ConfigDir: "/x/a"}, list: m.list},
	}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"a": true}
	m.handleWorkspaceActivated(workspaceActivatedMsg{
		name: "a", wsCtx: &config.WorkspaceContext{Name: "a", ConfigDir: "/x/a"},
	})
	assert.Len(t, m.slots, 1, "duplicate ConfigDir discarded")
}

func TestEnterOverview_SetsPendingLoad(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: "/tmp/a"}, {Name: "b", Path: "/tmp/b"},
	}}
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}, list: m.list}}
	m.focusedSlot = 0

	m.enterOverview()
	assert.Equal(t, viewOverview, m.viewMode)
	assert.True(t, m.pendingOverviewLoad, "overview entry raises the load flag")

	// handleScriptDone drains it into a real load Cmd.
	cmd := m.ensureFleetLoaded()
	assert.NotNil(t, cmd, "load Cmd for workspace b")
	assert.True(t, m.fleetLoading["b"])
}

var assertErr = fmt.Errorf("boom")
