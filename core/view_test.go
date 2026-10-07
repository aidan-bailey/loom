package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestView_CopiesTheInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	views := m.Views(ws)
	require.Len(t, views, 1)
	v := views[0]
	assert.NotZero(t, v.ID)
	assert.Equal(t, "x", v.Title)
	assert.Equal(t, session.Paused, v.Status)
	assert.True(t, v.Paused())
	assert.True(t, v.Started)
	assert.False(t, v.Active())
	assert.Equal(t, inst.Program(), v.Program)

	got, ok := m.View(v.ID)
	require.True(t, ok)
	assert.Equal(t, v, got)
}

func TestIDs_StableNeverReusedAndForgotten(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	a, b := pausedInst(t, "a"), pausedInst(t, "b")
	ws.Add(a)
	ws.Add(b)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	idA, idB := m.idOf(a), m.idOf(b)
	assert.NotEqual(t, idA, idB)
	assert.Equal(t, idA, m.idOf(a), "stable")

	ws.Remove(a)
	m.Sync() // a is no longer loaded: forgotten
	_, ok := m.View(idA)
	assert.False(t, ok)
	ws.Add(a)
	assert.NotEqual(t, idA, m.idOf(a), "a forgotten instance gets a new ID, never an old one")
	assert.Greater(t, m.idOf(a), idB)
}

func TestSync_PublishesChangedWorkspacesFirst(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.Add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.notifyInfo("hello")
	out := m.Sync()
	require.Len(t, out.Events, 2)
	vc, ok := out.Events[0].(ViewsChanged)
	require.True(t, ok, "views first")
	assert.Same(t, ws, vc.Workspace)
	assert.Equal(t, Notice{Info: "hello"}, out.Events[1])

	assert.Empty(t, m.Sync().Events, "nothing changed")

	require.NoError(t, inst.TransitionTo(session.Loading))
	out = m.Sync()
	require.Len(t, out.Events, 1)
	assert.Equal(t, session.Loading, out.Events[0].(ViewsChanged).Views[0].Status)
}

// TestSync_PublishedViewsDoNotAliasTheModel: the TUI keeps a ViewsChanged's
// views as its store, so changing them must not change what the next Sync
// compares against.
func TestSync_PublishedViewsDoNotAliasTheModel(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.Add(pausedInst(t, "x"))
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	out := m.Sync()
	require.Len(t, out.Events, 1)
	vc := out.Events[0].(ViewsChanged)
	vc.Views[0].Title = "scribbled"
	assert.Empty(t, m.Sync().Events, "the model's published copy is its own")
}

// TestSync_ForgetsClosedWorkspaces: a workspace no longer loaded is
// forgotten, so loading it again publishes it again.
func TestSync_ForgetsClosedWorkspaces(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	a.Add(pausedInst(t, "x"))
	b.Add(pausedInst(t, "y"))
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	require.Len(t, m.Sync().Events, 2, "both published the first time")

	m.SetWorkspacesForTest(nil, []*Workspace{a})
	assert.Empty(t, m.Sync().Events, "a closed workspace publishes nothing")
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	out := m.Sync()
	require.Len(t, out.Events, 1)
	assert.Same(t, b, out.Events[0].(ViewsChanged).Workspace, "reopened: published again")
}
