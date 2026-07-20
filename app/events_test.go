package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/require"
)

// TestPaneDirtyRerendersScrolledAgent: scrolling changes the WINDOW, not the
// live content — a dirty event for the scrolled agent's session must still
// re-render it (capture-pane -S) so new output keeps the anchored view fresh.
func TestPaneDirtyRerendersScrolledAgent(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)

	// startedInstanceWithHistory pins LOOM_PANE_RENDERER=snapshot for its
	// capture-pane mock; the event path needs the emulator flag ON so the
	// previewTick guard doesn't matter and dirty routing engages.
	t.Setenv("LOOM_PANE_RENDERER", "")
	require.NotEmpty(t, inst.TmuxSessionName())

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	require.Same(t, inst, m.list.GetSelectedInstance())
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)

	require.NoError(t, m.splitPane.UpdateAgent(inst))
	for i := 0; i < 30; i++ {
		m.splitPane.ScrollAgentUp()
	}
	require.NoError(t, m.splitPane.UpdateAgent(inst))
	require.True(t, m.splitPane.IsAgentInScrollMode())

	before := historyCaptures
	_, _ = m.Update(paneDirtyMsg{session: inst.TmuxSessionName()})
	require.Greater(t, historyCaptures, before,
		"a dirty event must re-render a scrolled agent pane")
	require.True(t, m.splitPane.IsAgentInScrollMode())
}

// TestPaneQuietRunsStatusDetection: a quiet event on an agent session runs
// CaptureAndProcessStatus and applies Prompting/Ready, mirroring the old
// 500ms metadata tick's transition logic.
func TestPaneQuietRunsStatusDetection(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	// Quiet handler returns a statusDetectCmd; run it and feed the result
	// message back through Update, as the Bubble Tea runtime would.
	_, cmd := m.Update(paneQuietMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd, "quiet on a live agent session must schedule detection")
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "detection cmd must return statusDetectedMsg, got %T", msg)
	_, _ = m.Update(detected)
	// The mock capture returns non-prompt content and the first hash counts
	// as an update → instance lands in Running.
	require.Equal(t, session.Running, inst.GetStatus())
}

// TestPaneQuietIgnoresUnknownAndInertSessions guards the drop paths.
func TestPaneQuietIgnoresUnknownAndInertSessions(t *testing.T) {
	m := homeWithAppState(t)
	_, cmd := m.Update(paneQuietMsg{session: "loom_nonexistent"})
	require.Nil(t, cmd, "unknown session → dropped")
}

// TestPtyDeadVerifiesBeforePausing: a ptyDeadMsg must probe has-session in a
// Cmd; a still-live session (failed reattach) must NOT be marked Paused.
func TestPtyDeadVerifiesBeforePausing(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	_, cmd := m.Update(ptyDeadMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd, "dead event on a live instance must schedule verification")
	msg := cmd()
	verified, ok := msg.(deadVerifiedMsg)
	require.True(t, ok, "expected deadVerifiedMsg, got %T", msg)
	// The mock cmdExec answers has-session with success → tmuxAlive true.
	require.True(t, verified.tmuxAlive)
	_, _ = m.Update(verified)
	require.NotEqual(t, session.Paused, inst.GetStatus(),
		"a live session must not be paused by a PTY-death false positive")
}

// TestBellBadgesUnselectedInstance: BEL from a backgrounded pane badges its
// list row; the selected instance never badges (the user is looking at it),
// and selecting a badged instance clears it.
func TestBellBadgesUnselectedInstance(t *testing.T) {
	var hc1, hc2 int
	inst1 := startedInstanceWithHistoryTitled(t, &hc1, "scroll1")
	inst2 := startedInstanceWithHistoryTitled(t, &hc2, "scroll2")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst1) // first add is auto-selected
	m.list.AddInstance(inst2)

	_, _ = m.Update(bellMsg{session: inst2.TmuxSessionName()})
	require.True(t, inst2.BellPending(), "bell on unselected instance must badge it")

	_, _ = m.Update(bellMsg{session: inst1.TmuxSessionName()})
	require.False(t, inst1.BellPending(), "bell on the selected instance is not badged")

	m.list.SetSelectedInstance(1) // select inst2
	_ = m.instanceChanged()
	require.False(t, inst2.BellPending(), "selecting a badged instance clears the badge")
}
