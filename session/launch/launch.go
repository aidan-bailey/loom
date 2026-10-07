// Package launch is a Claude session's launch options and their
// composition into (and decoding from) the agent command line: remote
// control, permission mode, model, effort, plus the options that never
// reach the command line (Headroom proxy, the 1h cache TTL, the branch
// prefix, the account). The TUI's Session Launch Options modal edits an
// Options value; the model composes one from config for the workspace
// terminal it creates.
package launch

import (
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// Options holds the per-session launch overrides. Defined
// here (rather than in app or ui/overlay) so it's usable both by
// overlay.SessionLaunchOptions (ephemeral, edited as a plain value) and by
// the launch-command composition below, without an import cycle back to
// app or a UI import in core.
type Options struct {
	RemoteControl  bool
	PermissionMode string
	Model          string
	// Context1M requests Claude's [1m] long-context suffix on Model.
	// Applied only when Model accepts it (see
	// config.ClaudeModelSupports1M); otherwise silently ignored.
	Context1M     bool
	HeadroomProxy bool
	Effort        string
	CacheTTL1h    bool
	// BranchPrefix overrides config.BranchPrefix for this one session.
	// Unlike the fields above it never reaches the agent command line —
	// it is consumed by git worktree setup (see session.Instance.
	// SetBranchPrefix), so Parse cannot recover it and the
	// restart path seeds it from the instance instead.
	BranchPrefix string
	// Account is the Claude account to launch under (account.DefaultName
	// for the default). Like BranchPrefix it never reaches the command
	// line: app records it on the instance (session.Instance.SetAccount),
	// and a launch turns it into CLAUDE_CONFIG_DIR.
	Account string
}

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

// FromConfig snapshots cfg's current global launch-option
// values into an Options, the same shape edited by the
// Session Launch Options modal. Returns the zero value (all
// disabled/default) for a nil cfg — matching the effect the old
// cfg-nil guards in remoteControlProgram/permissionModeProgram had
// before this refactor.
func FromConfig(cfg *config.Config) Options {
	if cfg == nil {
		return Options{}
	}
	return Options{
		RemoteControl:  cfg.RemoteControlEnabled(),
		PermissionMode: cfg.PermissionMode(),
		Model:          cfg.Model(),
		Context1M:      cfg.Context1MEnabled(),
		HeadroomProxy:  cfg.HeadroomProxyEnabled(),
		Effort:         cfg.Effort(),
		CacheTTL1h:     cfg.CacheTTL1hEnabled(),
		BranchPrefix:   cfg.GetBranchPrefix(),
	}
}

// EffectiveRemoteControl reports whether opts should actually apply
// the --remote-control flag once Headroom Proxy's exclusivity is
// accounted for. Compose and every RemoteControlBlocked
// call site must agree on this value — otherwise a config.json
// hand-edited to set both ClaudeRemoteControl and HeadroomProxy true
// (or a Session Launch Options selection reaching that state) would
// make RemoteControlBlocked report a conflict the composed command
// doesn't actually have.
func EffectiveRemoteControl(opts Options) bool {
	return opts.RemoteControl && !opts.HeadroomProxy
}

// effectiveModel returns the --model value to launch with, appending
// Claude's [1m] long-context suffix when the option is on and the
// selected alias accepts it.
//
// An alias that doesn't support the suffix (default, haiku, or anything
// this build doesn't recognize) is returned unchanged rather than
// producing a value Claude would reject. That makes the toggle a silent
// no-op for those models, which is deliberate: the alternative failure
// is a pane that dies at launch. One consequence is documented in the
// design doc — {haiku, Context1M: true} composes to a bare "haiku", so
// re-decoding that Program yields Context1M false and the checkbox
// reverts on resume. The state was never meaningful, and the global
// config default is unaffected.
func effectiveModel(opts Options) string {
	if opts.Context1M && config.ClaudeModelSupports1M(opts.Model) {
		return opts.Model + "[1m]"
	}
	return opts.Model
}

// Compose composes program in order: remote-control,
// permission-mode, model, then effort. Headroom Proxy is intentionally
// absent from composition — it never touches program (see
// session.HeadroomProxyEnv, applied separately to the tmux session's
// environment via Instance.HeadroomProxy) — but it still affects
// composition indirectly: remoteControlProgram receives
// EffectiveRemoteControl(opts), not raw opts.RemoteControl, so Headroom
// Proxy being on still forces --remote-control off. This is the
// authoritative enforcement of the RC/HeadroomProxy exclusivity rule —
// the UI-level auto-disable (Claude Preferences, Session Launch
// Options) is the good-UX layer on top, not the only guarantee.
func Compose(opts Options, auth session.RemoteControlAuth, program, title string) string {
	program = remoteControlProgram(EffectiveRemoteControl(opts), auth, program, title)
	program = permissionModeProgram(opts.PermissionMode, program)
	program = modelProgram(effectiveModel(opts), program)
	program = effortProgram(opts.Effort, program)
	return program
}

// Parse decodes a composed Program string back into the
// Options that produced it, plus the underlying bare
// program (binary path/name and any *other* flags) Compose
// would need to recompose it from scratch. It is the symmetric decode
// of Compose: scans tokens for --remote-control[=name],
// --permission-mode <mode>, --model <model>, and --effort <level>,
// removing each recognized flag (and its value token, where applicable)
// from the returned base program. Recomposing must start from a bare
// program — Compose's ApplyXFlag functions insert "right
// after parts[0]", so calling them again on an already-flagged string
// would duplicate an existing --permission-mode. A token this doesn't
// recognize (e.g. a hand-added flag) is left in place in baseProgram
// and simply doesn't set the corresponding opts field — never an
// error.
//
// opts.HeadroomProxy and opts.CacheTTL1h are left at their zero value
// (false) — unlike the other four options, neither is ever baked into
// Program (see session.HeadroomProxyEnv/CacheTTL1hEnv); callers must
// seed them from Instance.HeadroomProxy()/CacheTTL1h() and apply them
// through Instance.SetLaunchOptions instead.
func Parse(program string) (opts Options, baseProgram string) {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return opts, ""
	}

	// Compose's Build*Command helpers no-op (add no flag) for
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
			opts.Model, opts.Context1M = parseModelValue(parts[i+1])
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

// parseModelValue decodes a --model token into its alias and whether
// Claude's [1m] long-context suffix was present.
//
// It is deliberately more permissive than what Compose
// emits. Single quotes are stripped because ApplyModelFlag now quotes
// its value, while every Program persisted before that change has none,
// and those records are decoded on every load through
// Storage.LoadAndReconcile — so both shapes must be accepted
// permanently. The suffix match is case-insensitive to mirror Claude
// Code's own /\[1m\]/i test, so a hand-edited Program decodes the same
// way loom's own output does.
//
// A token that is nothing but the suffix is returned unchanged rather
// than yielding an empty alias, so a degenerate input can't silently
// turn into "no --model flag".
func parseModelValue(tok string) (model string, context1M bool) {
	if len(tok) >= 2 && strings.HasPrefix(tok, "'") && strings.HasSuffix(tok, "'") {
		tok = tok[1 : len(tok)-1]
	}
	const suffix = "[1m]"
	if len(tok) > len(suffix) && strings.EqualFold(tok[len(tok)-len(suffix):], suffix) {
		return tok[:len(tok)-len(suffix)], true
	}
	return tok, false
}

// RemoteControlBlocked reports whether a launch of program should be
// interrupted to tell the user remote control can't work: the toggle is
// on (rcEnabled, after EffectiveRemoteControl), the program is Claude, and
// the launching account's auth was clearly determined incompatible.
func RemoteControlBlocked(auth session.RemoteControlAuth, rcEnabled bool, program string) bool {
	return rcEnabled && session.IsClaudeProgram(program) && auth.Blocked()
}
