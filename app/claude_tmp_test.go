package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeTmpSweep_QueuedByEveryLoadPath: both workspace-load paths run
// reconcileOrphans, which queues a sweep of the loaded config dir.
func TestClaudeTmpSweep_QueuedByEveryLoadPath(t *testing.T) {
	isolateTmux(t)
	t.Setenv(config.EnvGlobalDir, t.TempDir())

	t.Run("multi-tab restore", func(t *testing.T) {
		ws := preservedTerminalWorkspace(t, "ws-sweep")
		m := newRestoreHome(&listingExec{})
		m.restoreSavedWorkspaces([]config.Workspace{ws})

		require.Len(t, m.slots, 1)
		assert.Contains(t, m.claudeTmpPending, config.WorkspaceConfigDir(&ws))
		assert.NotNil(t, m.maybeClaudeTmpSweep(), "the next health tick dispatches it")
	})

	t.Run("classic startup", func(t *testing.T) {
		ws := preservedTerminalWorkspace(t, "ws-classic")
		rec := &listingExec{}
		m := newRestoreHome(rec)
		m.wsCtx = config.WorkspaceContextFor(&ws)
		state := config.LoadStateFrom(m.wsCtx.ConfigDir)
		storage, err := session.NewStorage(state, m.wsCtx.ConfigDir)
		require.NoError(t, err)
		m.appState, m.storage = state, storage

		_, err = m.loadStartupStorage(rec, true)
		require.NoError(t, err)

		assert.Contains(t, m.claudeTmpPending, m.wsCtx.ConfigDir)
	})
}

// TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother: a load while a
// sweep runs gets exactly one more pass once it lands.
func TestClaudeTmpSweep_OneInFlightAndALoadQueuesAnother(t *testing.T) {
	sp := spinner.New()
	m := &home{}
	m.requestClaudeTmpSweep(t.TempDir(), ui.NewList(&sp), nil)
	first := m.maybeClaudeTmpSweep()
	require.NotNil(t, first)
	assert.Empty(t, m.claudeTmpPending, "dispatching takes the queue")

	m.requestClaudeTmpSweep(t.TempDir(), ui.NewList(&sp), nil)
	assert.Nil(t, m.maybeClaudeTmpSweep(), "at most one sweep in flight")

	msg, ok := first().(gatedMsg)
	require.True(t, ok)
	_, again := m.deliverGated(msg)
	require.NotNil(t, again, "the queued load gets one more pass")
	assert.Empty(t, m.claudeTmpPending)
	assert.Nil(t, m.maybeClaudeTmpSweep(), "and only one")
}

// TestClaudeTmpSweep_SnapshotsTheClaimSet: the claim set is taken when the
// load queues the sweep, on the Update goroutine.
func TestClaudeTmpSweep_SnapshotsTheClaimSet(t *testing.T) {
	cfg := t.TempDir()
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "paused",
		Path:          t.TempDir(),
		Branch:        "u/paused",
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:     t.TempDir(),
			WorktreePath: "/wt/paused_18be000000000001",
			BranchName:   "u/paused",
		},
	}
	inst, err := session.FromInstanceData(data, cfg)
	require.NoError(t, err)
	sp := spinner.New()
	list := ui.NewList(&sp)
	list.AddInstance(inst)
	m := &home{}

	m.requestClaudeTmpSweep(cfg, list, nil)

	assert.True(t, m.claudeTmpPending[cfg].claimed["/wt/paused_18be000000000001"])
}
