package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude writes an executable named claude that only sleeps: a launch
// of it counts as Claude (the adapter matches the basename), so remote
// control applies, without running the real CLI.
func fakeClaude(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
	return p
}

// TestOpen_BlockedRemoteControlEmitsTheRCOffNotice: the workspace
// terminal a first open creates launches without remote control when the
// default account's auth is blocked, and the model says so with an info
// notice (formerly the load's own errBox.SetInfo), queued before the
// terminal starts. The load paths run on a mock executor; the terminal
// starts on the private tmux server TestMain sets up.
func TestOpen_BlockedRemoteControlEmitsTheRCOffNotice(t *testing.T) {
	def := config.Workspace{Name: "rc-ws", Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"`+fakeClaude(t)+`"}`), 0o644))
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(def.Name))).Run()
	})
	noTmux := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{}, CmdExec: noTmux})
	m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"})

	ws, err := m.ensureLoaded(def)
	require.NoError(t, err)
	_, err = m.Open(m.wsIDOf(ws))
	require.NoError(t, err)

	assert.Contains(t, m.Drain().Events, Event(Notice{Info: "remote control off: not logged in"}))
	require.NotNil(t, ws.byTitle(def.Name), "the workspace terminal the notice is about was created")
	assert.NotContains(t, ws.byTitle(def.Name).Program(), "--remote-control")
}
