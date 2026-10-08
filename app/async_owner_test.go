package app

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
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
	storageA, err := session.NewStorage(recA, t.TempDir())
	require.NoError(t, err)
	reworkspace(t, m, m.slots[0], func(p *core.WorkspaceParts) { p.Storage = storageA })
	storageB, err := session.NewStorage(recB, t.TempDir())
	require.NoError(t, err)
	reworkspace(t, m, m.slots[1], func(p *core.WorkspaceParts) { p.Storage = storageB })
	return m, recA, recB
}

// startingInstance adds an unstarted instance to slot's list in Loading,
// as the launch-options confirm leaves it while Start runs.
func startingInstance(t *testing.T, slot *workspaceSlot, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Loading))
	slot.ws().AddForTest(inst)
	return inst
}

// finishStart leaves inst as a successful Instance.Start(true) does before
// its StartResult exists: started and Running, on a mock tmux session that
// answers alive (no tmux server contacted). Start(false) marks the preset
// session started without launching anything.
func finishStart(t *testing.T, inst *session.Instance) {
	t.Helper()
	inst.SetTmuxSession(tmux.NewSessionWithDeps(inst.Title, inst.Program(), fakePtyFactory{t: t}, aliveCmdExecForTest()))
	require.NoError(t, inst.EnsureRunning())
	require.Equal(t, session.Running, inst.GetStatus(), "fixture: a start leaves the instance Running")
	require.True(t, core.ViewForTest(inst, 1).Active(), "fixture: a start leaves the instance active")
}

// selectTitle selects the instance titled title in m's focused list.
func selectTitle(t *testing.T, m *home, title string) *session.Instance {
	t.Helper()
	v := m.list.GetInstanceByTitle(title)
	require.NotNil(t, v)
	m.list.SelectID(v.ID)
	return testModel(m).InstanceForTest(v.ID)
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

	deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Err: errors.New("boom"), Owner: owner.ws()}))

	assert.Equal(t, idOf(m, victim), titleID(m.slots[1].list, "b1"), "the focused workspace's selected session must be untouched")
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
	finishStart(t, starting)
	m.switchWorkspaceSlot(1)
	m.errBox.SetSize(400, 1)

	pumpCore(t, m, deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Owner: owner.ws()})))

	assert.Equal(t, stateDefault, m.state, "no inline attach into another workspace's pane")
	assert.Equal(t, "bpeer", focusedName(m), "focus stays where the user put it")
	assert.GreaterOrEqual(t, recA.calls, 1, "the owner's storage is saved")
	assert.Contains(t, string(recA.lastData), "new-one")
	assert.Zero(t, recB.calls, "the focused workspace's storage is not touched")
	assert.Equal(t, idOf(m, starting), selID(owner.list), "selected in its own workspace")
	assert.Empty(t, starting.Prompt(), "the pending prompt belongs to the instance and is sent anyway")
	assert.Contains(t, m.errBox.String(), "new-one")
}

// TestInstanceStarted_SuccessInFocusedWorkspaceAttaches pins the ordinary
// path: the owner is focused, so the new session is selected and attached.
func TestInstanceStarted_SuccessInFocusedWorkspaceAttaches(t *testing.T) {
	m, recA, recB := ownerTestHome(t)
	starting := startingInstance(t, m.workspaceSlot, "new-one")
	finishStart(t, starting)

	deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Owner: m.ws()}))

	assert.Equal(t, stateInlineAttach, m.state)
	assert.Equal(t, idOf(m, starting), selID(m.list))
	assert.True(t, m.panes.Alive(starting.Pane().TmuxSessionName()), "its pane client is attached")
	assert.GreaterOrEqual(t, recA.calls, 1)
	assert.Zero(t, recB.calls)
}

