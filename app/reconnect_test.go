package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reconnect tests lose stack A's daemon under a TUI and rejoin it onto
// stack B: another model booted from the same disk, as a new daemon would
// be, which agrees with A on every ID.

// runningRecord is a stored session that runs: its tmux session answers
// (the recording executor says every session is alive), so the row is
// active and gets a pane client.
func runningRecord(t *testing.T, title, created string) string {
	t.Helper()
	return fmt.Sprintf(`{"title":%q,"status":0,"program":"true","created_at":%q,"worktree":{"worktree_path":%q}}`,
		title, created, t.TempDir())
}

// twoTabs registers ws-a (holding keeper, paused) and ws-b (holding other,
// paused) under a fresh global dir, both in the open list, ws-a last used:
// the TUI starts with both as tabs, ws-a focused.
func twoTabs(t *testing.T) (a, b config.Workspace) {
	t.Helper()
	globalDisk(t)
	a = writeWorkspaceState(t, "ws-a", "["+pausedRecord("keeper", "2026-10-01T10:00:00Z")+"]")
	b = writeWorkspaceState(t, "ws-b", "["+pausedRecord("other", "2026-10-01T11:00:00Z")+"]")
	savedRegistry(t, []string{"ws-a", "ws-b"}, "ws-a", a, b)
	return a, b
}

// rejoinOnto has m's next rejoin join stack b, runs it, and applies its
// result with everything it produced (no tick: it succeeds).
func rejoinOnto(t *testing.T, m *home, b daemonStack) {
	t.Helper()
	m.rejoin = func(bool, func(string)) (*rpc.Client, error) { return b.c, nil }
	res := tick(t, m)
	require.NotNil(t, res)
	require.NoError(t, res.err)
	_, cmd := m.Update(*res)
	runCmds(t, cmd)
	require.Equal(t, linkConnected, m.link.state)
}

func slotIDs(m *home) []core.WorkspaceID {
	var ids []core.WorkspaceID
	for _, s := range m.slots {
		ids = append(ids, s.id)
	}
	return ids
}

// TestReconnect_KeepsTabsSelectionAndDraft: after a crash and a rejoin
// onto another daemon, the TUI shows the same tabs and selection (the same
// IDs: every daemon derives them from the disk), its draft is untouched,
// and the new daemon has opened every workspace shown and been told the
// selection.
func TestReconnect_KeepsTabsSelectionAndDraft(t *testing.T) {
	isolateTmux(t)
	twoTabs(t)
	m, a := linkedHome(t, "")
	require.Len(t, m.slots, 2)
	require.Equal(t, "ws-a", m.name())
	tabs := slotIDs(m)
	keeper := rowID(t, m, "keeper")
	m.newDraft("drafty", "", 0)
	m.list.SelectID(keeper)
	m.Update(coreWakeMsg{})

	crash(t, m, a)
	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)

	assert.Equal(t, tabs, slotIDs(m), "the same tabs")
	assert.Equal(t, "ws-a", m.name(), "the same tab focused")
	assert.Equal(t, keeper, selID(m.list), "the same row selected")
	require.NotNil(t, m.draft, "the draft is untouched")
	assert.True(t, slices.ContainsFunc(m.list.GetInstances(), func(v core.InstanceView) bool { return v.ID == 0 && v.Title == "drafty" }))
	for _, id := range tabs {
		assert.True(t, b.loop.OpenedForTest(id), "the new daemon opened every workspace shown")
	}
	require.Eventually(t, func() bool { return slices.Contains(b.loop.SelectedForTest(), keeper) },
		5e9, 5e6, "the new daemon was told the selection")
	assert.Contains(t, m.errBox.String(), "reconnected to the loom daemon")
	assert.NotContains(t, viewText(m), "⚠", "the banner is gone")
	assert.NoError(t, m.checkSlotInvariant())
	assert.Same(t, b.c, m.conn)
}

// TestReconnect_KeepsPaneClientsOnTheSameTmuxServer: a daemon rejoined on
// the server the last one named leaves every pane client as it is: the
// sessions never went away.
func TestReconnect_KeepsPaneClientsOnTheSameTmuxServer(t *testing.T) {
	isolateTmux(t)
	globalDisk(t, runningRecord(t, "live", "2026-10-01T10:00:00Z"))
	testPanes(t)
	m, a := linkedHome(t, "")
	wirePanes(t, m)
	v, _ := m.viewByID(rowID(t, m, "live"))
	require.NotNil(t, v)
	require.True(t, v.Active(), "fixture: the row is active")
	m.ensureSlotPanes(m.workspaceSlot)
	old := m.panes.Get(v.TmuxSession)
	require.NotNil(t, old)

	crash(t, m, a)
	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)
	assert.Same(t, old, m.panes.Get(v.TmuxSession), "the client is kept")
}

