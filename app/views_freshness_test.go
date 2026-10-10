package app

import (
	"github.com/aidan-bailey/loom/config"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The TUI reads instances from its view store, which the model's changes
// reach at the drain (core.ViewsChanged), and the split pane and menu hold
// copies of the selected row. These tests pin the freshness machinery: what
// used to read the shared instance must still see a change within the
// Update that made it, and a copy must follow the row it shows.

// agentPane renders the split pane's agent box, sized so it shows its text.
func agentPane(m *home) string {
	return ansi.Strip(m.splitPane.String())
}

// confirmKey presses the confirmation overlay's confirm key, as the user
// does on a kill's or a pause's "[!] … ?".
func confirmKey(m *home) {
	_, _ = handleStateConfirmKey(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
}

// TestViewsChanged_RefreshesTheSelectionsCopies: a tick that finds the
// selected session gone pauses it and emits no InstancesChanged, so only
// the ViewsChanged applier repoints the split pane and the menu at the new
// row. Before the store they held the live instance, which showed the pause
// at once.
func TestViewsChanged_RefreshesTheSelectionsCopies(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	// Marked started, as a restored record whose session runs is: the
	// tick probes only started sessions.
	require.NoError(t, inst.EnsureRunning())
	m.syncViews()
	_ = m.instanceChanged()
	require.Equal(t, session.Running, m.splitPane.Instance().Status, "fixture: the pane shows the running row")
	require.Contains(t, ansi.Strip(m.menu.String()), "stash", "fixture: a running session offers stash")

	deliver(t, m, core.HealthResult{Results: []core.ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessDead}}})

	require.Equal(t, session.Paused, inst.GetStatus(), "fixture: the tick paused it")
	assert.Equal(t, session.Paused, m.splitPane.Instance().Status, "the split pane's copy follows the row")
	menu := ansi.Strip(m.menu.String())
	assert.Contains(t, menu, "resume", "the menu offers resume for the paused row")
	assert.NotContains(t, menu, "stash")
}

// TestViewsChanged_PrunesBellsOfGoneInstances: bells are keyed by IDs that
// name one record, so the bell of an instance no slot shows any more is
// dropped when the views change; a shown instance keeps its bell.
func TestViewsChanged_PrunesBellsOfGoneInstances(t *testing.T) {
	m := homeWithAppState(t)
	kept := mustAddInstance(t, m, "kept")
	gone := mustAddInstance(t, m, "gone")
	keptID, goneID := idOf(m, kept), idOf(m, gone)
	ring(m, kept)
	ring(m, gone)

	m.ws().RemoveForTest(gone)
	m.Update(keyupMsg{}) // any Update drains, publishing the removal

	assert.NotContains(t, m.bells, goneID, "a gone instance's bell is pruned")
	assert.True(t, m.bells[keptID], "a shown instance keeps its bell")
}

// TestViewsChanged_KeepsOverlaysOfOtherSlots: the overlays are pruned
// against every open slot's views, not only the slot whose views changed,
// so a bell and a ladder status on a row of an unfocused slot survive a
// change to the focused one; the changed slot's gone row loses both.
func TestViewsChanged_KeepsOverlaysOfOtherSlots(t *testing.T) {
	m := fleetHome(t)
	m.viewMode = viewFocus
	peer := liveInstance(t, "b-live")
	m.slots[1].ws().AddForTest(peer)
	gone := liveInstance(t, "f-gone")
	m.slots[0].ws().AddForTest(gone)
	m.syncViews()
	peerID, goneID := idOf(m, peer), idOf(m, gone)
	ring(m, peer)
	m.setLadder(peerID, session.Prompting)
	ring(m, gone)
	m.setLadder(goneID, session.Ready)

	m.slots[0].ws().RemoveForTest(gone)
	m.Update(keyupMsg{}) // any Update drains, publishing the focused slot's change

	assert.True(t, m.bells[peerID], "the unfocused slot's bell survives")
	assert.Contains(t, m.ladder, peerID, "and so does its ladder status")
	assert.NotContains(t, m.bells, goneID, "a gone row's bell is pruned")
	assert.NotContains(t, m.ladder, goneID, "and its ladder status")
}

// TestViewsChanged_PrunesTheLadderOfInactiveAndReportedRows: a ladder
// status never overrides a lifecycle status or one Claude reports, and
// must not resurface after a pause and resume, or once the report goes
// quiet; the views that say so prune it.
func TestViewsChanged_PrunesTheLadderOfInactiveAndReportedRows(t *testing.T) {
	m := homeWithAppState(t)
	paused := liveInstance(t, "paused")
	reported := startedInstanceWithProgram(t, "reported", "claude", "x")
	m.ws().AddForTest(paused)
	m.ws().AddForTest(reported)
	m.syncViews()
	m.setLadder(idOf(m, paused), session.Prompting)
	m.setLadder(idOf(m, reported), session.Ready)

	require.NoError(t, paused.TransitionTo(session.Paused))
	applyHookEvents(t, reported, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})
	applyClaudeStatus(m, reported) // rereads the stores, without the applier's prune
	require.Contains(t, m.ladder, idOf(m, paused), "fixture: not pruned yet")
	assert.Equal(t, session.Paused, shownStatus(t, m, paused), "the overlay never covers a lifecycle status")
	assert.Equal(t, session.Prompting, shownStatus(t, m, reported), "nor a reported one")

	m.Update(keyupMsg{})

	assert.NotContains(t, m.ladder, idOf(m, paused), "an inactive row loses its ladder status")
	assert.NotContains(t, m.ladder, idOf(m, reported), "and so does a reported one")
}

