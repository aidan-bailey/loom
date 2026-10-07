package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
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

// TestViewsChanged_PrunesBellsOfGoneInstances: bells are keyed by IDs the
// model never reuses, so the bell of an instance no slot shows any more is
// dropped when the views change; a shown instance keeps its bell.
func TestViewsChanged_PrunesBellsOfGoneInstances(t *testing.T) {
	m := homeWithAppState(t)
	kept := mustAddInstance(t, m, "kept")
	gone := mustAddInstance(t, m, "gone")
	keptID, goneID := idOf(m, kept), idOf(m, gone)
	ring(m, kept)
	ring(m, gone)

	m.ws.Remove(gone)
	m.Update(keyupMsg{}) // any Update drains, publishing the removal

	assert.NotContains(t, m.bells, goneID, "a gone instance's bell is pruned")
	assert.True(t, m.bells[keptID], "a shown instance keeps its bell")
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
	m.slots[1].ws.Add(inst)
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

		require.Equal(t, session.Running, inst.GetStatus())
		assert.Equal(t, []ui.PeerSection{{Name: "bpeer", Running: 1, Idle: 1}}, m.list.PeerSections())
	})
	t.Run("an event-path detection", func(t *testing.T) {
		m, inst := peerLiveHome(t, session.Running)
		require.Equal(t, []ui.PeerSection{{Name: "bpeer", Running: 1, Idle: 1}}, m.list.PeerSections(), "fixture")

		m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, hasPrompt: true})

		require.Equal(t, session.Prompting, inst.GetStatus())
		assert.Equal(t, []ui.PeerSection{{Name: "bpeer", Attention: 1, Idle: 1}}, m.list.PeerSections())
	})
	t.Run("a snapshot-path scan", func(t *testing.T) {
		m, inst := peerLiveHome(t, session.Running)

		m.Update(snapshotStatusMsg{results: []snapshotStatus{{id: idOf(m, inst), title: inst.Title, hasPrompt: true}}})

		require.Equal(t, session.Prompting, inst.GetStatus())
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
		m.ws.Add(placeholder)
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
	m.ws.Add(inst)
	m.syncViews()
	name := inst.Pane().TmuxSessionName()
	row := rowOf(t, m, inst)
	_, _ = m.Update(startFullScreenAttachMsg{instance: row, target: attachTargetAgent})
	require.False(t, m.panes.Alive(name), "fixture: the attach took the session from the client")

	require.NoError(t, inst.TransitionTo(session.Paused))
	_, _ = m.Update(attachDoneMsg{instance: row})

	assert.False(t, m.panes.Alive(name), "a session paused during the attach gets no client back")
}

// TestScriptDone_ReadsWhatTheScriptChanged: a script changes instances on
// its own goroutine (inst:pause()), so its completion reads them afresh
// before refreshing the panes; and a session it created
// (ctx:new_instance) is the row instanceChanged shows when it is the
// only one.
func TestScriptDone_ReadsWhatTheScriptChanged(t *testing.T) {
	t.Run("an instance the script paused", func(t *testing.T) {
		m := homeWithAppState(t)
		m.splitPane.SetSize(100, 40)
		inst := addReadyInstance(t, m)
		_ = m.instanceChanged()

		require.NoError(t, inst.TransitionTo(session.Paused))
		m.Update(scriptDoneMsg{})

		assert.Contains(t, agentPane(m), "Session is paused")
	})
	t.Run("an instance the script created", func(t *testing.T) {
		m := homeWithAppState(t)
		m.splitPane.SetSize(100, 40)
		_ = m.instanceChanged()
		require.Contains(t, agentPane(m), "No agents running yet", "fixture")
		inst, err := session.NewInstance(session.InstanceOptions{Title: "scripted", Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)

		m.Update(scriptDoneMsg{slot: m.workspaceSlot, pendingInstances: []*session.Instance{inst}})

		assert.Contains(t, agentPane(m), "Please enter a name for the instance")
	})
}
