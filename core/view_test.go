package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestView_CopiesTheInstance(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	views := m.ViewsWS(ws)
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
	ws.add(a)
	ws.add(b)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	idA, idB := m.idOf(a), m.idOf(b)
	assert.NotEqual(t, idA, idB)
	assert.Equal(t, idA, m.idOf(a), "stable")

	ws.remove(a)
	m.Sync() // a is no longer loaded: forgotten
	_, ok := m.View(idA)
	assert.False(t, ok)
	ws.add(a)
	assert.NotEqual(t, idA, m.idOf(a), "a forgotten instance gets a new ID, never an old one")
	assert.Greater(t, m.idOf(a), idB)
}

func TestSync_PublishesChangedWorkspacesFirst(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := pausedInst(t, "x")
	ws.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	m.notifyInfo("hello")
	out := m.Sync()
	require.Len(t, out.Events, 3)
	_, ok := out.Events[0].(WorkspacesChanged)
	require.True(t, ok, "workspace views first")
	vc, ok := out.Events[1].(ViewsChanged)
	require.True(t, ok, "instance views next")
	assert.Same(t, ws, vc.Workspace)
	assert.Equal(t, Notice{Info: "hello"}, out.Events[2])

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
	ws.add(pausedInst(t, "x"))
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	out := instanceEvents(m.Sync().Events)
	require.Len(t, out, 1)
	vc := out[0].(ViewsChanged)
	vc.Views[0].Title = "scribbled"
	assert.Empty(t, m.Sync().Events, "the model's published copy is its own")
}

// TestSync_ForgetsClosedWorkspaces: a workspace no longer loaded is
// forgotten, so loading it again publishes it again.
func TestSync_ForgetsClosedWorkspaces(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	a.add(pausedInst(t, "x"))
	b.add(pausedInst(t, "y"))
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	require.Len(t, instanceEvents(m.Sync().Events), 2, "both published the first time")

	m.SetWorkspacesForTest(nil, []*Workspace{a})
	assert.Empty(t, instanceEvents(m.Sync().Events), "a closed workspace publishes nothing")
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	out := instanceEvents(m.Sync().Events)
	require.Len(t, out, 1)
	assert.Same(t, b, out[0].(ViewsChanged).Workspace, "reopened: published again")
}

// TestLookup_DoesNotAllocate: lookup runs on every pane event.
func TestLookup_DoesNotAllocate(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	inst := pausedInst(t, "x")
	b.add(inst)
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	id := m.idOf(inst)

	got, ws := m.lookup(id)
	require.Same(t, inst, got)
	require.Same(t, b, ws)
	assert.Zero(t, testing.AllocsPerRun(100, func() { m.lookup(id) }))

	m.SetWorkspacesForTest(b, nil)
	got, ws = m.lookup(id)
	require.Same(t, inst, got, "classic mode")
	require.Same(t, b, ws)
	assert.Zero(t, testing.AllocsPerRun(100, func() { m.lookup(id) }))
}

// TestCloneViews_CopiesSubagents: a published view's Subagents must not
// share a backing array with the model's copy.
func TestCloneViews_CopiesSubagents(t *testing.T) {
	views := []InstanceView{{ID: 1, Subagents: []subagent.View{{Name: "a"}}}}
	c := cloneViews(views)
	c[0].Subagents[0].Name = "b"
	c[0].Title = "t"
	assert.Equal(t, "a", views[0].Subagents[0].Name)
	assert.Empty(t, views[0].Title)
	assert.Nil(t, cloneViews([]InstanceView{{}})[0].Subagents, "nil stays nil")
}

// instanceEvents drops the WorkspacesChanged events from events: the
// instance-view tests count only what they publish.
func instanceEvents(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		if _, ok := ev.(WorkspacesChanged); !ok {
			out = append(out, ev)
		}
	}
	return out
}
