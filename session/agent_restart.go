package session

import (
	"strings"

	"github.com/aidan-bailey/loom/session/agent"
)

// defaultRegistry is the package-level adapter registry used by
// BuildRecoveryCommand and other call sites that don't have a scoped
// registry handy. A test can swap this out if needed.
var defaultRegistry = agent.DefaultRegistry()

// BuildRecoveryCommand modifies a program command string for crash
// recovery. The adapter registry decides whether and how the string is
// modified (e.g. "claude" → "claude --continue"). Unsupported agents
// are returned unchanged.
func BuildRecoveryCommand(program string) string {
	return defaultRegistry.Lookup(program).ApplyRecoveryFlag(program)
}

// BuildRemoteControlCommand modifies a program command string to launch
// the agent with its remote-control mode enabled, naming the remote
// session after sessionName. The adapter registry decides whether and
// how the string is modified (e.g. "claude" → "claude --remote-control
// <name>"). Idempotent, and a no-op for agents without a remote-control
// mode.
func BuildRemoteControlCommand(program, sessionName string) string {
	return defaultRegistry.Lookup(program).ApplyRemoteControlFlag(program, sessionName)
}

// BuildPermissionModeCommand modifies a program command string to
// launch with the given --permission-mode value. The adapter registry
// decides whether and how the string is modified. Idempotent, and a
// no-op for agents without a permission-mode concept or when mode is
// "" / "default".
func BuildPermissionModeCommand(program, mode string) string {
	return defaultRegistry.Lookup(program).ApplyPermissionModeFlag(program, mode)
}

// BuildModelCommand modifies a program command string to launch with
// the given --model value. The adapter registry decides whether and how
// the string is modified. Idempotent, and a no-op for agents without a
// model-selection concept or when model is "" / "default".
func BuildModelCommand(program, model string) string {
	return defaultRegistry.Lookup(program).ApplyModelFlag(program, model)
}

// headroomSupportedTools lists the adapter Name() values Headroom's
// `wrap` subcommand recognizes (`headroom wrap --help`: claude, codex,
// copilot, aider, vibe, cursor, cline, continue, goose, openhands,
// openclaw, opencode). Only the subset loom also has an adapter for
// is relevant here.
var headroomSupportedTools = map[string]bool{
	"claude": true,
	"aider":  true,
}

// BuildHeadroomWrapCommand rewrites program into a `headroom wrap
// <tool> <args>` invocation. Headroom's `wrap` subcommand takes a
// fixed tool keyword (e.g. "claude"), not an arbitrary binary — the
// configured program's first token is typically an absolute path
// (e.g. "/etc/profiles/.../bin/claude"), which `headroom wrap` rejects
// with "Error: No such command '<path>'." and exits immediately. So
// this substitutes the first token with the matched adapter's Name()
// and keeps the remaining flags, rather than prefixing the program
// string unchanged. A no-op when the adapter isn't one Headroom
// supports (e.g. gemini, or an unrecognized program) — wrapping those
// would still fail the same way. Idempotent: no-ops if program is
// already wrapped. Also a no-op for an empty program, matching the
// empty-input guard every ApplyXFlag implementation has.
func BuildHeadroomWrapCommand(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	if len(parts) >= 2 && parts[0] == "headroom" && parts[1] == "wrap" {
		return program
	}
	name := defaultRegistry.Lookup(program).Name()
	if !headroomSupportedTools[name] {
		return program
	}
	rest := strings.TrimPrefix(program, parts[0])
	return "headroom wrap " + name + rest
}
