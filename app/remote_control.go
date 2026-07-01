package app

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// remoteControlProgram returns program with Claude's --remote-control flag
// (named after title) applied when cfg enables it AND the detected auth can
// use it. It is a no-op when cfg is nil, the toggle is disabled, the auth is
// not confirmed OK (fail closed on Blocked/Unknown), or the program isn't
// Claude.
//
// Callers apply it to an instance's Program at first launch — once the title
// is known — so the rewritten command is persisted and later resume/crash
// restarts inherit the flag through BuildRecoveryCommand.
func remoteControlProgram(cfg *config.Config, auth session.RemoteControlAuth, program, title string) string {
	if cfg == nil || !cfg.RemoteControlEnabled() || !auth.OK() {
		return program
	}
	return session.BuildRemoteControlCommand(program, title)
}

// remoteControlBlocked reports whether a launch of program should be
// interrupted to tell the user remote control can't work: the toggle is on,
// the program is Claude, and auth was clearly determined incompatible.
func (m *home) remoteControlBlocked(program string) bool {
	return m.appConfig != nil &&
		m.appConfig.RemoteControlEnabled() &&
		session.IsClaudeProgram(program) &&
		m.rcAuth.Blocked()
}

// promptRemoteControlBlocked shows the "remote control unavailable" modal for
// a titled-but-unstarted instance. Confirm (y) runs startWithoutRC — which
// launches the session with no --remote-control flag; cancel (n/esc) aborts
// creation, popping and killing the pending instance the way Esc does. Both
// branches route their Cmd through pendingConfirmation so state_confirm.go
// dispatches it.
func (m *home) promptRemoteControlBlocked(startWithoutRC overlay.ConfirmationTask) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = startWithoutRC

	msg := "Remote control unavailable: " + m.rcAuth.Reason +
		"\n\nStart this session without remote control?"
	co := overlay.NewConfirmationOverlay(msg)
	co.SetWidth(60)
	co.OnCancel = func() {
		// Swap in an abort task so cancel tears the pending instance down
		// (async, like the Esc path) instead of starting it.
		popped := m.list.PopSelectedForKill()
		m.menu.SetState(ui.StateDefault)
		m.pendingConfirmation = overlay.ConfirmationTask{Async: backgroundKillCmd(popped)}
	}
	m.setOverlay(co, overlayConfirmation)
	return nil
}
