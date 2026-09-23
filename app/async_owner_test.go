package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownerTestHome is fleetHome ("afocus" focused, "bpeer") with recording
// storages, so a test can see which workspace an async completion saved.
func ownerTestHome(t *testing.T) (m *home, recA, recB *recordingInstanceStorage) {
	t.Helper()
	m = fleetHome(t)
	m.ctx = cancelledCtx()
	m.viewMode = viewFocus
	m.errBox = ui.NewErrBox()
	recA, recB = &recordingInstanceStorage{}, &recordingInstanceStorage{}
	var err error
	m.slots[0].storage, err = session.NewStorage(recA, t.TempDir())
	require.NoError(t, err)
	m.slots[1].storage, err = session.NewStorage(recB, t.TempDir())
	require.NoError(t, err)
	return m, recA, recB
}

// startingInstance adds an unstarted instance to slot's list in Loading,
// as the launch-options confirm leaves it while Start runs.
func startingInstance(t *testing.T, slot *workspaceSlot, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Loading))
	slot.list.AddInstance(inst)
	return inst
}

// selectTitle selects the instance titled title in m's focused list.
func selectTitle(t *testing.T, m *home, title string) *session.Instance {
	t.Helper()
	inst := m.list.GetInstanceByTitle(title)
	require.NotNil(t, inst)
	m.list.SelectInstance(inst)
	return inst
}

// TestInstanceStarted_FailureAfterSwitchKillsOnlyTheFailedInstance: a failed
// start used to pop the focused list's selection for kill — after a tab switch,
// an unrelated session in another workspace, whose kill then deleted its
// worktree and branch — and leave the failed instance behind.
func TestInstanceStarted_FailureAfterSwitchKillsOnlyTheFailedInstance(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	owner := m.workspaceSlot
	starting := startingInstance(t, owner, "new-one")
	m.switchWorkspaceSlot(1)
	victim := selectTitle(t, m, "b1")

	_, _ = m.Update(instanceStartedMsg{instance: starting, err: errors.New("boom"), slot: owner})

	assert.Same(t, victim, m.slots[1].list.GetInstanceByTitle("b1"), "the focused workspace's selected session must be untouched")
	assert.NotEqual(t, session.Deleting, victim.GetStatus())
	assert.Nil(t, owner.list.GetInstanceByTitle("new-one"), "the failed instance is removed from its owner")
	require.NoError(t, m.checkSlotInvariant())
}

// TestInstanceStarted_SuccessAfterSwitchStaysInItsWorkspace: a successful
// start used to inline-attach to the focused workspace's selection and
// save the focused workspace's storage, leaving the owner's record at
// Loading for the next load's reconcile to kill.
func TestInstanceStarted_SuccessAfterSwitchStaysInItsWorkspace(t *testing.T) {
	m, recA, recB := ownerTestHome(t)
	owner := m.workspaceSlot
	starting := startingInstance(t, owner, "new-one")
	starting.SetPrompt("do the thing")
	require.NoError(t, starting.TransitionTo(session.Running))
	m.switchWorkspaceSlot(1)
	m.errBox.SetSize(400, 1)

	_, _ = m.Update(instanceStartedMsg{instance: starting, slot: owner})

	assert.Equal(t, stateDefault, m.state, "no inline attach into another workspace's pane")
	assert.Equal(t, "bpeer", focusedName(m), "focus stays where the user put it")
	assert.GreaterOrEqual(t, recA.calls, 1, "the owner's storage is saved")
	assert.Contains(t, string(recA.lastData), "new-one")
	assert.Zero(t, recB.calls, "the focused workspace's storage is not touched")
	assert.Same(t, starting, owner.list.GetSelectedInstance(), "selected in its own workspace")
	assert.Empty(t, starting.Prompt(), "the pending prompt belongs to the instance and is sent anyway")
	assert.Contains(t, m.errBox.String(), "new-one")
}

