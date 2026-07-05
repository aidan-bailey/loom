package app

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// remoteControlProgram returns program with Claude's --remote-control flag
// (named after title) applied when enabled AND the detected auth can use
// it. It is a no-op when enabled is false, the auth is not confirmed OK
// (fail closed on Blocked/Unknown), or the program isn't Claude.
//
// Callers apply it to an instance's Program at first launch — once the
// title is known — so the rewritten command is persisted and later
// resume/crash restarts inherit the flag through BuildRecoveryCommand.
func remoteControlProgram(enabled bool, auth session.RemoteControlAuth, program, title string) string {
	if !enabled || !auth.OK() {
		return program
	}
	return session.BuildRemoteControlCommand(program, title)
}

// permissionModeProgram returns program with Claude's --permission-mode
// flag applied. No-op when the program isn't Claude
// (BuildPermissionModeCommand's registry lookup already no-ops for
// non-Claude adapters) or mode is "" / "default".
func permissionModeProgram(mode, program string) string {
	return session.BuildPermissionModeCommand(program, mode)
}

// modelProgram returns program with Claude's --model flag applied.
// No-op when the program isn't Claude or model is "" / "default".
func modelProgram(model, program string) string {
	return session.BuildModelCommand(program, model)
}

// headroomWrapProgram returns program wrapped as "headroom wrap
// <program>" when enabled. Agent-agnostic: applies regardless of which
// program is configured.
func headroomWrapProgram(enabled bool, program string) string {
	if !enabled {
		return program
	}
	return session.BuildHeadroomWrapCommand(program)
}

// launchOptionsFromConfig snapshots cfg's current global launch-option
// values into an overlay.LaunchOptions, the same shape edited by the
// Session Launch Options modal. Returns the zero value (all
// disabled/default) for a nil cfg — matching the effect the old
// cfg-nil guards in remoteControlProgram/permissionModeProgram had
// before this refactor.
func launchOptionsFromConfig(cfg *config.Config) overlay.LaunchOptions {
	if cfg == nil {
		return overlay.LaunchOptions{}
	}
	return overlay.LaunchOptions{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		HeadroomWrap:   cfg.HeadroomWrapEnabled(),
	}
}

// applyLaunchOptions composes program in order: remote-control,
// permission-mode, model, then headroom-wrap last — headroom-wrap must
// be outermost so the earlier three steps still see the bare agent
// name at parts[0] when deciding how to modify the string. HeadroomWrap
// forcibly disables RemoteControl here regardless of opts.RemoteControl:
// this is the authoritative enforcement of the exclusivity rule (the
// UI-level auto-disable in ClaudePreferences/SessionLaunchOptions is
// the good-UX layer on top, not the only guarantee — a hand-edited
// config.json with both fields true still can't launch both flags
// together).
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	if opts.HeadroomWrap {
		opts.RemoteControl = false
	}
	program = remoteControlProgram(opts.RemoteControl, auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	program = headroomWrapProgram(opts.HeadroomWrap, program)
	return program
}

// remoteControlBlocked reports whether a launch of program should be
// interrupted to tell the user remote control can't work: the toggle is
// on (rcEnabled — either the global config or a per-instance override),
// the program is Claude, and auth was clearly determined incompatible.
func (m *home) remoteControlBlocked(rcEnabled bool, program string) bool {
	return rcEnabled && session.IsClaudeProgram(program) && m.rcAuth.Blocked()
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
