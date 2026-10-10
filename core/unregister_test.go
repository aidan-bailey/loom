package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savedModel is bootModel with its registry on disk too (workspaces.json
// in the test's global dir), as the daemon's is, booted: every registry
// request rereads the file first.
func savedModel(t *testing.T, reg config.WorkspaceRegistry) *Model {
	t.Helper()
	m := bootModel(t, reg.Workspaces...)
	m.registry.OpenWorkspaces, m.registry.LastUsed = reg.OpenWorkspaces, reg.LastUsed
	require.NoError(t, config.SaveWorkspaceRegistry(m.registry))
	m.boot()
	return m
}

// onDisk is the registry as workspaces.json holds it.
func onDisk(t *testing.T) *config.WorkspaceRegistry {
	t.Helper()
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	return reg
}

// handEdit rewrites workspaces.json behind the model's back, as a user's
// editor or another process does.
func handEdit(t *testing.T, reg config.WorkspaceRegistry) {
	t.Helper()
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	data, err := json.Marshal(reg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspaces.json"), data, 0o644))
}

// tmuxRecorder records every command the model runs, answering each as a
// server with no session would.
func tmuxRecorder(ran *[][]string) cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			*ran = append(*ran, c.Args)
			return &exec.ExitError{}
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			*ran = append(*ran, c.Args)
			return nil, nil
		},
	}
}

func servedIDs(m *Model) []WorkspaceID {
	var ids []WorkspaceID
	for _, v := range m.Workspaces() {
		ids = append(ids, v.ID)
	}
	return ids
}

// Unregistering a workspace saves it and stops serving it, and touches
// none of its sessions: registering its directory again serves its
// records anew, under the same workspace and instance IDs.
func TestUnregister_StopsServingAndKeepsItsSessions(t *testing.T) {
	a := workspaceDef(t, "a", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a, b}, OpenWorkspaces: []string{"a", "b"}})
	ws := served(m, a)
	id, xID := m.wsIDOf(ws), m.idOf(ws.byTitle("x"))
	m.Sync()
	var ran [][]string
	m.SetExecForTest(tmuxRecorder(&ran))

	require.NoError(t, m.Unregister("a"))

	assert.Nil(t, served(m, a), "no longer served")
	assert.False(t, m.IsLoaded(id))
	assert.NotContains(t, servedIDs(m), id)
	assert.NotNil(t, served(m, b), "the others stay served")
	assert.Nil(t, onDisk(t).Get("a"), "unregistered on disk")
	assert.Equal(t, []string{"b"}, onDisk(t).OpenWorkspaces, "and gone from the open list")
	assert.Empty(t, ran, "its sessions are not touched")
	require.Len(t, savedRecords(t, config.WorkspaceConfigDir(&a)), 1, "its records are kept")
	var changed *WorkspacesChanged
	for _, e := range m.Sync().Events {
		if w, ok := e.(WorkspacesChanged); ok {
			changed = &w
		}
	}
	require.NotNil(t, changed, "clients hear of it")
	for _, v := range changed.Views {
		assert.NotEqual(t, id, v.ID)
	}

	v, err := m.Register("a", a.Path)
	require.NoError(t, err)
	assert.Equal(t, id, v.ID, "served again under the same ID")
	views := m.Views(v.ID)
	require.Len(t, views, 1)
	assert.Equal(t, xID, views[0].ID, "its session is served again under the same ID")
}

// The workspaces Unregister refuses, changing nothing: the global one,
// which no entry names, an unknown name, and one whose session a
// lifecycle job owns, or that a reset is emptying, since the job's
// completion acts on its workspace.
func TestUnregister_Refusals(t *testing.T) {
	a := workspaceDef(t, "a", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a}})
	ws := served(m, a)

	assert.ErrorIs(t, m.Unregister(""), ErrRefused, "the global workspace")
	assert.ErrorIs(t, m.Unregister("nope"), ErrRefused, "an unknown name")

	require.NoError(t, ws.byTitle("x").TransitionTo(session.Loading))
	err := m.Unregister("a")
	assert.ErrorIs(t, err, ErrRefused, "a busy session")
	assert.ErrorContains(t, err, "x is busy")
	require.NoError(t, ws.byTitle("x").TransitionTo(session.Paused))

	ws.resetting = true
	assert.ErrorIs(t, m.Unregister("a"), ErrRefused, "a reset running")
	ws.resetting = false

	assert.Same(t, ws, served(m, a), "still served")
	assert.NotNil(t, onDisk(t).Get("a"), "still registered")
	require.NoError(t, m.Unregister("a"), "idle, it goes")
}