// TestInstanceStarted_SuccessInFocusedWorkspaceAttaches pins the ordinary
// path: the owner is focused, so the new session is selected and attached.
func TestInstanceStarted_SuccessInFocusedWorkspaceAttaches(t *testing.T) {
	m, recA, recB := ownerTestHome(t)
	starting := startingInstance(t, m.workspaceSlot, "new-one")
	require.NoError(t, starting.TransitionTo(session.Running))

	_, _ = m.Update(instanceStartedMsg{instance: starting, slot: m.workspaceSlot})

	assert.Equal(t, stateInlineAttach, m.state)
	assert.Same(t, starting, m.list.GetSelectedInstance())
	assert.GreaterOrEqual(t, recA.calls, 1)
	assert.Zero(t, recB.calls)
}

// TestInstanceStarted_AfterOwnerDropped: the owner tab was closed while
// the start ran. Nothing displays the instance, so its preview client is
// released, and the owner's storage is still saved so the record isn't
// left at Loading.
func TestInstanceStarted_AfterOwnerDropped(t *testing.T) {
	isolateTmux(t)
	m, recA, recB := ownerTestHome(t)
	owner := m.workspaceSlot
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
	require.Equal(t, []string{"bpeer"}, m.slotNames())
	recA.calls, recB.calls = 0, 0
	// The start finishes after the drop: live, with its preview attached.
	started := liveInstance(t, "late")
	owner.list.AddInstance(started)

	_, cmd := m.Update(instanceStartedMsg{instance: started, slot: owner})

	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.list.GetInstanceByTitle("late"), "not filed under the focused workspace")
	assert.GreaterOrEqual(t, recA.calls, 1, "the closed owner's record is saved")
	assert.Contains(t, string(recA.lastData), "late")
	assert.Zero(t, recB.calls)
	assertReleased(t, m, cmd, started)
}