// TestInstanceStarted_AnotherClientsStartOnlyAttachesItsPane: a start
// another client asked for (a server names no request to this one) gets its
// pane client here, but neither moves the selection nor attaches inline:
// the user here did not ask for it, and may be doing anything.
func TestInstanceStarted_AnotherClientsStartOnlyAttachesItsPane(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	before := selID(m.list)
	starting := startingInstance(t, m.workspaceSlot, "theirs")
	finishStart(t, starting)

	deliver(t, m, core.StartResult{Instance: starting, Owner: m.ws()})

	assert.Equal(t, stateDefault, m.state, "no inline attach")
	assert.Equal(t, before, selID(m.list), "the selection stays")
	assert.NotContains(t, m.errBox.String(), "theirs", "nor is it announced here")
	assert.True(t, m.panes.Alive(starting.Pane().TmuxSessionName()), "its pane client is attached")
}

// TestConfirmedDraft_ItsOwnStartAttachesInline runs the path production
// takes, with no request ID a test made up: the confirmed draft's Create
// carries this TUI's request, the start job the model spawns serves it,
// and Started names it back (core.Model.cause), so this TUI selects the
// new session and attaches inline. A start that lost its request would
// read as another client's and only attach the pane.
func TestConfirmedDraft_ItsOwnStartAttachesInline(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	repo := setupMergeRepo(t)
	d := m.newDraft("fresh", "", 0)
	d.path, d.program = repo, "claude"
	m.confirmDraft(d, overlay.LaunchOptions{})
	m.drainCore() // the Reply; the start's job is held
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	require.NotZero(t, sel.ID, "fixture: the session's row")
	inst := testModel(m).InstanceForTest(sel.ID)
	require.NotNil(t, inst)
	// A tmux session that comes alive when Start creates it, on no server.
	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			s := c.String()
			if strings.Contains(s, "has-session") && !created {
				return errors.New("session does not exist")
			}
			if strings.Contains(s, "new-session") {
				created = true
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	inst.SetTmuxSession(tmux.NewSessionWithDeps(inst.Title, inst.Program(), runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))

	pumpCore(t, m, nil) // the start, then Started

	require.Equal(t, session.Running, inst.GetStatus(), "fixture: the start succeeded")
	assert.Equal(t, stateInlineAttach, m.state, "its own start attaches inline")
	assert.Equal(t, sel.ID, selID(m.list))
}

// TestInstanceRecovered_AnotherClientsRecoverOnlyAttachesItsPane: the
// Recovered twin of the test above. A recovery another client asked for
// (its Recovered names no request of this TUI's) attaches the recovered
// session's pane, as every client shows it, but neither moves this TUI's
// selection nor announces it here.
func TestInstanceRecovered_AnotherClientsRecoverOnlyAttachesItsPane(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	m.errBox.SetSize(400, 1)
	placeholder, err := session.FromInstanceData(session.InstanceData{
		Title: "theirs", Status: session.Recoverable, Program: "claude",
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, placeholder.TransitionTo(session.Loading))
	m.ws().AddForTest(placeholder)
	m.syncViews()
	before := selID(m.list)
	recovered, err := session.NewInstance(session.InstanceOptions{Title: "theirs", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	finishStart(t, recovered)

	deliver(t, m, core.RecoverResult{OldTitle: "theirs", Recovered: recovered, Placeholder: placeholder, Owner: m.ws()})

	require.Contains(t, listIDs(m.list), idOf(m, recovered), "fixture: the recovery landed")
	assert.Equal(t, before, selID(m.list), "the selection stays")
	assert.NotContains(t, m.errBox.String(), "Recovered", "nor is it announced here")
	assert.True(t, m.panes.Alive(recovered.Pane().TmuxSessionName()), "its pane client is attached")
}

// TestInstanceStarted_InlineAttachWaitsForThePrompt: the N flow's prompt
// is pasted and Entered before the completion puts the user into inline
// attach, as when the completion sent it inline: a key typed into the
// attached pane in between would join the prompt.
func TestInstanceStarted_InlineAttachWaitsForThePrompt(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	starting := startingInstance(t, m.workspaceSlot, "new-one")
	starting.SetPrompt("do the thing")
	finishStart(t, starting)

	cmd := deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Owner: m.ws()}))
	assert.Equal(t, stateDefault, m.state, "no inline attach while the prompt is being sent")
	assert.Empty(t, starting.Prompt(), "the prompt is cleared at once, so nothing re-sends it")

	pumpCore(t, m, cmd)
	assert.Equal(t, stateInlineAttach, m.state, "attached once the prompt is sent")
	assert.Equal(t, idOf(m, starting), selID(m.list))
}

// TestInstanceStarted_KilledWhileThePromptIsSentIsNotAttached: Started
// waits for the initial prompt's send, and the instance is Running then,
// so the user can confirm a kill meanwhile. Inline attach on its Deleting
// row would leave them typing into the neighbouring session once the kill
// removes it (inline attach looks the selection up per key).
func TestInstanceStarted_KilledWhileThePromptIsSentIsNotAttached(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	m.errBox.SetSize(400, 1)
	starting := startingInstance(t, m.workspaceSlot, "new-one")
	starting.SetPrompt("do the thing")
	finishStart(t, starting)

	cmd := deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Owner: m.ws()}))
	require.NoError(t, starting.TransitionTo(session.Deleting)) // a kill confirmed meanwhile
	pumpCore(t, m, cmd)

	assert.Equal(t, stateDefault, m.state, "no inline attach on a session being killed")
	assert.NotEqual(t, ui.StateInlineAttach, m.menu.State(), "nor the menu")
	assert.Contains(t, m.errBox.String(), "new-one started")
}

