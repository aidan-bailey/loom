package app

import (
	tea "charm.land/bubbletea/v2"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/ui"
)

// The spinner's tick redraws the whole TUI twelve times a second, so it runs
// only while a card on screen shows it (spinnerDrawn): spinnerTick lets the
// chain lapse once none does, and startSpinner starts a new one from the
// first Update that sees one again.

// spinnerTick advances the spinner and re-arms its tick while a drawn card
// shows it; otherwise it lets the chain lapse.
func (m *home) spinnerTick(msg spinner.TickMsg) tea.Cmd {
	if !m.spinnerDrawn() {
		m.spinnerOn = false
		return nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// startSpinner starts the tick when a drawn card shows the spinner and no
// tick is in flight.
func (m *home) startSpinner() tea.Cmd {
	if m.spinnerOn || !m.spinnerDrawn() {
		return nil
	}
	m.spinnerOn = true
	return m.spinner.Tick
}

// spinnerDrawn reports whether the screen shows a card with the spinner:
// in overview, any Running or Loading row of the groups it draws (collapsed
// groups included, which only keeps the tick a little longer); in the focus
// layout, a row in the rail's visible window. The work, feedback and
// workbench views draw no cards.
func (m *home) spinnerDrawn() bool {
	switch m.layout() {
	case viewOverview:
		lists := []*ui.List{m.list}
		if len(m.slots) > 0 {
			lists = lists[:0]
			for _, s := range m.slots {
				lists = append(lists, s.list)
			}
		}
		for _, l := range lists {
			for _, v := range l.GetInstances() {
				if ui.ShowsSpinner(v.Status) {
					return true
				}
			}
		}
		return false
	case viewWork, viewFeedback, viewWorkbench:
		return false
	default:
		return !m.railHidden && m.list.DrawsSpinner()
	}
}