// TestReconnect_ReleasesPanesOnAnotherTmuxServer: a daemon rejoined on
// another tmux server (the last one's died with it) has its sessions
// there, so every pane client is replaced, and this process pins the new
// server.
func TestReconnect_ReleasesPanesOnAnotherTmuxServer(t *testing.T) {
	isolateTmux(t)
	t.Cleanup(func() { tmux.UseServer("") })
	globalDisk(t, runningRecord(t, "live", "2026-10-01T10:00:00Z"))
	testPanes(t)
	m, a := linkedHome(t, "")
	wirePanes(t, m)
	v, _ := m.viewByID(rowID(t, m, "live"))
	require.NotNil(t, v)
	m.ensureSlotPanes(m.workspaceSlot)
	old := m.panes.Get(v.TmuxSession)
	require.NotNil(t, old)

	crash(t, m, a)
	const elsewhere = "/elsewhere/tmux.sock"
	b, _ := bootDaemonOn(t, &recordingExec{}, elsewhere)
	rejoinOnto(t, m, b)

	now := m.panes.Get(v.TmuxSession)
	require.NotNil(t, now, "the active row has a client again")
	assert.NotSame(t, old, now, "the old client is replaced")
	assert.False(t, old.Attached(), "and released")
	assert.Equal(t, elsewhere, m.daemonTmux)
	assert.Equal(t, []string{"tmux", "-u", "-S", elsewhere, "list-sessions"},
		tmux.Command(context.Background(), "list-sessions").Args, "the new server is pinned")
}

// TestReconnect_ClosesATabTheNewDaemonDoesNotServe: a workspace
// unregistered while the TUI was offline is not served by the next daemon:
// its tab is closed, with a note, or, when it was the only one shown, the
// TUI goes to global mode.
func TestReconnect_ClosesATabTheNewDaemonDoesNotServe(t *testing.T) {
	unregister := func(t *testing.T, name string) {
		t.Helper()
		reg, err := config.LoadWorkspaceRegistry()
		require.NoError(t, err)
		require.NoError(t, reg.Remove(name))
	}

	t.Run("one of two tabs", func(t *testing.T) {
		isolateTmux(t)
		twoTabs(t)
		m, a := linkedHome(t, "")
		require.Len(t, m.slots, 2)
		crash(t, m, a)
		unregister(t, "ws-b")
		b, _ := bootDaemon(t, &recordingExec{})
		rejoinOnto(t, m, b)

		require.Len(t, m.slots, 1)
		assert.Equal(t, "ws-a", m.name())
		assert.Contains(t, m.errBox.String(), "ws-b is no longer registered; closed its tab")
		assert.NoError(t, m.checkSlotInvariant())
	})

	t.Run("the only tab", func(t *testing.T) {
		isolateTmux(t)
		globalDisk(t)
		ws := writeWorkspaceState(t, "ws-b", "["+pausedRecord("other", "2026-10-01T11:00:00Z")+"]")
		savedRegistry(t, []string{"ws-b"}, "ws-b", ws)
		m, a := linkedHome(t, "")
		require.Len(t, m.slots, 1)
		crash(t, m, a)
		unregister(t, "ws-b")
		b, _ := bootDaemon(t, &recordingExec{})
		rejoinOnto(t, m, b)

		assert.Empty(t, m.slots, "global mode")
		assert.Empty(t, m.name())
		assert.Contains(t, m.errBox.String(), "ws-b is no longer registered; closed its tab")
		assert.NoError(t, m.checkSlotInvariant())
	})
}

// TestReconnect_AClassicWorkspaceNoLongerServedFallsBackToGlobal: with no
// tab open, the TUI shows the workspace it started on; unregistered while
// the TUI was offline, the next daemon serves it no more, and the TUI
// shows the global workspace, with a note.
func TestReconnect_AClassicWorkspaceNoLongerServedFallsBackToGlobal(t *testing.T) {
	isolateTmux(t)
	globalDisk(t)
	ws := writeWorkspaceState(t, "ws-b", "["+pausedRecord("other", "2026-10-01T11:00:00Z")+"]")
	savedRegistry(t, nil, "", ws)
	m, a := linkedHome(t, "ws-b")
	require.Empty(t, m.slots)
	require.Equal(t, "ws-b", m.name(), "the classic slot shows the startup workspace")
	crash(t, m, a)
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.Remove("ws-b"))
	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)

	assert.Empty(t, m.slots)
	assert.Empty(t, m.name(), "the global workspace")
	assert.True(t, b.c.IsLoaded(m.id))
	assert.Contains(t, m.errBox.String(), "ws-b is no longer registered; showing global")
	assert.NoError(t, m.checkSlotInvariant())
}

// TestReconnect_SendsTheTabFocusedOffline: a tab focused while offline is
// the registry's last used once a daemon is joined, as the UI prefs are.
func TestReconnect_SendsTheTabFocusedOffline(t *testing.T) {
	isolateTmux(t)
	twoTabs(t)
	m, a := linkedHome(t, "")
	require.Equal(t, "ws-a", m.name())
	crash(t, m, a)
	pressScript(t, m, keyFor(t, "}"))
	require.Equal(t, "ws-b", m.name(), "tabs switch offline")
	assert.Equal(t, "ws-b", m.unsentLastUsed)
	assert.Equal(t, "ws-a", lastUsedOnDisk(t), "no daemon wrote it")

	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)
	assert.Empty(t, m.unsentLastUsed, "sent")
	assert.Equal(t, "ws-b", lastUsedOnDisk(t))
}

// TestReconnect_AFailedOpenShowsItsError: a workspace the new daemon
// serves but cannot load (its state.json broke while the TUI was offline)
// keeps its tab, and its open's error is shown.
func TestReconnect_AFailedOpenShowsItsError(t *testing.T) {
	isolateTmux(t)
	_, wsB := twoTabs(t)
	m, a := linkedHome(t, "")
	require.Len(t, m.slots, 2)
	crash(t, m, a)
	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&wsB), config.StateFileName),
		[]byte(`{"instances":{"not":"an array"}}`), 0o644))
	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)

	assert.Len(t, m.slots, 2, "the tab stays")
	assert.Contains(t, m.errBox.String(), "reopen ws-b")
}