// TestInstanceStarted_PausedWhileThePromptIsSentIsNotAttached: likewise
// a pause confirmed while the initial prompt is sent leaves the instance
// Loading (then Paused). Inline attach on it would forward keys to a
// session being torn down.
func TestInstanceStarted_PausedWhileThePromptIsSentIsNotAttached(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	m.errBox.SetSize(400, 1)
	starting := startingInstance(t, m.workspaceSlot, "new-one")
	starting.SetPrompt("do the thing")
	finishStart(t, starting)

	cmd := deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: starting, Owner: m.ws()}))
	require.NoError(t, starting.TransitionTo(session.Loading)) // a pause confirmed meanwhile
	pumpCore(t, m, cmd)

	assert.Equal(t, stateDefault, m.state, "no inline attach on a session being paused")
	assert.NotEqual(t, ui.StateInlineAttach, m.menu.State(), "nor the menu")
	assert.Contains(t, m.errBox.String(), "new-one started")
}

// TestInstanceStarted_AfterOwnersTabClosed: the owner's tab was closed
// while the start ran. The model still serves the workspace, so the start
// lands there and its storage is saved (the record isn't left at Loading),
// but nothing displays the instance: its completion attaches nothing and
// says where it started.
func TestInstanceStarted_AfterOwnersTabClosed(t *testing.T) {
	isolateTmux(t)
	m, recA, recB := ownerTestHome(t)
	owner := m.workspaceSlot
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
	require.Equal(t, []string{"bpeer"}, m.slotNames())
	recA.calls, recB.calls = 0, 0
	// The start finishes after the drop: live, with no client (a start
	// attaches none).
	started := liveInstance(t, "late")
	m.panes.Retain(nil) // nothing attached it
	owner.ws().AddForTest(started)
	m.syncViews()

	m.errBox.SetSize(400, 1)

	cmd := deliver(t, m, core.CausedForTest(1, core.StartResult{Instance: started, Owner: owner.ws()}))

	assert.Contains(t, m.errBox.String(), "late started in afocus", "its workspace is still served")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.list.GetInstanceByTitle("late"), "not filed under the focused workspace")
	assert.GreaterOrEqual(t, recA.calls, 1, "the owner's record is saved")
	assert.Contains(t, string(recA.lastData), "late")
	assert.Zero(t, recB.calls)
	drainCmd(cmd)
	assert.Nil(t, m.panes.Get(started.Pane().TmuxSessionName()), "nothing displays it, so nothing attaches it")
	assert.False(t, reaches(referencedViews(m), started))
	require.NoError(t, m.checkSlotInvariant())
}

