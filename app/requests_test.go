package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"

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
