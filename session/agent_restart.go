package session

import (
	"os"
	"regexp"

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

// claudeSessionIDRe matches the UUIDs Claude uses as session IDs. The ID
// is inserted into a shell command, so anything else is refused.
var claudeSessionIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// BuildResumeCommand rewrites program to resume the recorded conversation:
// --resume <sessionID> when the ID is a well-formed Claude session ID and
// its transcript still exists, else BuildRecoveryCommand's --continue.
// The transcript check matters because Claude deletes transcripts after
// cleanupPeriodDays (30 by default), and --resume on a missing one exits
// at once, which the health tick would read as the agent dying again.
func BuildResumeCommand(program, sessionID, transcriptPath string) string {
	if claudeSessionIDRe.MatchString(sessionID) && transcriptPath != "" {
		if _, err := os.Stat(transcriptPath); err == nil {
			return defaultRegistry.Lookup(program).ApplyResumeFlag(program, sessionID)
		}
	}
	return BuildRecoveryCommand(program)
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

// ClaudeFullscreenEnv returns the tmux session environment variable that
// forces Claude's fullscreen (alternate-screen) renderer. Unconditional
// for Claude, a no-op (nil) for every other program.
//
// Loom cannot scroll Claude's classic inline renderer: it wraps every
// frame in synchronized output (DEC 2026), so tmux repaints Loom's attach
// client instead of scrolling it and the pane emulator never accumulates
// scrollback (TestSyncOutputDefeatsEmulatorScrollback_RealTmux). On the
// alternate screen Loom forwards the wheel into Claude, which scrolls its
// own transcript. The env var (not --settings) keeps `/tui default`
// usable per session: Claude drops CLAUDE_CODE_NO_FLICKER when it
// relaunches for a renderer switch.
func ClaudeFullscreenEnv(program string) []string {
	if !IsClaudeProgram(program) {
		return nil
	}
	return []string{"CLAUDE_CODE_NO_FLICKER=1"}
}

// LaunchEnv is everything about an instance's launch that becomes tmux
// session environment rather than part of the program string.
type LaunchEnv struct {
	Program       string
	HeadroomProxy bool
	CacheTTL1h    bool
	// ClaudeConfigDir is the CLAUDE_CONFIG_DIR of the account the session
	// runs on; empty for the default account.
	ClaudeConfigDir string
}

// ClaudeConfigDirEnv returns the tmux session environment variable that
// runs Claude as the account whose config dir is dir. A no-op (nil) for the
// default account (empty dir) and for every program but Claude.
func ClaudeConfigDirEnv(dir, program string) []string {
	if dir == "" || !IsClaudeProgram(program) {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + dir}
}

// InstanceEnv combines every per-session environment variable derived
// from an instance's launch (Headroom Proxy, Cache TTL, the account's
// config dir) plus the always-on Claude fullscreen renderer into the
// single slice tmux.NewTmuxSession's variadic env parameter needs.
// Centralized here so the four Instance call sites that construct a
// TmuxSession don't each repeat the same combination.
func InstanceEnv(e LaunchEnv) []string {
	env := append(HeadroomProxyEnv(e.HeadroomProxy, e.Program), CacheTTL1hEnv(e.CacheTTL1h, e.Program)...)
	env = append(env, ClaudeFullscreenEnv(e.Program)...)
	return append(env, ClaudeConfigDirEnv(e.ClaudeConfigDir, e.Program)...)
}
