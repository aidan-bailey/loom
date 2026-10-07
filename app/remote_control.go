package app

import (
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// remoteControlBlockedOn reports whether a launch of program on account
// acct should be interrupted to tell the user remote control can't work:
// the toggle is on (rcEnabled — either the global config or a
// per-instance override), the program is Claude, and acct's auth was
// clearly determined incompatible.
func (m *home) remoteControlBlockedOn(acct string, rcEnabled bool, program string) bool {
	return launch.RemoteControlBlocked(m.core.RCAuthFor(acct), rcEnabled, program)
}

// promptRemoteControlBlocked shows the "remote control unavailable" modal for
// a titled draft. Confirm (y) runs startWithoutRC — which launches the
// session with no --remote-control flag; cancel (n/esc) aborts creation,
// discarding the draft the way Esc does. Both branches route through
// pendingConfirmation so state_confirm.go dispatches it. reason is the
// launching account's auth reason.
func (m *home) promptRemoteControlBlocked(startWithoutRC overlay.ConfirmationTask, reason string) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = startWithoutRC

	msg := "Remote control unavailable: " + reason +
		"\n\nStart this session without remote control?"
	co := overlay.NewConfirmationOverlay(msg)
	co.SetWidth(60)
	co.OnCancel = func() {
		// Discard the draft, like the Esc path, and swap in the zero task
		// so cancel doesn't start it.
		m.menu.SetState(ui.StateDefault)
		m.discardDraft()
		m.pendingConfirmation = overlay.ConfirmationTask{}
	}
	m.setOverlay(co, overlayConfirmation)
	return nil
}

// promptRestartRemoteControlBlocked shows the "remote control
// unavailable" modal for the restart-with-options flow.
// resumeWithoutRC is the SAME task that would have run directly if
// auth weren't blocked — remoteControlProgram already omits
// --remote-control when auth isn't OK regardless of the enabled flag,
// so confirming here just proceeds with the composition the caller
// was always going to apply. Unlike promptRemoteControlBlocked
// (creation flow, which discards the draft on cancel), cancel here
// just returns to stateDefault — the
// already-existing Paused instance is untouched. handleStateConfirmKey
// unconditionally calls m.pendingConfirmation.Run() once the overlay
// reports closed=true (confirm AND cancel alike), so OnCancel must
// neutralize pendingConfirmation to a zero-value ConfirmationTask —
// otherwise cancel would still execute resumeWithoutRC's Sync/Async.
// reason is the launching account's auth reason.
func (m *home) promptRestartRemoteControlBlocked(resumeWithoutRC overlay.ConfirmationTask, reason string) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = resumeWithoutRC
	msg := "Remote control unavailable: " + reason + "\n\nResume this session without remote control?"
	co := overlay.NewConfirmationOverlay(msg)
	co.SetWidth(60)
	co.OnCancel = func() {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		m.pendingConfirmation = overlay.ConfirmationTask{}
	}
	m.setOverlay(co, overlayConfirmation)
	return nil
}