// An entry for a directory another entry still names (a twin, through a
// symlink) only leaves the registry: the workspace stays served, under the
// name still registered and the same ID.
func TestUnregister_ATwinOnlyLeavesTheRegistry(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	link := filepath.Join(t.TempDir(), "a")
	require.NoError(t, os.Symlink(a.Path, link))
	twin := config.Workspace{Name: "a-via-link", Path: link}
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a, twin}})
	ws := served(m, a)
	id := m.wsIDOf(ws)

	require.NoError(t, m.Unregister("a"))

	assert.Same(t, ws, served(m, twin), "still served")
	v, ok := m.Workspace(id)
	require.True(t, ok)
	assert.Equal(t, "a-via-link", v.Name, "under the name still registered")
}

// Renaming keeps the workspace's ID and sessions, and the registry's open
// list and last used workspace follow the new name; a taken name, an
// unknown one and the global workspace are refused.
func TestRenameWorkspace_KeepsTheID(t *testing.T) {
	a := workspaceDef(t, "a", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a, b}, OpenWorkspaces: []string{"a", "b"}, LastUsed: "a"})
	ws := served(m, a)
	id, xID := m.wsIDOf(ws), m.idOf(ws.byTitle("x"))

	require.NoError(t, m.RenameWorkspace("a", "c"))

	v, ok := m.Workspace(id)
	require.True(t, ok, "the same ID")
	assert.Equal(t, "c", v.Name)
	assert.Same(t, ws, served(m, a), "the same workspace, not reloaded")
	_, ok = m.View(xID)
	assert.True(t, ok, "its session keeps its ID")
	reg := m.Registry()
	assert.Equal(t, "c", reg.LastUsed)
	require.Len(t, reg.Open, 2)
	assert.Equal(t, "c", reg.Open[0].Name, "the open list follows")
	assert.Equal(t, []string{"c", "b"}, onDisk(t).OpenWorkspaces)

	assert.ErrorIs(t, m.RenameWorkspace("c", "b"), ErrRefused, "a taken name")
	assert.ErrorIs(t, m.RenameWorkspace("nope", "z"), ErrRefused, "an unknown name")
	assert.ErrorIs(t, m.RenameWorkspace("", "z"), ErrRefused, "the global workspace")
	assert.ErrorIs(t, m.RenameWorkspace("c", ""), ErrRefused, "no name")
	assert.Equal(t, "c", served(m, a).Name(), "nothing changed")
}

// A hand edit of workspaces.json is applied at the next reread: a workspace
// removed from it is unloaded, and one renamed in it is renamed in place,
// keeping its ID.
func TestReloadRegistry_AppliesAHandEditedRemovalAndRename(t *testing.T) {
	a := workspaceDef(t, "a", `[]`, "true")
	b := workspaceDef(t, "b", `[]`, "true")
	c := workspaceDef(t, "c", `[]`, "true")
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a, b, c}})
	aID, bID := m.wsIDOf(served(m, a)), m.wsIDOf(served(m, b))
	n := len(m.Loaded())

	bee := b
	bee.Name = "bee"
	handEdit(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{bee, c}})
	require.NoError(t, m.ReloadRegistry())

	assert.Nil(t, served(m, a), "the removed workspace is unloaded")
	assert.False(t, m.IsLoaded(aID))
	assert.Len(t, m.Loaded(), n-1)
	v, ok := m.Workspace(bID)
	require.True(t, ok, "the renamed one keeps its ID")
	assert.Equal(t, "bee", v.Name)
	assert.NotNil(t, served(m, c))
}

// A workspace removed by hand while one of its sessions is busy stays
// served until a reread finds it idle.
func TestReloadRegistry_KeepsABusyWorkspaceUntilTheNextReread(t *testing.T) {
	a := workspaceDef(t, "a", `[{"title":"x","status":3,"program":"claude"}]`, "true")
	m := savedModel(t, config.WorkspaceRegistry{Workspaces: []config.Workspace{a}})
	ws := served(m, a)
	require.NoError(t, ws.byTitle("x").TransitionTo(session.Loading))

	handEdit(t, config.WorkspaceRegistry{})
	require.NoError(t, m.ReloadRegistry())
	assert.Same(t, ws, served(m, a), "kept while busy")

	require.NoError(t, ws.byTitle("x").TransitionTo(session.Paused))
	require.NoError(t, m.ReloadRegistry())
	assert.Nil(t, served(m, a), "unloaded once idle")
}
