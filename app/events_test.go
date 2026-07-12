package app

import (
	"testing"

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
	_ = m.list.AddInstance(inst)
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
