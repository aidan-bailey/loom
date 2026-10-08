package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen_ReturnsTheWorkspacesView(t *testing.T) {
	def := config.Workspace{Name: "by-id", Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"`+fakeClaude(t)+`"}`), 0o644))
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(def.Name))).Run()
	})
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{}, CmdExec: aliveExec()})
	ws, err := m.ensureLoaded(def)
	require.NoError(t, err)

	v, err := m.Open(m.wsIDOf(ws))
	require.NoError(t, err)
	require.NotZero(t, v.ID)
	assert.Equal(t, "by-id", v.Name)
	got, ok := m.Workspace(v.ID)
	require.True(t, ok)
	assert.Equal(t, v, got, "the returned view is the workspace's")
	assert.True(t, m.IsLoaded(v.ID))
}

func TestByID_UnknownWorkspaces(t *testing.T) {
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(storedWorkspace(t, "a"))

	_, err := m.Open(99)
	assert.Error(t, err, "an unknown workspace has nothing to open")
	assert.Nil(t, m.Views(99))
	assert.False(t, m.IsLoaded(99))
	_, ok := m.Workspace(99)
	assert.False(t, ok)

	req := ReqID(7)
	m.Create(99, NewInstance{Title: "x"}, req)
	var reply *Reply
	for _, ev := range m.Drain().Events {
		if r, ok := ev.(Reply); ok && r.Req == req {
			reply = &r
		}
	}
	require.NotNil(t, reply, "the refused Create replies")
	require.Error(t, reply.Err)
	assert.Contains(t, reply.Err.Error(), "no longer open")
}

func TestRegistry_IsACopyAndReloads(t *testing.T) {
	reg := &config.WorkspaceRegistry{
		Workspaces:     []config.Workspace{{Name: "a", Path: "/a"}},
		OpenWorkspaces: []string{"a"},
	}
	m := NewForTest(Options{Registry: reg})

	v := m.Registry()
	require.Len(t, v.Workspaces, 1)
	require.Len(t, v.Open, 1)
	v.Workspaces[0].Name = "changed"
	assert.Equal(t, "a", m.Registry().Workspaces[0].Name, "a copy")

	// Another process registers a workspace.
	globalDir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(globalDir, 0o755))
	onDisk := config.WorkspaceRegistry{Workspaces: []config.Workspace{{Name: "a", Path: "/a"}, {Name: "b", Path: "/b"}}, OpenWorkspaces: []string{"a"}}
	data, err := json.Marshal(onDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "workspaces.json"), data, 0o644))
	t.Cleanup(func() { _ = os.Remove(filepath.Join(globalDir, "workspaces.json")) })

	require.NoError(t, m.ReloadRegistry())
	assert.Len(t, m.Registry().Workspaces, 2, "the reload sees it")
	assert.Equal(t, []string{"a"}, reg.OpenWorkspaces, "the open list is the file's")
}

func TestAccountNames(t *testing.T) {
	m := NewForTest(Options{})
	assert.False(t, m.AccountNames().Present, "nothing before InitAccounts")
	m.SetAccountsForTest(&account.Registry{Accounts: []account.Account{{Name: "work", Dir: "/w"}}})
	n := m.AccountNames()
	assert.True(t, n.Present)
	assert.Equal(t, account.DefaultName, n.Default)
	assert.Equal(t, []string{account.DefaultName, "work"}, n.Names)
	n.Names[1] = "changed"
	assert.Equal(t, "work", m.AccountNames().Names[1], "a copy")
}

func TestOwnerFields_NameTheOwner(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(a, b)

	id, label := m.ownerFields(a)
	assert.Equal(t, m.wsIDOf(a), id)
	assert.Equal(t, "a", label)

	id, label = m.ownerFields(nil)
	assert.Zero(t, id)
	assert.Equal(t, "global", label)
}
