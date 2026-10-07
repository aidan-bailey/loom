package app

import (
	"context"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHome builds a minimal *home suitable for unit tests. It wires
// a fresh script engine with embedded defaults so the Lua keymap is
// live, plus a tempdir-backed storage so handlers that save on exit
// (QuitIntent → handleQuit) don't panic. Tests that want an empty
// engine should reset m.scripts after construction.
func newTestHome(t *testing.T) *home {
	t.Helper()
	cfgDir := t.TempDir()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)

	h := &home{
		workspaceSlot: slotOver(t, testWS(core.WorkspaceParts{Storage: storage, Config: config.DefaultConfig(), State: state})),
		ctx:           context.Background(),
		state:         stateDefault,
		menu:          ui.NewMenu(),
		overview:      ui.NewOverview(),
		tabBar:        ui.NewWorkspaceTabBar(),
		errBox:        ui.NewErrBox(),
	}
	h.scripts = script.NewEngine(buildReservedKeys())
	h.scripts.LoadDefaults()
	return wireCore(t, wirePanes(t, h))
}

// TestSelectedNotBusyNotWorkspaceGuardsLifecycle exercises the shared
// precondition that gates kill/submit/stash. Loading/Deleting block;
// a workspace-terminal always blocks; Running passes.
func TestSelectedNotBusyNotWorkspaceGuardsLifecycle(t *testing.T) {
	h := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	h.ws().AddForTest(instance)
	h.syncViews()

	_ = instance.TransitionTo(session.Loading)
	h.syncViews()
	assert.False(t, selectedNotBusyNotWorkspace(h), "Loading should block")

	_ = instance.TransitionTo(session.Deleting)
	h.syncViews()
	assert.False(t, selectedNotBusyNotWorkspace(h), "Deleting should block")

	_ = instance.TransitionTo(session.Running)
	h.syncViews()
	assert.True(t, selectedNotBusyNotWorkspace(h), "Running is a normal state")

	instance.IsWorkspaceTerminal = true
	h.syncViews()
	assert.False(t, selectedNotBusyNotWorkspace(h), "workspace terminal blocks")
}
