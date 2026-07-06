package app

import (
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// handleStateLaunchOptionsKey runs while the Session Launch Options
// modal is active — shown after title (and, for the N flow, prompt)
// entry, right before a new instance actually starts. Confirming
// (enter) hands the chosen overlay.LaunchOptions to whichever closure
// state_new.go/state_prompt.go stashed in m.pendingLaunchOptions before
// opening this modal; canceling (esc/ctrl+c) pops and kills the
// pending, not-yet-started instance, mirroring handleStateNewKey's own
// cancel path.
func handleStateLaunchOptionsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.cancelLaunchOptions()
	}

	lo := m.launchOptionsOverlay()
	if lo == nil {
		return m, nil
	}

	closed, confirmed := lo.HandleKeyPress(msg)
	if !closed {
		return m, nil
	}

	if !confirmed {
		return m.cancelLaunchOptions()
	}

	opts := lo.Options()
	pending := m.pendingLaunchOptions
	m.pendingLaunchOptions = nil
	m.dismissOverlay()
	if pending == nil {
		m.state = stateDefault
		return m, nil
	}
	return pending(opts)
}

// cancelLaunchOptions dismisses the Session Launch Options modal
// without confirming and runs whatever pendingLaunchOptionsCancel the
// opening flow stashed — pop-and-kill the pending instance for
// creation (killPendingLaunchOptionsCancel), or a no-op dismiss for
// restart (runRestartWithOptionsSelected).
func (m *home) cancelLaunchOptions() (tea.Model, tea.Cmd) {
	cancel := m.pendingLaunchOptionsCancel
	m.pendingLaunchOptions = nil
	m.pendingLaunchOptionsCancel = nil
	m.dismissOverlay()
	if cancel == nil {
		m.state = stateDefault
		return m, nil
	}
	return cancel()
}

// killPendingLaunchOptionsCancel is the creation flow's
// pendingLaunchOptionsCancel: pop and kill the pending, not-yet-started
// instance and return to stateDefault — the same shape as
// handleStateNewKey's Esc/ctrl+c handling.
func (m *home) killPendingLaunchOptionsCancel() (tea.Model, tea.Cmd) {
	popped := m.list.PopSelectedForKill()
	m.state = stateDefault
	m.instanceChanged()
	return m, tea.Batch(
		tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		),
		backgroundKillCmd(popped),
	)
}
