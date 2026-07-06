package app

import (
	"strings"

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

// effortProgram returns program with Claude's --effort flag applied.
// No-op when the program isn't Claude or effort is "" / "default".
func effortProgram(effort, program string) string {
	return session.BuildEffortCommand(program, effort)
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
		HeadroomProxy:  cfg.HeadroomProxyEnabled(),
		Effort:         cfg.Effort(),
		CacheTTL1h:     cfg.CacheTTL1hEnabled(),
	}
}

// effectiveRemoteControl reports whether opts should actually apply
// the --remote-control flag once Headroom Proxy's exclusivity is
// accounted for. applyLaunchOptions and every remoteControlBlocked
// call site must agree on this value — otherwise a config.json
// hand-edited to set both ClaudeRemoteControl and HeadroomProxy true
// (or a Session Launch Options selection reaching that state) would
// make remoteControlBlocked report a conflict the composed command
// doesn't actually have.
func effectiveRemoteControl(opts overlay.LaunchOptions) bool {
	return opts.RemoteControl && !opts.HeadroomProxy
}

// applyLaunchOptions composes program in order: remote-control,
// permission-mode, model, then effort. Headroom Proxy is intentionally
// absent from composition — it never touches program (see
// session.HeadroomProxyEnv, applied separately to the tmux session's
// environment via Instance.HeadroomProxy) — but it still affects
// composition indirectly: remoteControlProgram receives
// effectiveRemoteControl(opts), not raw opts.RemoteControl, so Headroom
// Proxy being on still forces --remote-control off. This is the
// authoritative enforcement of the RC/HeadroomProxy exclusivity rule —
// the UI-level auto-disable (Claude Preferences, Session Launch
// Options) is the good-UX layer on top, not the only guarantee.
func applyLaunchOptions(opts overlay.LaunchOptions, auth session.RemoteControlAuth, program, title string) string {
	program = remoteControlProgram(effectiveRemoteControl(opts), auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(opts.Model, program)
	program = effortProgram(opts.Effort, program)
	return program
}

// ParseLaunchOptions decodes a composed Program string back into the
// overlay.LaunchOptions that produced it, plus the underlying bare
// program (binary path/name and any *other* flags) applyLaunchOptions
// would need to recompose it from scratch. It is the symmetric decode
// of applyLaunchOptions: scans tokens for --remote-control[=name],
// --permission-mode <mode>, --model <model>, and --effort <level>,
// removing each recognized flag (and its value token, where applicable)
// from the returned base program. Recomposing must start from a bare
// program — applyLaunchOptions's ApplyXFlag functions insert "right
// after parts[0]", so calling them again on an already-flagged string
// would duplicate an existing --permission-mode. A token this doesn't
// recognize (e.g. a hand-added flag) is left in place in baseProgram
// and simply doesn't set the corresponding opts field — never an
// error.
//
// opts.HeadroomProxy and opts.CacheTTL1h are left at their zero value
// (false) — unlike the other four options, neither is ever baked into
// Program (see session.HeadroomProxyEnv/CacheTTL1hEnv); callers must
// seed/apply them from Instance.HeadroomProxy/CacheTTL1h directly
// instead.
func ParseLaunchOptions(program string) (opts overlay.LaunchOptions, baseProgram string) {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return opts, ""
	}

	// applyLaunchOptions's Build*Command helpers no-op (add no flag) for
	// "default", so an absent flag means "default" once we know there's
	// an actual program present — not the zero value, which the
	// len(parts)==0 case above already returned.
	opts.PermissionMode = "default"
	opts.Model = "default"
	opts.Effort = "default"

	kept := []string{parts[0]}
	for i := 1; i < len(parts); i++ {
		switch {
		case parts[i] == "--remote-control":
			opts.RemoteControl = true
			// May be followed by a session-name value token, or may
			// stand alone (Claude auto-generates a name). Only
			// consume the next token if it doesn't look like another
			// flag.
			if i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "--") {
				i++
			}
		case strings.HasPrefix(parts[i], "--remote-control="):
			opts.RemoteControl = true
		case parts[i] == "--permission-mode" && i+1 < len(parts):
			opts.PermissionMode = parts[i+1]
			i++
		case parts[i] == "--model" && i+1 < len(parts):
			opts.Model = parts[i+1]
			i++
		case parts[i] == "--effort" && i+1 < len(parts):
			opts.Effort = parts[i+1]
			i++
		default:
			kept = append(kept, parts[i])
		}
	}
	return opts, strings.Join(kept, " ")
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

// promptRestartRemoteControlBlocked shows the "remote control
// unavailable" modal for the restart-with-options flow.
// resumeWithoutRC is the SAME task that would have run directly if
// auth weren't blocked — remoteControlProgram already omits
// --remote-control when auth isn't OK regardless of the enabled flag,
// so confirming here just proceeds with the composition the caller
// was always going to apply. Unlike promptRemoteControlBlocked
// (creation flow, which pops/kills the pending not-yet-started
// instance on cancel), cancel here just returns to stateDefault — the
// already-existing Paused instance is untouched. handleStateConfirmKey
// unconditionally calls m.pendingConfirmation.Run() once the overlay
// reports closed=true (confirm AND cancel alike), so OnCancel must
// neutralize pendingConfirmation to a zero-value ConfirmationTask —
// otherwise cancel would still execute resumeWithoutRC's Sync/Async.
func (m *home) promptRestartRemoteControlBlocked(resumeWithoutRC overlay.ConfirmationTask) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = resumeWithoutRC
	msg := "Remote control unavailable: " + m.rcAuth.Reason + "\n\nResume this session without remote control?"
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
