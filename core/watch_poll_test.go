package core

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchWorkspace builds a workspace over repo ("" for the global one) with
// the watcher switched on or off and the branch prefix "loom/".
func watchWorkspace(t *testing.T, repo string, on bool) *Workspace {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mutate(func(c *config.Config) {
		c.Watch = &on
		c.BranchPrefix = "loom/"
	})
	return NewWorkspace(WorkspaceParts{
		Ctx:    &config.WorkspaceContext{Name: "polyoxide", ConfigDir: t.TempDir(), RepoPath: repo},
		Config: cfg,
	})
}

func TestWatchFor_CreatesTheStateOnceWithItsMaps(t *testing.T) {
	m := NewForTest(Options{})
	ws := watchWorkspace(t, t.TempDir(), true)
	assert.Nil(t, ws.watch, "nothing until first use")

	st := m.watchFor(ws)
	require.NotNil(t, st)
	assert.Same(t, st, ws.watch)
	assert.Same(t, st, m.watchFor(ws), "the same state every time")
	assert.NotNil(t, st.inFlight, "Story 9 writes to it")
	assert.NotNil(t, st.actErrs, "Story 9 writes to it")
	assert.False(t, st.polling)
	assert.Nil(t, st.snap, "no tick decided yet")
}

func TestWatchOf_IsTheStateOfAServedWorkspaceWithTheWatcherOn(t *testing.T) {
	m := NewForTest(Options{})
	ws := watchWorkspace(t, t.TempDir(), true)
	m.SetWorkspacesForTest(ws)
	assert.Nil(t, m.watchOf(nil))
	assert.Nil(t, m.watchOf(ws), "no state until a poll made it")

	st := m.watchFor(ws)
	assert.Same(t, st, m.watchOf(ws))

	off := false
	ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })
	assert.Nil(t, m.watchOf(ws), "switched off: a late result records nothing")

	on := true
	ws.cfg.Mutate(func(c *config.Config) { c.Watch = &on })
	m.SetWorkspacesForTest()
	assert.Nil(t, m.watchOf(ws), "no longer served: a late result records nothing")
}
