package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
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
	require.NotEmpty(t, inst.Pane().TmuxSessionName())

	m := homeWithAppState(t)
	m.ws.Add(inst)
	m.syncViews()
	require.Equal(t, idOf(m, inst), selID(m.list))
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(rowOf(t, m, inst))

	require.NoError(t, m.splitPane.UpdateAgent(rowOf(t, m, inst)))
	for i := 0; i < 30; i++ {
		m.splitPane.ScrollAgentUp()
	}
	require.NoError(t, m.splitPane.UpdateAgent(rowOf(t, m, inst)))
	require.True(t, m.splitPane.IsAgentInScrollMode())

	before := historyCaptures
	_, _ = m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
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
	m.ws.Add(inst)
	m.syncViews()

	// Quiet handler returns a statusDetectCmd; run it and feed the result
	// message back through Update, as the Bubble Tea runtime would.
	_, cmd := m.Update(paneQuietMsg{session: inst.Pane().TmuxSessionName()})
	require.NotNil(t, cmd, "quiet on a live agent session must schedule detection")
	detected := detectionFrom(t, cmd)
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
	m.ws.Add(inst)
	m.syncViews()

	_, cmd := m.Update(ptyDeadMsg{session: inst.Pane().TmuxSessionName()})
	require.NotNil(t, cmd, "dead event on a live instance must schedule verification")
	msg := cmd()
	result, ok := msg.(coreResultMsg)
	require.True(t, ok, "expected the model's probe, got %T", msg)
	verified, ok := result.msg.(core.DeadVerified)
	require.True(t, ok, "expected core.DeadVerified, got %T", result.msg)
	// The mock cmdExec answers has-session with success → tmuxAlive true.
	require.Equal(t, tmux.LivenessAlive, verified.TmuxLive)
	_, _ = m.Update(result)
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
	m.ws.Add(inst1) // first add is auto-selected
	m.ws.Add(inst2)
	m.syncViews()

	_, _ = m.Update(bellMsg{session: inst2.Pane().TmuxSessionName()})
	require.True(t, bell(m, inst2), "bell on unselected instance must badge it")

	_, _ = m.Update(bellMsg{session: inst1.Pane().TmuxSessionName()})
	require.False(t, bell(m, inst1), "bell on the selected instance is not badged")

	m.list.SetSelectedInstance(1) // select inst2
	_ = m.instanceChanged()
	require.False(t, bell(m, inst2), "selecting a badged instance clears the badge")
}

// TestStatusDetection_NoClientGivesNoOpinion: a pane with no client has
// no screen to scan. Its scan used to come back as "unchanged, no prompt",
// which the ladder reads as settled, so a working agent whose client had
// been released went Ready and its re-detection chain ended there. Now
// nothing is scanned and nothing moves.
func TestStatusDetection_NoClientGivesNoOpinion(t *testing.T) {
	isolateTmux(t)
	m := newTestHome(t)
	inst := liveInstance(t, "working")
	m.ws.Add(inst)
	m.syncViews()
	name := inst.Pane().TmuxSessionName()
	m.panes.Retain(nil) // its client was released
	require.Nil(t, m.panes.For(rowOf(t, m, inst)).Client())

	_, cmd := m.Update(redetectMsg{session: name})
	if cmd != nil {
		if detected, ok := cmd().(statusDetectedMsg); ok {
			_, cmd = m.Update(detected)
		}
	}

	require.Equal(t, session.Running, inst.GetStatus(), "no screen, no opinion: the status stays")
	require.Nil(t, cmd, "and the re-detection chain ends")
	require.False(t, m.redetectPending[name])
}
