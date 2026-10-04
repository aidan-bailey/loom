package core

import (
	"encoding/json"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/require"
)

// newInst builds an unstarted instance titled title in a temp dir.
func newInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	return inst
}

// newTerminal builds an unstarted workspace terminal titled title.
func newTerminal(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	return inst
}

// pausedInst builds a started, Paused instance titled title, as a
// reconciled record comes back (FromInstanceData; no tmux contacted).
// Persistable keeps it, and a resume moves it to Loading.
func pausedInst(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// storedWorkspace builds a workspace named name over a fresh temp config
// dir and its (empty) storage. A storage never loaded loads before its
// first write (Storage.writeLocked), so no explicit load is needed.
func storedWorkspace(t *testing.T, name string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	return NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{Name: name, ConfigDir: dir}, Storage: storage, Config: config.DefaultConfig(), State: state})
}

// recordingInstanceStorage counts SaveInstances calls and keeps the last
// payload, standing in for state.json (a copy of app's fixture of the
// same name).
type recordingInstanceStorage struct {
	calls    int
	lastData json.RawMessage
}

func (r *recordingInstanceStorage) SaveInstances(data json.RawMessage) error {
	r.calls++
	r.lastData = data
	return nil
}

func (r *recordingInstanceStorage) GetInstances() json.RawMessage { return r.lastData }
func (r *recordingInstanceStorage) DeleteAllInstances() error     { return nil }
