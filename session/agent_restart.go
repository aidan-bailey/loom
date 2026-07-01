package session

import (
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
