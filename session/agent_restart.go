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

// BuildEffortCommand modifies a program command string to launch with
// the given --effort value. The adapter registry decides whether and
// how the string is modified. Idempotent, and a no-op for agents
// without an effort-level concept or when effort is "" / "default".
func BuildEffortCommand(program, effort string) string {
	return defaultRegistry.Lookup(program).ApplyEffortFlag(program, effort)
}

// HeadroomProxyURL is the base URL Loom points ANTHROPIC_BASE_URL at
// when the Headroom Proxy launch option is enabled — Headroom's own
// default proxy address (`headroom proxy`, port 8787). Loom does not
// start or manage the proxy process itself; the user is expected to
// have it running already (e.g. via `headroom install`'s persistent
// deployment).
const HeadroomProxyURL = "http://127.0.0.1:8787"

// HeadroomProxyEnv returns the tmux session environment variables
// needed to route program's API calls through Headroom's proxy. A
// no-op (nil) unless enabled and program resolves to Claude —
// ANTHROPIC_BASE_URL is Anthropic-API-specific, so setting it for any
// other agent wouldn't do anything useful.
func HeadroomProxyEnv(enabled bool, program string) []string {
	if !enabled || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"ANTHROPIC_BASE_URL=" + HeadroomProxyURL}
}

// CacheTTL1hEnv returns the tmux session environment variable that
// extends Claude's prompt cache from the default 5-minute TTL to 1
// hour. A no-op (nil) unless enabled and program resolves to Claude —
// ENABLE_PROMPT_CACHING_1H is a Claude-CLI-specific toggle, so setting
// it for any other agent wouldn't do anything useful.
func CacheTTL1hEnv(enabled bool, program string) []string {
	if !enabled || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"ENABLE_PROMPT_CACHING_1H=1"}
}

// InstanceEnv combines every per-session environment variable derived
// from an instance's launch options (Headroom Proxy, Cache TTL) into
// the single slice tmux.NewTmuxSession's variadic env parameter needs.
// Centralized here so the four Instance call sites that construct a
// TmuxSession don't each repeat the same combination.
func InstanceEnv(program string, headroomProxy, cacheTTL1h bool) []string {
	return append(HeadroomProxyEnv(headroomProxy, program), CacheTTL1hEnv(cacheTTL1h, program)...)
}