// peerLiveHome is a two-tab home whose peer tab ("bpeer") shows b1
// (Ready) and a live session in status st, plus that session.
func peerLiveHome(t *testing.T, st session.Status) (*home, *session.Instance) {
	t.Helper()
	isolateTmux(t)
	m := fleetHome(t)
	m.viewMode = viewFocus
	inst := liveInstance(t, "b-live")
	require.NoError(t, inst.TransitionTo(st))
	m.slots[1].ws().AddForTest(inst)
	m.syncViews()
	m.updateTabBarStatuses()
	return m, inst
}

// TestLadderWrites_ReachTheTabBarWithinTheUpdate: each pane-ladder write
// refreshes the tab statuses and peer summaries in the same Update, from
// rows that already carry the write; nothing later in the Update refreshes
// them again.
func TestLadderWrites_ReachTheTabBarWithinTheUpdate(t *testing.T) {
	t.Run("output promotes Ready to Running", func(t *testing.T) {
		m, inst := peerLiveHome(t, session.Ready)
		require.Equal(t, []ui.PeerSection{{Name: "bpeer", Idle: 2}}, m.list.PeerSections(), "fixture")

		m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

		require.Equal(t, session.Running, m.ladder[idOf(m, inst)].status, "the ladder promoted the shown status")
		assert.Equal(t, []ui.PeerSection{{Name: "bpeer", Running: 1, Idle: 1}}, m.list.PeerSections())
	})
	t.Run("an event-path detection", func(t *testing.T) {
		m, inst := peerLiveHome(t, session.Running)
		require.Equal(t, []ui.PeerSection{{Name: "bpeer", Running: 1, Idle: 1}}, m.list.PeerSections(), "fixture")

		m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, hasPrompt: true})

		require.Equal(t, session.Prompting, m.ladder[idOf(m, inst)].status, "the ladder recorded the prompt")
		assert.Equal(t, []ui.PeerSection{{Name: "bpeer", Attention: 1, Idle: 1}}, m.list.PeerSections())
	})
	t.Run("a snapshot-path scan", func(t *testing.T) {
		m, inst := peerLiveHome(t, session.Running)

		m.Update(snapshotStatusMsg{results: []snapshotStatus{{id: idOf(m, inst), title: inst.Title, hasPrompt: true}}})

		require.Equal(t, session.Prompting, m.ladder[idOf(m, inst)].status, "the ladder recorded the prompt")
		assert.Equal(t, []ui.PeerSection{{Name: "bpeer", Attention: 1, Idle: 1}}, m.list.PeerSections())
	})
}

// TestConfirmedKill_ShowsDeletingWithinTheUpdate: the confirmation's sync
// step marks the session Deleting, and the instanceChanged that follows it
// hands the panes and the rail a row that says so.
func TestConfirmedKill_ShowsDeletingWithinTheUpdate(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)
	_, _ = runKillSelected(m)
	require.Equal(t, stateConfirm, m.state)

	confirmKey(m)

	assert.Equal(t, session.Deleting, m.splitPane.Instance().Status)
	assert.Equal(t, session.Deleting, m.list.GetSelectedInstance().Status)
}

// TestConfirmedPause_ShowsSettingUpWithinTheUpdate: the confirmed pause's
// Loading reaches the agent pane in the same Update, which renders the
// "Setting up workspace..." screen until the pause lands.
func TestConfirmedPause_ShowsSettingUpWithinTheUpdate(t *testing.T) {
	m := homeWithAppState(t)
	m.splitPane.SetSize(100, 40)
	addReadyInstance(t, m)
	_, _ = runStashSelectedOpts(m, true, false)
	require.Equal(t, stateConfirm, m.state)

	m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})

	assert.Contains(t, agentPane(m), "Setting up workspace")
}

// TestUnconfirmedOps_TheRowsShowTheWrite: the kill and pause that skip the
// confirmation write the model directly, and the rows show the write as
// soon as they return, as the shared instance did.
func TestUnconfirmedOps_TheRowsShowTheWrite(t *testing.T) {
	t.Run("kill", func(t *testing.T) {
		m := homeWithAppState(t)
		addReadyInstance(t, m)
		_, _ = runKillSelectedNoConfirm(m)
		assert.Equal(t, session.Deleting, m.list.GetSelectedInstance().Status)
	})
	t.Run("pause", func(t *testing.T) {
		m := homeWithAppState(t)
		addReadyInstance(t, m)
		_, _ = runStashSelectedOpts(m, false, false)
		assert.Equal(t, session.Loading, m.list.GetSelectedInstance().Status)
	})
}