// TestRecoverDone_AfterSwitchActsOnTheOwnerByIdentity: the recover
// completion used to act on the focused list by title — after a tab switch removing a
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
		slot.ws().AddForTest(p)
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

		placeholderID := idOf(m, placeholder) // captured while loaded: a removal forgets it
		deliver(t, m, core.CausedForTest(1, core.RecoverResult{OldTitle: "dup", Recovered: recovered, Placeholder: placeholder, Owner: owner.ws()}))

		assert.Equal(t, idOf(m, bystander), titleID(m.slots[1].list, "dup"), "the same-titled row elsewhere is untouched")
		assert.NotContains(t, listIDs(m.slots[1].list), idOf(m, recovered))
		assert.Contains(t, listIDs(owner.list), idOf(m, recovered), "filed under its own workspace")
		assert.NotContains(t, listIDs(owner.list), placeholderID, "the placeholder is replaced")
		assert.GreaterOrEqual(t, recA.calls, 1, "the owner's storage is saved")
		assert.Zero(t, recB.calls, "the focused workspace's storage is not touched")
	})

	t.Run("failure reverts the placeholder, not a namesake", func(t *testing.T) {
		m, _, _ := ownerTestHome(t)
		owner := m.workspaceSlot
		placeholder := newPlaceholder(t, owner)
		m.switchWorkspaceSlot(1)
		bystander := startingInstance(t, m.workspaceSlot, "dup")

		deliver(t, m, core.CausedForTest(1, core.RecoverResult{OldTitle: "dup", Err: errors.New("boom"), Placeholder: placeholder, Owner: owner.ws()}))

		assert.Equal(t, session.Recoverable, placeholder.GetStatus(), "the placeholder is back to Recoverable for a retry")
		assert.Equal(t, session.Loading, bystander.GetStatus(), "the namesake is untouched")
	})
}

// TestResumeDone_AfterOwnerDropped: the owner tab was closed while a
// resume ran; nothing displays the instance, so its completion attaches
// nothing.
func TestResumeDone_AfterOwnerDropped(t *testing.T) {
	isolateTmux(t)
	m, _, recB := ownerTestHome(t)
	owner := m.workspaceSlot
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
	recB.calls = 0
	resumed := liveInstance(t, "resumed")
	m.panes.Retain(nil) // nothing attached it
	owner.ws().AddForTest(resumed)
	m.syncViews()

	cmd := deliver(t, m, core.ResumeResult{Instance: resumed, Owner: owner.ws()})

	assert.Nil(t, m.list.GetInstanceByTitle("resumed"), "not filed under the focused workspace")
	assert.Zero(t, recB.calls)
	drainCmd(cmd)
	assert.Nil(t, m.panes.Get(resumed.Pane().TmuxSessionName()), "nothing displays it, so nothing attaches it")
}

// TestResumeFailed_RevertsAndLeavesNoClient: a resume whose checkpoint save
// failed comes back as core.OpFailed and is reverted to Paused, so
// the user can retry. A paused session keeps no client, whether or not its
// owner is still loaded.
func TestResumeFailed_RevertsAndLeavesNoClient(t *testing.T) {
	isolateTmux(t)
	failedResume := func(inst *session.Instance) core.OpFailed {
		return core.OpFailed{Instance: inst, Title: inst.Title, Op: "resume", Previous: session.Paused,
			Err: errors.New("resume checkpoint save: disk full")}
	}
	for _, ownerClosed := range []bool{true, false} {
		m, _, _ := ownerTestHome(t)
		owner := m.workspaceSlot
		if ownerClosed {
			drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
		}
		resumed := liveInstance(t, "resumed")
		owner.ws().AddForTest(resumed)
		m.syncViews()

		cmd := deliver(t, m, failedResume(resumed))
		drainCmd(cmd)

		assert.Equal(t, session.Paused, resumed.GetStatus(), "reverted, so the user can retry (owner closed: %v)", ownerClosed)
		assert.Nil(t, m.panes.Get(resumed.Pane().TmuxSessionName()), "a paused session keeps no client (owner closed: %v)", ownerClosed)
		assert.False(t, clientOf(t, resumed).PtmxAlive(), "and the one it had is closed (owner closed: %v)", ownerClosed)
	}
}
