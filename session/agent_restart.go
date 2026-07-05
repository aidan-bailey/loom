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

// BuildHeadroomWrapCommand prefixes program with "headroom wrap ",
// wrapping the agent invocation regardless of which adapter matches —
// Headroom's context compression works the same way for every agent, so
// this bypasses the adapter registry entirely. Idempotent: no-ops if
// program is already wrapped.
func BuildHeadroomWrapCommand(program string) string {
	parts := strings.Fields(program)
	if len(parts) >= 2 && parts[0] == "headroom" && parts[1] == "wrap" {
		return program
	}
	return "headroom wrap " + program
}
