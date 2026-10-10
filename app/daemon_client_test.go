package app

import (
	"context"
	"net"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaemonGone_GoesOffline: the daemon going away under a running TUI
// (a crash: no bye) no longer quits it. The TUI goes offline, reconnecting,
// with its banner up, and keeps working on what needs no model. A daemon's
// client panics nowhere, so neither a render (View's local reads) nor the
// Update that notices the loss, nor a key after it, trips Bubble Tea's
// panic recovery.
func TestDaemonGone_GoesOffline(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	model := core.NewForTest(core.Options{Program: "true", CmdExec: &recordingExec{}})
	notices := model.Boot()
	loop := core.StartForTest(model)
	t.Cleanup(loop.Stop)
	srv := rpc.NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	client, err := rpc.Dial(b) // as the TUI dials the daemon
	require.NoError(t, err)
	m, err := startHome(context.Background(), client, client.Close, notices, "", "true", "", true)
	require.NoError(t, err)
	t.Cleanup(m.stopCore)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, linkConnected, m.link.state)

	srv.Close() // the daemon goes, saying no bye
	require.Eventually(t, func() bool { return client.Err() != nil }, 5*time.Second, 10*time.Millisecond)

	require.NotPanics(t, func() { _ = m.View() }, "a render before the TUI notices")
	require.NotPanics(t, func() {
		_ = m.core.Registry()
		_ = m.core.Workspaces()
		_ = m.core.Sync()
	}, "the client's local reads answer from its last replica")
	require.NotPanics(t, func() { m.Update(coreWakeMsg{}) }, "the loss's wake")
	assert.NoError(t, m.exitErr, "the TUI does not quit")
	assert.Equal(t, linkReconnecting, m.link.state, "no bye: a crash, so it reconnects")
	assert.Contains(t, viewText(m), "lost the loom daemon: reconnecting (attempt 1)")
	ended := make(chan struct{})
	go func() {
		for range client.Wakes() {
		}
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the lost client's wakes never closed: its forwardWakes would run on")
	}
	require.NotPanics(t, func() { _ = m.View() }, "a render offline")
	require.NotPanics(t, func() { m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) }, "a key that lands after")
	assert.NoError(t, m.exitErr)
}

// TestStartupHome_FindsAWorkspaceRegisteredAfterBoot: the daemon booted
// before `loom workspace add` registered a workspace, and `loom -w` names
// it: the TUI rereads the registry before it looks for the workspace, so
// it starts on it, and records it as the last used.
func TestStartupHome_FindsAWorkspaceRegisteredAfterBoot(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	model := core.NewForTest(core.Options{Registry: reg, Program: "true", CmdExec: &recordingExec{}})
	notices := model.Boot()

	late := writeWorkspaceState(t, "ws-late", `[]`)
	other, err := config.LoadWorkspaceRegistry() // `loom workspace add`'s
	require.NoError(t, err)
	require.NoError(t, other.Add(late.Name, late.Path))

	client, stop, err := startTestCore(model)
	require.NoError(t, err)
	t.Cleanup(stop)
	m, err := startHome(context.Background(), client, stop, notices, "ws-late", "true", "", true)
	require.NoError(t, err)

	assert.Equal(t, "ws-late", m.name())
	assert.Equal(t, "ws-late", m.core.Registry().LastUsed)
}
