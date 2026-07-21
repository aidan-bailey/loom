package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachedInstance builds an instance whose tmux session has a live
// preview PTY (fake /dev/null handle), so tests can observe discard
// paths detaching it via PtmxAlive.
func attachedInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: title, Path: t.TempDir(), Program: "claude",
	})
	require.NoError(t, err)
	ts := tmux.NewTmuxSessionWithDeps(title, "claude", fakePtyFactory{t: t}, aliveCmdExecForTest())
	inst.SetTmuxSession(ts)
	require.NoError(t, inst.RepairPtmx())
	require.True(t, inst.PtmxAlive(), "fixture: preview PTY attached")
	return inst
}

// A stale fleetEngaged flag (survived enterGlobalMode, which empties
// m.slots) must not make the fleet toggle branch index into empty slots.
func TestApplyWorkspaceToggle_FleetEngagedWithoutSlots_NoPanic(t *testing.T) {
	t.Setenv("LOOM_HOME", t.TempDir())
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{}
	m.fleetEngaged = true

	assert.NotPanics(t, func() {
		_ = m.applyWorkspaceToggle([]config.Workspace{{Name: "a", Path: t.TempDir()}})
	})
}

// Fleet loading requires workspace mode: with no slots (global/classic)
// there is no foreground anchor for background slots, so ensureFleetLoaded
// must no-op without engaging.
func TestEnsureFleetLoaded_NoOpWithoutSlots(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{Workspaces: []config.Workspace{
		{Name: "a", Path: "/tmp/a"}, {Name: "b", Path: "/tmp/b"},
	}}

	cmd := m.ensureFleetLoaded()

	assert.Nil(t, cmd, "no fleet load in global/classic mode")
	assert.False(t, m.fleetEngaged, "fleet must not engage without a foreground slot")
	assert.Empty(t, m.fleetLoading)
}

// enterGlobalMode must clear all fleet state: the sticky engaged flag
// (else the picker's fleet branch later indexes empty slots) and the
// load bookkeeping (else stale loading/error groups render forever).
func TestEnterGlobalMode_ResetsFleetState(t *testing.T) {
	t.Setenv("LOOM_HOME", t.TempDir())
	m := fleetHome(t)
	m.fleetEngaged = true
	m.fleetLoading = map[string]bool{"c": true}
	m.fleetLoadErrors = map[string]error{"d": assertErr}

	_ = m.enterGlobalMode()

	assert.Empty(t, m.slots)
	assert.False(t, m.fleetEngaged, "global mode must clear the sticky fleet flag")
	assert.Empty(t, m.fleetLoading)
	assert.Empty(t, m.fleetLoadErrors)
}

// A load that outlived the mode that requested it (user returned to
// global mode mid-flight) must be discarded — not adopted as a background
// slot with no foreground anchor — and its preview PTYs released.
func TestHandleWorkspaceActivated_StaleGlobalModeLoadDiscarded(t *testing.T) {
	m := newTestHome(t) // no slots: global/classic mode
	m.fleetLoading = map[string]bool{"b": true}
	inst := attachedInstance(t, "b-sess")

	st := config.LoadStateFrom(t.TempDir())
	stor, err := session.NewStorage(st, t.TempDir())
	require.NoError(t, err)
	m.handleWorkspaceActivated(workspaceActivatedMsg{
		name: "b", wsCtx: &config.WorkspaceContext{Name: "b"},
		storage: stor, appConfig: config.DefaultConfig(), appState: st,
		instances: []*session.Instance{inst},
	})

	assert.Empty(t, m.slots, "stale fleet load must not create a slot in global mode")
	assert.False(t, m.fleetLoading["b"])
	assert.False(t, inst.PtmxAlive(), "discarded instance's preview PTY detached")
}

// The duplicate guard must release the discarded instances' preview PTYs
// (pump + handle + emulator) instead of leaking them for the app's life.
// The tmux sessions themselves stay alive — the winning slot owns them.
func TestHandleWorkspaceActivated_DuplicateReleasesPty(t *testing.T) {
	m := newTestHome(t)
	m.fleetEngaged = true
	m.slots = []workspaceSlot{{wsCtx: &config.WorkspaceContext{Name: "a"}, list: m.list}}
	m.focusedSlot = 0
	m.fleetLoading = map[string]bool{"a": true}
	inst := attachedInstance(t, "a-sess")

	st := config.LoadStateFrom(t.TempDir())
	stor, err := session.NewStorage(st, t.TempDir())
	require.NoError(t, err)
	m.handleWorkspaceActivated(workspaceActivatedMsg{
		name: "a", wsCtx: &config.WorkspaceContext{Name: "a"},
		storage: stor, appConfig: config.DefaultConfig(), appState: st,
		instances: []*session.Instance{inst},
	})

	assert.Len(t, m.slots, 1, "duplicate discarded")
	assert.False(t, inst.PtmxAlive(), "duplicate's preview PTY detached")
}
