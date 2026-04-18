package app

import (
	"context"
	"testing"

	"claude-squad/config"
	"claude-squad/script"
	"claude-squad/session"
	"claude-squad/ui"

	"github.com/charmbracelet/bubbles/spinner"
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
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&s, false)

	cfgDir := t.TempDir()
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	require.NoError(t, err)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		storage:   storage,
		appState:  state,
	}
	h.scripts = script.NewEngine(buildReservedKeys())
	h.scripts.LoadDefaults()
	return h
}

// TestSelectedNotBusyNotWorkspaceGuardsLifecycle exercises the shared
// precondition that gates kill/submit/checkout. Loading/Deleting block;
// a workspace-terminal always blocks; Running passes.
func TestSelectedNotBusyNotWorkspaceGuardsLifecycle(t *testing.T) {
	h := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = h.list.AddInstance(instance)

	_ = instance.TransitionTo(session.Loading)
	assert.False(t, selectedNotBusyNotWorkspace(h), "Loading should block")

	_ = instance.TransitionTo(session.Deleting)
	assert.False(t, selectedNotBusyNotWorkspace(h), "Deleting should block")

	_ = instance.TransitionTo(session.Running)
	assert.True(t, selectedNotBusyNotWorkspace(h), "Running is a normal state")

	instance.IsWorkspaceTerminal = true
	assert.False(t, selectedNotBusyNotWorkspace(h), "workspace terminal blocks")
}
