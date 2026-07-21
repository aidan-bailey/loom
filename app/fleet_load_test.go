package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
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
