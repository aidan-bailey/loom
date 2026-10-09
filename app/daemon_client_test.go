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

// TestDaemonGone_QuitsCleanly: the daemon going away under a running TUI
// (a crash, `loom serve stop`, a newer loom replacing it) quits the TUI,
// with ErrDaemonGone for Run to report once the terminal is restored. A
// daemon's client panics nowhere, so neither a render (View's local reads)
// nor the Update that quits can trip Bubble Tea's panic recovery first.
func TestDaemonGone_QuitsCleanly(t *testing.T) {
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
	require.NoError(t, m.exitErr)

	srv.Close() // the daemon goes
	require.Eventually(t, func() bool { return client.Err() != nil }, 5*time.Second, 10*time.Millisecond)

	require.NotPanics(t, func() { _ = m.View() }, "a render before the TUI quits")
	require.NotPanics(t, func() {
		_ = m.core.Registry()
		_ = m.core.Workspaces()
		_ = m.core.Sync()
	}, "the client's local reads answer from its last replica")
	var cmd tea.Cmd
	require.NotPanics(t, func() { _, cmd = m.Update(coreWakeMsg{}) }, "the loss's wake")
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd(), "the TUI quits")
	assert.ErrorIs(t, m.exitErr, ErrDaemonGone)
	require.NotPanics(t, func() { _ = m.View() }, "the last render")
	require.NotPanics(t, func() { m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) }, "a key that lands after")
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
