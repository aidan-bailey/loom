package app

import (
	"encoding/json"
	"errors"
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningInstance adds a Running (never actually started) instance to
// m's focused list.
func runningInstance(t *testing.T, m *home, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	m.list.AddInstance(inst)
	return inst
}

// TestCompletionDuringNaming_CancelKillsOnlyThePendingInstance is the
// reviewer's repro: a start completing while a second new instance is
// being named moved the selection onto the started one, and ctrl+c — which
// popped the selection — then killed it (worktree, branch -D).
func TestCompletionDuringNaming_CancelKillsOnlyThePendingInstance(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	m.errBox.SetSize(400, 1)
	first := runningInstance(t, m, "first")
	_, _ = runNewInstance(m)
	require.Equal(t, stateNew, m.state)
	pending := m.list.GetSelectedInstance()
	require.NotSame(t, first, pending)

	_, _ = m.Update(instanceStartedMsg{instance: first, slot: m.workspaceSlot})
	assert.Same(t, pending, m.list.GetSelectedInstance(), "a completion must not move the selection under the naming flow")
	assert.Equal(t, stateNew, m.state)
	assert.Contains(t, m.errBox.String(), "first", "the start is still announced")

	_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.Contains(t, m.list.GetInstances(), first, "cancel must not kill the started session")
	assert.NotContains(t, m.list.GetInstances(), pending, "cancel removes the pending instance")
	assert.Equal(t, session.Running, first.GetStatus())
}

// TestCompletionDuringInlineAttach_KeepsTheAttachTarget: inline attach
// forwards each key to the selected instance, so a completion that moved
// the selection retargeted the user's typing.
func TestCompletionDuringInlineAttach_KeepsTheAttachTarget(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	attached := runningInstance(t, m, "attached")
	first := runningInstance(t, m, "first")
	m.list.SelectInstance(attached)
	m.state = stateInlineAttach

	_, _ = m.Update(instanceStartedMsg{instance: first, slot: m.workspaceSlot})
	assert.Same(t, attached, m.list.GetSelectedInstance(), "keys must keep going to the attached session")
	assert.Equal(t, stateInlineAttach, m.state)
}

// TestRecoverDuringNaming_LeavesThePendingInstanceAlone: a recovered row
// appended during naming became the list's last row — the one the naming
// flow edited — and was selected, so cancel then killed it.
func TestRecoverDuringNaming_LeavesThePendingInstanceAlone(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	placeholder, err := session.FromInstanceData(session.InstanceData{
		Title: "orphan", Status: session.Recoverable, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, placeholder.TransitionTo(session.Loading))
	m.list.AddInstance(placeholder)
	_, _ = runNewInstance(m)
	pending := m.list.GetSelectedInstance()
	recovered, err := session.NewInstance(session.InstanceOptions{Title: "orphan", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, recovered.TransitionTo(session.Running))

	_, _ = m.Update(recoverDoneMsg{oldTitle: "orphan", recovered: recovered, placeholder: placeholder, slot: m.workspaceSlot})
	assert.Same(t, pending, m.list.GetSelectedInstance(), "the recover must not move the selection under the naming flow")

	typeTitle(t, m, "x")
	assert.Equal(t, "x", pending.Title, "typing names the pending instance")
	assert.Equal(t, "orphan", recovered.Title, "not the recovered row")

	_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.Contains(t, m.list.GetInstances(), recovered, "cancel must not remove the recovered session")
	assert.NotContains(t, m.list.GetInstances(), pending)
}

// TestInstanceStarted_OwnerReopened: the owner was closed and the same
// workspace reopened while the start ran. The reopened slot loaded the
// record as a never-started twin.
func TestInstanceStarted_OwnerReopened(t *testing.T) {
	isolateTmux(t)
	setup := func(t *testing.T) (m *home, owner *workspaceSlot, twin *session.Instance, recA, recC *recordingInstanceStorage) {
		t.Helper()
		m, recA, _ = ownerTestHome(t)
		owner = m.workspaceSlot
		drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
		reopened := fleetSlot(t, "afocus", "late")
		recC = &recordingInstanceStorage{}
		var err error
		reopened.storage, err = session.NewStorage(recC, t.TempDir())
		require.NoError(t, err)
		m.slots = append(m.slots, reopened)
		twin = reopened.list.GetInstanceByTitle("late")
		recA.calls = 0
		return m, owner, twin, recA, recC
	}

	t.Run("success takes the twin's place", func(t *testing.T) {
		m, owner, twin, recA, recC := setup(t)
		started := liveInstance(t, "late")
		owner.list.AddInstance(started)

		_, cmd := m.Update(instanceStartedMsg{instance: started, slot: owner})
		drainCmd(cmd)

		reopened := m.slots[1]
		assert.Same(t, started, reopened.list.GetInstanceByTitle("late"), "the started instance replaces the twin")
		assert.NotContains(t, reopened.list.GetInstances(), twin)
		assert.GreaterOrEqual(t, recC.calls, 1, "the reopened slot is saved")
		assert.Zero(t, recA.calls, "the closed owner's stale copy is not")
		assert.True(t, started.PtmxAlive(), "it is displayed again, so its preview stays")
	})

	t.Run("failure leaves the twin's worktree and branch alone", func(t *testing.T) {
		m, owner, twin, _, _ := setup(t)
		started := liveInstance(t, "late")
		owner.list.AddInstance(started)

		_, cmd := m.Update(instanceStartedMsg{instance: started, err: errors.New("boom"), slot: owner})
		drainCmd(cmd)

		assert.True(t, started.Started(), "not killed: the reopened record owns its resources")
		assert.False(t, started.PtmxAlive(), "only its preview client is released")
		assert.Same(t, twin, m.slots[1].list.GetInstanceByTitle("late"))
	})
}

// TestKillAction_UsesTheDispatchSlotsStorage: killAction runs for seconds
// in a Cmd. It read m.storage there, so after a tab switch it deleted the
// record from whichever workspace was focused by then.
func TestKillAction_UsesTheDispatchSlotsStorage(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	m, recA, recB := ownerTestHome(t)
	a1, err := session.FromInstanceData(session.InstanceData{
		Title: "a1", Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: t.TempDir(), BranchName: "loom/a1", SessionName: "a1"},
	}, t.TempDir())
	require.NoError(t, err)
	m.list.AddInstance(a1)
	seed, err := json.Marshal([]session.InstanceData{a1.ToInstanceData()})
	require.NoError(t, err)
	recA.lastData = seed

	_, killAction := killActionFor(m, a1)
	m.switchWorkspaceSlot(1)
	_ = killAction()

	assert.GreaterOrEqual(t, recA.calls, 1, "the record is deleted from its own workspace")
	assert.NotContains(t, string(recA.lastData), `"a1"`)
	assert.Zero(t, recB.calls, "the workspace focused at completion is untouched")
}

// TestCreationCancelPaths_KillThePendingInstanceByIdentity is the defence
// in depth: whatever moved the selection mid-flow, every creation-flow
// cancel path removes the pending instance and leaves the selected one.
func TestCreationCancelPaths_KillThePendingInstanceByIdentity(t *testing.T) {
	type flow struct {
		name   string
		open   func(m *home)
		cancel func(m *home)
	}
	naming := func(m *home) { _, _ = runNewInstance(m) }
	toLaunchOptions := func(m *home) {
		naming(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, stateLaunchOptions, m.state)
	}
	toPrompt := func(m *home) {
		_, _ = runPromptNewInstance(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, statePrompt, m.state)
	}
	for _, f := range []flow{
		{"ctrl+c while naming", naming, func(m *home) { _, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) }},
		{"esc while naming", naming, func(m *home) { _, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEsc}) }},
		{"launch options cancelled", toLaunchOptions, func(m *home) { _, _ = m.cancelLaunchOptions() }},
		{"prompt overlay cancelled", toPrompt, func(m *home) { _ = m.cancelPromptOverlay() }},
		{"remote-control confirm cancelled", toLaunchOptions, func(m *home) {
			_ = m.promptRemoteControlBlocked(overlay.ConfirmationTask{})
			m.confirmation().OnCancel()
			drainCmd(m.pendingConfirmation.Async)
		}},
	} {
		t.Run(f.name, func(t *testing.T) {
			m := newTestHome(t)
			first := runningInstance(t, m, "first")
			f.open(m)
			pending := m.pendingNew
			require.NotNil(t, pending)
			m.list.SelectInstance(first) // however it moved
			f.cancel(m)
			assert.Contains(t, m.list.GetInstances(), first, "the selected session must survive the cancel")
			assert.NotContains(t, m.list.GetInstances(), pending, "the pending instance is removed")
			assert.Nil(t, m.pendingNew)
		})
	}
}
