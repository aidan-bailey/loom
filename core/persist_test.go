package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistableInstances_ExcludesRecoverable(t *testing.T) {
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "orphan",
		Path:          t.TempDir(),
		Branch:        "u/orphan",
		Status:        session.Recoverable,
		Worktree: session.GitWorktreeData{
			RepoPath:         t.TempDir(),
			WorktreePath:     t.TempDir(),
			BranchName:       "u/orphan",
			IsExistingBranch: true,
		},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, Persistable([]*session.Instance{inst}))
}

// TestPersistableInstancesFiltersDeleting verifies that Persistable
// excludes instances with Deleting status.
func TestPersistableInstancesFiltersDeleting(t *testing.T) {
	running, _ := session.NewInstance(session.InstanceOptions{
		Title: "running", Path: t.TempDir(), Program: "claude",
	})
	_ = running.TransitionTo(session.Running)

	deleting, _ := session.NewInstance(session.InstanceOptions{
		Title: "deleting", Path: t.TempDir(), Program: "claude",
	})
	_ = deleting.TransitionTo(session.Deleting)

	paused, _ := session.NewInstance(session.InstanceOptions{
		Title: "paused", Path: t.TempDir(), Program: "claude",
	})
	_ = paused.TransitionTo(session.Paused)

	result := Persistable([]*session.Instance{running, deleting, paused})
	assert.Len(t, result, 2)
	assert.Equal(t, "running", result[0].Title)
	assert.Equal(t, "paused", result[1].Title)
}

// TestPersistableInstances_KeepsIdleSessions: Ready is overloaded. A
// creation flow's instance is Ready before it starts, and the status
// ladder and Claude's roster report an idle agent or workspace terminal as
// Ready too. Only the never-started one stays off disk: skipping every
// Ready instance dropped idle sessions' records on each save, so the next
// load offered their worktrees as Recoverable orphans and killed and
// recreated an idle workspace terminal.
func TestPersistableInstances_KeepsIdleSessions(t *testing.T) {
	// started builds a started instance (paused data comes back started)
	// and moves it through Running to status.
	started := func(title string, terminal bool, status session.Status) *session.Instance {
		t.Helper()
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: title, Status: session.Paused, Program: "claude", IsWorkspaceTerminal: terminal,
		}, t.TempDir())
		require.NoError(t, err)
		require.NoError(t, inst.TransitionTo(session.Running))
		require.NoError(t, inst.TransitionTo(status))
		require.True(t, inst.Started())
		return inst
	}
	idleAgent := started("idle-agent", false, session.Ready)
	idleTerminal := started("idle-terminal", true, session.Ready)
	deleting := started("deleting", false, session.Deleting)

	creating, err := session.NewInstance(session.InstanceOptions{Title: "creating", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.Equal(t, session.Ready, creating.GetStatus())
	require.False(t, creating.Started(), "fixture: a creation flow's instance")
	starting, err := session.NewInstance(session.InstanceOptions{Title: "starting", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, starting.TransitionTo(session.Loading)) // its start is in flight
	recoverable, err := session.FromInstanceData(session.InstanceData{
		Title: "orphan", Status: session.Recoverable, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)

	got := Persistable([]*session.Instance{idleAgent, idleTerminal, creating, starting, deleting, recoverable})

	assert.Equal(t, []*session.Instance{idleAgent, idleTerminal, starting}, got)
}

// TestSave_WritesALoadedWorkspace is the control for the skip below: a
// Paused record reaches disk (Persistable keeps it).
func TestSave_WritesALoadedWorkspace(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.add(pausedInst(t, "kept"))
	m.SetWorkspacesForTest(nil, []*Workspace{ws})

	require.NoError(t, m.SaveWS(ws))
	data, err := ws.storage.LoadInstanceData()
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "kept", data[0].Title)
}

func TestCloseTab_RefusesTheLastTab(t *testing.T) {
	m := NewForTest(Options{})
	a := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(nil, []*Workspace{a})
	_, err := m.CloseTabWS("a")
	require.Error(t, err)
	assert.Equal(t, []*Workspace{a}, m.TabsWS())
}

func TestCloseTab_DropsTheTab(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(nil, []*Workspace{a, b})
	closed, err := m.CloseTabWS("a")
	require.NoError(t, err)
	assert.Same(t, a, closed)
	assert.Equal(t, []*Workspace{b}, m.TabsWS())
	assert.False(t, m.IsLoadedWS(a))
}

// TestSave_SkipsAClosedWorkspaceThatWasReopened: a dropped workspace's
// copy is stale once its workspace is open again, so saving it would
// overwrite the reopened copy's newer state.json.
func TestSave_SkipsAClosedWorkspaceThatWasReopened(t *testing.T) {
	m := NewForTest(Options{})
	closed, reopened := storedWorkspace(t, "a"), storedWorkspace(t, "a")
	closed.add(pausedInst(t, "stale"))
	m.SetWorkspacesForTest(nil, []*Workspace{reopened, storedWorkspace(t, "b")})

	require.NoError(t, m.SaveWS(closed))
	data, err := closed.storage.LoadInstanceData()
	require.NoError(t, err)
	assert.Empty(t, data, "the closed copy was not written")
}
