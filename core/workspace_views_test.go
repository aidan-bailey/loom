package core

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspacesEvent returns the WorkspacesChanged among events, or nil.
func workspacesEvent(events []Event) *WorkspacesChanged {
	for _, ev := range events {
		if wc, ok := ev.(WorkspacesChanged); ok {
			return &wc
		}
	}
	return nil
}

func TestWorkspaceIDs_StableAndNeverReused(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(a, b)

	idA, idB := m.wsIDOf(a), m.wsIDOf(b)
	assert.NotEqual(t, idA, idB)
	assert.Equal(t, idA, m.wsIDOf(a), "stable")

	m.SetWorkspacesForTest(b)
	m.Sync() // a is no longer loaded: forgotten
	_, ok := m.Workspace(idA)
	assert.False(t, ok, "a closed workspace has no view")

	reopened := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(b, reopened)
	assert.Greater(t, m.wsIDOf(reopened), idB, "a reopened workspace gets a new ID, never an old one")
}

func TestWorkspaceView_CopiesTheWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.recovery = RecoverySummary{Cleaned: 2}
	m.SetWorkspacesForTest(ws)

	v, ok := m.Workspace(m.wsIDOf(ws))
	require.True(t, ok)
	assert.Equal(t, "a", v.Name)
	assert.Equal(t, "a", v.Label)
	assert.Equal(t, ws.ctx.ConfigDir, v.ConfigDir)
	assert.Equal(t, ws.cfg.GetProgram(), v.Settings.GetProgram())
	assert.Equal(t, 2, v.Recovery.Cleaned)
	assert.False(t, v.WritesRefused)

	global := NewWorkspace(WorkspaceParts{})
	m.SetWorkspacesForTest(global)
	gv, ok := m.Workspace(m.wsIDOf(global))
	require.True(t, ok)
	assert.Equal(t, "", gv.Name)
	assert.Equal(t, "global", gv.Label, "a bare workspace's view is safe and labelled")
}

func TestSync_PublishesWorkspacesFirstAndOnlyOnChange(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.add(pausedInst(t, "x"))
	m.SetWorkspacesForTest(ws)

	out := m.Sync()
	require.NotEmpty(t, out.Events)
	wc, ok := out.Events[0].(WorkspacesChanged)
	require.True(t, ok, "workspace views come first")
	require.Len(t, wc.Views, 1)
	assert.Equal(t, m.wsIDOf(ws), wc.Views[0].ID)

	assert.Nil(t, workspacesEvent(m.Sync().Events), "nothing changed: nothing published")

	prefs := ws.state.GetUIPrefs()
	prefs.RailHidden = true
	require.NoError(t, ws.state.SetUIPrefs(prefs))
	got := workspacesEvent(m.Sync().Events)
	require.NotNil(t, got, "a changed pref republishes")
	assert.True(t, got.Views[0].UIPrefs.RailHidden)

	ws.cfg.Mutate(func(c *config.Config) { c.DefaultProgram = "aider" })
	got = workspacesEvent(m.Sync().Events)
	require.NotNil(t, got, "a changed setting republishes")
	assert.Equal(t, "aider", got.Views[0].Settings.DefaultProgram)

	ws.recovery = RecoverySummary{Review: 1}
	assert.NotNil(t, workspacesEvent(m.Sync().Events), "a new recovery summary republishes")

	other := storedWorkspace(t, "b")
	m.SetWorkspacesForTest(ws, other)
	got = workspacesEvent(m.Sync().Events)
	require.NotNil(t, got, "a change to the loaded set republishes")
	assert.Len(t, got.Views, 2)
}

// TestSync_PublishedWorkspacesDoNotAliasTheModel: the TUI keeps the event's
// views, so changing them must change neither the model's state nor what
// the next Sync compares against.
func TestSync_PublishedWorkspacesDoNotAliasTheModel(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.cfg.Mutate(func(c *config.Config) { c.Profiles = []config.Profile{{Name: "p", Program: "x"}} })
	prefs := ws.state.GetUIPrefs()
	prefs.SplitRatios = map[string]float64{"x": 0.5}
	require.NoError(t, ws.state.SetUIPrefs(prefs))
	m.SetWorkspacesForTest(ws)

	wc := workspacesEvent(m.Sync().Events)
	require.NotNil(t, wc)
	wc.Views[0].Settings.Profiles[0].Program = "scribbled"
	wc.Views[0].UIPrefs.SplitRatios["x"] = 0.9
	wc.Views[0].Name = "scribbled"

	assert.Nil(t, workspacesEvent(m.Sync().Events), "the model's published copy is its own")
	assert.Equal(t, "x", ws.cfg.Snapshot().Profiles[0].Program)
	assert.InDelta(t, 0.5, ws.state.GetUIPrefs().SplitRatios["x"], 0)
}