// TestRecoverDone_AfterSwitchActsOnTheOwnerByIdentity: recoverDoneMsg used
// to act on the focused list by title — after a tab switch removing a
// same-titled row in another workspace, filing the recovered session
// there, saving that workspace, and stranding the placeholder.
func TestRecoverDone_AfterSwitchActsOnTheOwnerByIdentity(t *testing.T) {
	newPlaceholder := func(t *testing.T, slot *workspaceSlot) *session.Instance {
		t.Helper()
		p, err := session.FromInstanceData(session.InstanceData{
			Title: "dup", Status: session.Recoverable, Program: "claude", IsWorkspaceTerminal: true,
		}, t.TempDir())
		require.NoError(t, err)
		require.NoError(t, p.TransitionTo(session.Loading))
		slot.list.AddInstance(p)
		return p
	}

	t.Run("success", func(t *testing.T) {
		m, recA, recB := ownerTestHome(t)
		owner := m.workspaceSlot
		placeholder := newPlaceholder(t, owner)
		m.switchWorkspaceSlot(1)
		bystander := startingInstance(t, m.workspaceSlot, "dup")
		recovered, err := session.NewInstance(session.InstanceOptions{Title: "dup", Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		require.NoError(t, recovered.TransitionTo(session.Running))

		_, _ = m.Update(recoverDoneMsg{oldTitle: "dup", recovered: recovered, placeholder: placeholder, slot: owner})

		assert.Same(t, bystander, m.slots[1].list.GetInstanceByTitle("dup"), "the same-titled row elsewhere is untouched")
		assert.NotContains(t, m.slots[1].list.GetInstances(), recovered)
		assert.Contains(t, owner.list.GetInstances(), recovered, "filed under its own workspace")
		assert.NotContains(t, owner.list.GetInstances(), placeholder, "the placeholder is replaced")
		assert.GreaterOrEqual(t, recA.calls, 1, "the owner's storage is saved")
		assert.Zero(t, recB.calls, "the focused workspace's storage is not touched")
	})

	t.Run("failure reverts the placeholder, not a namesake", func(t *testing.T) {
		m, _, _ := ownerTestHome(t)
		owner := m.workspaceSlot
		placeholder := newPlaceholder(t, owner)
		m.switchWorkspaceSlot(1)
		bystander := startingInstance(t, m.workspaceSlot, "dup")

		_, _ = m.Update(recoverDoneMsg{oldTitle: "dup", err: errors.New("boom"), placeholder: placeholder, slot: owner})

		assert.Equal(t, session.Recoverable, placeholder.GetStatus(), "the placeholder is back to Recoverable for a retry")
		assert.Equal(t, session.Loading, bystander.GetStatus(), "the namesake is untouched")
	})
}

// TestResumeDone_AfterOwnerDropped: the owner tab was closed while a
// resume ran. releaseSlotCmd skipped the instance (nothing was attached
// yet), and the finished resume attached a preview client that nothing
// displays — so the completion must release it.
func TestResumeDone_AfterOwnerDropped(t *testing.T) {
	isolateTmux(t)
	m, _, recB := ownerTestHome(t)
	owner := m.workspaceSlot
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
	recB.calls = 0
	resumed := liveInstance(t, "resumed")
	owner.list.AddInstance(resumed)

	_, cmd := m.Update(resumeDoneMsg{instance: resumed, slot: owner})

	assert.Nil(t, m.list.GetInstanceByTitle("resumed"), "not filed under the focused workspace")
	assert.Zero(t, recB.calls)
	assertReleased(t, m, cmd, resumed)
}

// TestResumeDone_OwnerReopened: the owner was closed and its workspace
// reopened mid-resume. The reopened slot reconciled the record into a
// Paused twin; a resumed session that survived the reopen takes its place.
func TestResumeDone_OwnerReopened(t *testing.T) {
	isolateTmux(t)
	wtPath := filepath.Join(t.TempDir(), "res-wt")
	m, owner, twin, _, recC := reopenedHome(t, "res", wtPath, deadCmdExecForTest())
	resumed := startedWorktreeInstance(t, "res", wtPath, newFakeTmuxServer())
	owner.list.AddInstance(resumed)

	_, cmd := m.Update(resumeDoneMsg{instance: resumed, slot: owner})
	drainCmd(cmd)

	reopened := m.slots[1]
	assert.Same(t, resumed, reopened.list.GetInstanceByTitle("res"))
	assert.NotContains(t, reopened.list.GetInstances(), twin)
	assert.GreaterOrEqual(t, recC.calls, 1, "the reopened slot is saved")
	assert.True(t, resumed.Pane().PtmxAlive(), "displayed again, so its preview stays")
}

// TestResumeFailed_AfterOwnerDroppedReleasesPreview: a resume attaches its
// preview client before its checkpoint save, and a failing save comes back
// as transitionFailedMsg, which reverted the instance to Paused but left
// that client open. With the owner closed meanwhile, nothing displays the
// instance, so the client leaked until exit.
func TestResumeFailed_AfterOwnerDroppedReleasesPreview(t *testing.T) {
	isolateTmux(t)
	failedResume := func(inst *session.Instance) transitionFailedMsg {
		return transitionFailedMsg{inst: inst, title: inst.Title, op: "resume", previousStatus: session.Paused,
			err: errors.New("resume checkpoint save: disk full")}
	}

	t.Run("owner closed", func(t *testing.T) {
		m, _, _ := ownerTestHome(t)
		owner := m.workspaceSlot
		drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
		// finishResume leaves it Running, preview attached, when the save fails.
		resumed := liveInstance(t, "resumed")
		owner.list.AddInstance(resumed)

		_, cmd := m.Update(failedResume(resumed))

		assert.Equal(t, session.Paused, resumed.GetStatus(), "reverted, so the user can retry")
		assertReleased(t, m, cmd, resumed)
	})

	t.Run("control: owner loaded, the preview stays", func(t *testing.T) {
		m, _, _ := ownerTestHome(t)
		resumed := liveInstance(t, "resumed")
		m.list.AddInstance(resumed)

		_, cmd := m.Update(failedResume(resumed))
		drainCmd(cmd)

		assert.Equal(t, session.Paused, resumed.GetStatus())
		assert.True(t, resumed.Pane().PtmxAlive(), "a displayed instance keeps its preview")
	})
}
