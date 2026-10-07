package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quickSend sends text to the selected session's agent through the quick
// input bar, as the user types it.
func quickSend(t *testing.T, m *home, text string) {
	t.Helper()
	_, _ = runQuickInputAgent(m)
	require.Equal(t, stateQuickInteract, m.state, "fixture: the bar opened")
	for _, r := range text {
		_, _ = handleStateQuickInteractKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, _ = handleStateQuickInteractKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestSendHold_RefusesInputUntilTheReply: while a prompt send to a session
// is in flight, keys typed into inline attach, or a second send, would
// land between its paste and its Enter (decision 12). Both are refused,
// with an info line, until the send's Reply, and allowed after it.
func TestSendHold_RefusesInputUntilTheReply(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	// Marked started, as a restored record whose session runs is: the
	// model sends only to a started session.
	require.NoError(t, inst.EnsureRunning())
	m.syncViews()
	id := idOf(m, inst)

	quickSend(t, m, "hi")
	require.Equal(t, stateDefault, m.state, "the first send closes the bar")
	require.True(t, m.sending[id], "the send is in flight")
	send := m.drainCore() // its job, not run yet

	_, _ = runInlineAttachAgent(m)
	assert.Equal(t, stateDefault, m.state, "inline attach is refused while the send lands")
	assert.Contains(t, m.errBox.String(), "still sending the last prompt to a")
	_, _ = runQuickInputAgent(m)
	assert.Equal(t, stateDefault, m.state, "and so is a second quick send")

	pumpCore(t, m, send) // the send lands; its Reply releases the hold
	assert.Empty(t, m.sending)
	assert.Empty(t, m.pending)
	_, _ = runQuickInputAgent(m)
	assert.Equal(t, stateQuickInteract, m.state, "a second send is allowed after the Reply")
	m.state = stateDefault
	_, _ = runInlineAttachAgent(m)
	assert.Equal(t, stateInlineAttach, m.state, "and so is inline attach")
}

// TestSendHold_ASubmitDuringTheSendKeepsTheBar: the bar's submit is held
// too, keeping its text, so nothing lands mid-send.
func TestSendHold_ASubmitDuringTheSendKeepsTheBar(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	_, _ = runQuickInputAgent(m)
	m.sending = map[core.InstanceID]bool{idOf(m, inst): true} // a send already in flight

	_, _ = handleStateQuickInteractKey(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, _ = handleStateQuickInteractKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateQuickInteract, m.state, "the bar stays open")
	require.NotNil(t, m.quickInputBar)
	assert.Equal(t, "x", m.quickInputBar.Value(), "with its text")
	assert.Empty(t, m.pending, "and nothing was sent")
}

// TestSendHold_ReleasedByARefusal: a send the model refuses (the session
// went: ErrNoSession) answers at once, and that Reply releases the hold
// too, and says so.
func TestSendHold_ReleasedByARefusal(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	gone := &core.InstanceView{ID: 999, Title: "gone"}

	m.sendPrompt(gone, "hello")
	require.True(t, m.sending[gone.ID])
	m.drainCore()

	assert.Empty(t, m.sending, "the refusal releases the hold")
	assert.Empty(t, m.pending)
	assert.Contains(t, m.errBox.String(), "prompt not sent to gone")
}

// promptRunning opens the prompt overlay on the selected running session
// (no creation flow) and types text into it.
func promptRunning(t *testing.T, m *home, text string) {
	t.Helper()
	m.state = statePrompt
	m.setOverlay(m.newPromptOverlay(), overlayTextInput)
	for _, r := range text {
		_, _ = handleStatePromptKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// submitPrompt presses the prompt overlay's Enter button.
func submitPrompt(m *home) {
	_, _ = handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	_, _ = handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestRefusal_ASendToASessionPausedMeanwhileIsShown: the user types into
// the prompt overlay; meanwhile the agent exits and the tick pauses the
// session. The model refuses the send (its precondition), which nothing
// else reports, so the TUI shows the refusal instead of dropping the
// prompt without a word.
func TestRefusal_ASendToASessionPausedMeanwhileIsShown(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	require.NoError(t, inst.EnsureRunning())
	m.syncViews()
	promptRunning(t, m, "hi")

	require.NoError(t, inst.TransitionTo(session.Paused)) // the tick paused it
	m.syncViews()
	submitPrompt(m)
	m.drainCore()

	assert.Contains(t, m.errBox.String(), "send a prompt to a: the session is paused")
	assert.Empty(t, m.sending, "and the refusal released the hold")
	assert.Empty(t, m.pending)
}

// TestRefusal_AConfirmedKillOfABusySessionIsShown: the session turned busy
// while the kill's confirmation was open, so the model refuses the kill,
// and the TUI says so.
func TestRefusal_AConfirmedKillOfABusySessionIsShown(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	_, _ = runKillSelected(m)
	require.Equal(t, stateConfirm, m.state)

	require.NoError(t, inst.TransitionTo(session.Loading)) // a resume started meanwhile
	m.syncViews()
	confirmKey(m)
	m.drainCore()

	assert.Contains(t, m.errBox.String(), "kill a: the session is busy (Loading)")
	assert.Equal(t, session.Loading, inst.GetStatus(), "the refused kill changed nothing")
	assert.Empty(t, m.pending)
}

// TestRefusal_AMergeWhoseSourceTurnedBusyIsShown: the source turned busy
// after the merge picker opened; the model's Merge refuses it, and the TUI
// says so rather than merging nothing silently.
func TestRefusal_AMergeWhoseSourceTurnedBusyIsShown(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	m.ws.AddForTest(target)
	m.ws.AddForTest(source)
	m.syncViews()
	selectIn(m, m.list, target)
	_, _ = runMergeSelected(m)
	require.Equal(t, stateMergePicker, m.state)

	require.NoError(t, source.TransitionTo(session.Loading)) // a resume started meanwhile
	m.syncViews()
	_, _ = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	results := requestResults(t, m)

	assert.Empty(t, results, "no merge job runs")
	assert.Contains(t, m.errBox.String(), "merge from source: the session is busy (Loading)")
	assert.Empty(t, m.pending)
}

// TestRefusal_AJobsFailureIsShownOnce: a request the model admitted whose
// job then fails is reported by the model itself (a Notice); its Reply
// carries the same error, which the TUI must not show a second time.
func TestRefusal_AJobsFailureIsShownOnce(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m) // never started: the kill's job fails (no worktree)
	_, _ = runKillSelectedNoConfirm(m)
	res, ok := requestJob(t, m)().(coreResultMsg)
	require.True(t, ok)
	testModel(m).Deliver(res.msg)

	notices, replies := 0, 0
	var replyCmd tea.Cmd
	for _, ev := range m.core.Sync().Events {
		switch ev.(type) {
		case core.Notice:
			notices++
		case core.Reply:
			replies++
			replyCmd = m.applyCoreEvent(ev)
		}
	}
	assert.Equal(t, 1, notices, "the model reports the job's failure")
	assert.Equal(t, 1, replies)
	assert.Nil(t, replyCmd, "and the TUI shows no second copy of it")
	assert.Empty(t, m.pending)
}

// TestSendHold_ThePromptOverlayKeepsItsText: a submit while the last send
// to the session is still landing is held like the quick input bar's: the
// overlay stays open with its text, and the info line says why.
func TestSendHold_ThePromptOverlayKeepsItsText(t *testing.T) {
	m := homeWithAppState(t)
	m.errBox.SetSize(400, 1)
	inst := addReadyInstance(t, m)
	promptRunning(t, m, "hi")
	m.sending = map[core.InstanceID]bool{idOf(m, inst): true} // a send already in flight

	submitPrompt(m)

	assert.Equal(t, statePrompt, m.state, "the overlay stays open")
	ti := m.textInput()
	require.NotNil(t, ti)
	assert.Equal(t, "hi", ti.GetValue(), "with its text")
	assert.Empty(t, m.pending, "and nothing was sent")
	assert.Contains(t, m.errBox.String(), "still sending the last prompt to a")
}