// TestResumeAndRecover_ShowSettingUpWithinTheUpdate: resume and recover
// move the session to Loading before their job runs, and the agent pane
// renders that at once.
func TestResumeAndRecover_ShowSettingUpWithinTheUpdate(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		m, _ := newPausedInstanceHome(t)
		m.splitPane.SetSize(100, 40)
		_ = m.instanceChanged()
		require.Contains(t, agentPane(m), "Session is paused", "fixture")

		_, _ = runResumeSelected(m)

		assert.Contains(t, agentPane(m), "Setting up workspace")
	})
	t.Run("recover", func(t *testing.T) {
		m := homeWithAppState(t)
		m.splitPane.SetSize(100, 40)
		placeholder, err := session.FromInstanceData(session.InstanceData{
			Title: "orphan", Status: session.Recoverable, Program: "claude",
			Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(), BranchName: "u/orphan"},
		}, t.TempDir())
		require.NoError(t, err)
		m.ws().AddForTest(placeholder)
		m.syncViews()
		_ = m.instanceChanged()
		require.Contains(t, agentPane(m), "Recoverable session", "fixture")

		_, _ = runRecoverSelected(m)

		assert.Contains(t, agentPane(m), "Setting up workspace")
	})
}

// TestIssuePicked_SelectsTheNewRow: a pick adds the issue's session and
// selects it while Launch Options is open, even with another row selected
// before.
func TestIssuePicked_SelectsTheNewRow(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	mustAddInstance(t, m, "a")
	require.Equal(t, "a", m.list.GetSelectedInstance().Title, "fixture")

	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "Fix flaky test", URL: "https://x/12"}})

	require.Equal(t, stateLaunchOptions, m.state)
	assert.Equal(t, "gh-12-fix-flaky-test", m.list.GetSelectedInstance().Title)
}

// TestAttachDone_RepairsTheRowAsItIsNow: a full-screen attach holds the
// event loop for its whole run, so the store can lag the model when it
// returns. A session paused meanwhile (a script's inst:pause()) must not get
// its client back from the stale row.
func TestAttachDone_RepairsTheRowAsItIsNow(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "fs")
	m.ws().AddForTest(inst)
	m.syncViews()
	name := inst.Pane().TmuxSessionName()
	row := rowOf(t, m, inst)
	_, _ = m.Update(startFullScreenAttachMsg{instance: row, target: attachTargetAgent})
	require.False(t, m.panes.Alive(name), "fixture: the attach took the session from the client")

	require.NoError(t, inst.TransitionTo(session.Paused))
	_, _ = m.Update(attachDoneMsg{instance: row})

	assert.False(t, m.panes.Alive(name), "a session paused during the attach gets no client back")
}

// TestScriptDone_ReadsWhatTheScriptChanged: a script changes instances
// through the model (inst:pause(), ctx:new_instance), so what its calls
// did is in the stores by the time it finishes: a session it paused shows
// as paused, and a session it created is the row instanceChanged shows
// when it is the only one.
func TestScriptDone_ReadsWhatTheScriptChanged(t *testing.T) {
	t.Run("an instance the script paused", func(t *testing.T) {
		isolateTmux(t)
		m := homeWithAppState(t)
		m.splitPane.SetSize(100, 40)
		inst := startedInstanceWithProgram(t, "lua-paused", "claude", "idle")
		m.ws().AddForTest(inst)
		selectIn(m, m.list, inst)
		_ = m.instanceChanged()
		withScript(t, m, `cs.bind("Z", function(ctx) ctx:selected():pause() end)`)

		require.NoError(t, lastErr(t, runKey(t, m, "Z")))

		assert.Contains(t, agentPane(m), "Session is paused")
	})
	t.Run("an instance the script created", func(t *testing.T) {
		m := homeWithAppState(t)
		m.splitPane.SetSize(100, 40)
		_ = m.instanceChanged()
		require.Contains(t, agentPane(m), "No agents running yet", "fixture")
		withScript(t, m, `cs.bind("Z", function(ctx) ctx:new_instance{title = "scripted"} end)`)

		require.NoError(t, lastErr(t, runKey(t, m, "Z")))

		assert.Contains(t, agentPane(m), "Please enter a name for the instance")
	})
}

// TestWorkspacesChanged_RefreshesTheSlotsView: a slot caches its
// workspace's view, and the drain's WorkspacesChanged replaces it with the
// model's newest, so a change the model made on its own (a load's recovery
// summary, the classic workspace's first load, a pref written elsewhere)
// reaches the slot without the slot rereading it.
func TestWorkspacesChanged_RefreshesTheSlotsView(t *testing.T) {
	m := newTestHome(t)
	require.NoError(t, m.appState().SetUIPrefs(config.UIPrefs{RailHidden: true}))
	require.False(t, m.uiPrefs().RailHidden, "fixture: the slot's copy is stale")

	_ = m.drainCore()

	assert.True(t, m.uiPrefs().RailHidden, "the drain's WorkspacesChanged refreshed the slot's view")
}
