package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drawsHome is fleetHome in focus mode with two live rows: "f-live" in the
// focused slot (not selected) and "b-live" in the peer slot, both Running
// so output promotes nothing, and a current terminal session, injected.
func drawsHome(t *testing.T) (m *home, focusRow, peerRow, terminal string) {
	t.Helper()
	isolateTmux(t)
	m = fleetHome(t)
	m.viewMode = viewFocus
	f := liveInstance(t, "f-live")
	b := liveInstance(t, "b-live")
	m.slots[0].ws().AddForTest(f)
	m.slots[1].ws().AddForTest(b)
	m.syncViews()
	m.drainCore()
	ts := tmux.NewTmuxSessionWithDeps("t-cur", "bash", fakePtyFactory{t: t}, aliveCmdExecForTest())
	m.splitPane.InjectTerminalSessionForTest("t-cur", ts, t.TempDir())
	// A new row takes the selection; put it back on a fleet row, so
	// f-live is a rail row and not the selected agent.
	for _, row := range m.list.GetInstances() {
		if row.Title == "f1" {
			m.list.SelectID(row.ID)
		}
	}
	require.NotEqual(t, f.Title, m.list.GetSelectedInstance().Title, "fixture: f-live is not the selection")
	// Settle: the first pane event after the rows were added flushes their
	// views (ViewsChanged) once; later events publish nothing, as in a
	// running TUI.
	for _, inst := range []*session.Instance{f, b} {
		m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	}
	return m, f.Pane().TmuxSessionName(), b.Pane().TmuxSessionName(), ts.SessionName()
}

func (m *home) drawsSession(name string) bool {
	v, _ := m.viewBySession(name)
	return m.drawsPane(name, v)
}

func TestDrawsPane_ByLayout(t *testing.T) {
	m, focusRow, peerRow, term := drawsHome(t)
	// The selection is a fleet row with no tmux session of its own, so it
	// is asked by its row, as the case asks it.
	selectedDrawn := func() bool {
		sel := m.list.GetSelectedInstance()
		return m.drawsPane(sel.TmuxSession, sel)
	}

	// Focus: the selection, the current terminal, the focused slot's rows
	// while the rail shows; never a peer slot's row or another terminal.
	assert.True(t, selectedDrawn(), "focus: the selected agent")
	assert.True(t, m.drawsSession(term), "focus: the current terminal")
	assert.True(t, m.drawsSession(focusRow), "focus: a rail row")
	assert.False(t, m.drawsSession(peerRow), "focus: another tab's row")
	assert.False(t, m.drawsSession("loom_t-cached"), "focus: a terminal that is not current")
	m.railHidden = true
	assert.False(t, m.drawsSession(focusRow), "focus, rail hidden: a rail row")
	assert.True(t, selectedDrawn(), "focus, rail hidden: the selected agent")
	m.railHidden = false

	m.viewMode = viewOverview
	assert.True(t, m.drawsSession(peerRow), "overview: every open workspace's row")
	assert.False(t, m.drawsSession(term), "overview: no terminal pane")

	m.viewMode = viewWorkbench
	assert.True(t, selectedDrawn(), "workbench: the selected agent")
	assert.True(t, m.drawsSession(term), "workbench: its terminal tab")
	assert.False(t, m.drawsSession(focusRow), "workbench: no rail")

	m.viewMode = viewWork
	assert.False(t, selectedDrawn(), "work: no pane")
	assert.False(t, m.drawsSession(term), "work: no pane")

	m.viewMode = viewFeedback
	assert.False(t, selectedDrawn(), "feedback: no pane")
	assert.False(t, m.drawsSession(term), "feedback: no pane")

	// The file explorer keeps the focus layout over overview and workbench.
	m.viewMode = viewOverview
	m.state = stateFileExplorer
	assert.True(t, m.drawsSession(focusRow), "file explorer over overview: the rail shows")
	assert.False(t, m.drawsSession(peerRow), "file explorer over overview: focus rules")
}

// stubFrame records a frame through the gate, as View does, without
// drawing the fleet fixture (it has no chrome to draw).
func stubFrame(m *home) {
	m.gate.view(func() tea.View { return tea.NewView("frame") })
}

// rendered leaves m a frame past a render, so drawn output renders at
// once rather than arming a tick.
func rendered(t *testing.T, m *home) *time.Time {
	t.Helper()
	now := time.Unix(1000, 0)
	m.gate.now = func() time.Time { return now }
	stubFrame(m)
	now = now.Add(frameInterval)
	return &now
}

func TestPaneOutput_UndrawnOutputReusesTheFrame(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: peerRow})
	assert.True(t, m.gate.reuse, "another tab's output asks for no frame")
	m.Update(paneDirtyMsg{session: "loom_t-cached"})
	assert.True(t, m.gate.reuse, "a cached terminal's output asks for no frame")
}

func TestPaneOutput_DrawnOutputRenders(t *testing.T) {
	m, focusRow, _, term := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: focusRow})
	assert.False(t, m.gate.reuse, "a rail row's output renders")
	m.Update(paneDirtyMsg{session: term})
	assert.False(t, m.gate.reuse, "the current terminal's output renders")
}

func TestPaneOutput_PromotionRenders(t *testing.T) {
	m, inst := peerLiveHome(t, session.Ready) // a peer slot's row, not drawn in focus mode
	// Settle as drawsHome does, without the pane event that would promote:
	// the first PaneOutput flushes the rows' views once.
	m.core.PaneOutput(idOf(m, inst))
	m.drainCore()
	rendered(t, m)
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.Equal(t, session.Running, m.ladder[idOf(m, inst)].status, "fixture: the output promoted the row")
	assert.False(t, m.gate.reuse, "a promotion changes the tab bar and the peer footer")
}

func TestUpdate_OtherMessagesRender(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: peerRow})
	require.True(t, m.gate.reuse, "fixture")
	m.Update(spinner.TickMsg{})
	assert.False(t, m.gate.reuse, "a message that is not pane output renders")
}

func TestUpdate_ADrainThatAppliedEventsRenders(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.slots[1].ws().AddForTest(liveInstance(t, "b-new")) // the model publishes; the drain applies it
	m.Update(paneDirtyMsg{session: peerRow})
	assert.False(t, m.gate.reuse, "a model change that rode a pane message renders")
}

func TestUpdate_OfflineRenders(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.link.state = linkReconnecting
	m.Update(paneDirtyMsg{session: peerRow})
	assert.False(t, m.gate.reuse, "offline, every Update renders")
}

func TestUpdate_TheFrameTickRendersAndClearsTheTick(t *testing.T) {
	m, focusRow, _, _ := drawsHome(t)
	now := rendered(t, m)
	stubFrame(m)
	*now = now.Add(time.Millisecond)
	m.Update(paneDirtyMsg{session: focusRow})
	require.True(t, m.gate.pending, "fixture: within a frame, a tick is armed")
	require.True(t, m.gate.reuse)
	m.Update(frameTickMsg{})
	assert.False(t, m.gate.pending)
	assert.False(t, m.gate.reuse)
}

var _ = ui.FocusAgent // ui is imported for the fixture's types
