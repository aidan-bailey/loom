package agent

import (
	"regexp"
	"strings"
)

type claudeAdapter struct{}

// Claude returns the adapter for the Claude Code agent.
func Claude() Adapter { return claudeAdapter{} }

// Name implements Adapter.
func (claudeAdapter) Name() string { return "claude" }

// Matches implements Adapter.
func (claudeAdapter) Matches(program string) bool {
	return basenameMatch(program, "claude")
}

// TrustPromptPatterns implements Adapter.
func (claudeAdapter) TrustPromptPatterns() []string {
	return []string{
		"Do you trust the files in this folder?",
		"new MCP server",
	}
}

// TrustPromptResponse implements Adapter.
func (claudeAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapEnter
}

// PendingPromptPattern implements Adapter.
func (claudeAdapter) PendingPromptPattern() string {
	return "No, and tell Claude what to do differently"
}

// ApplyRecoveryFlag inserts --continue after "claude", preserving the
// original program's remaining flags. Returns program unchanged if
// --continue or --resume is already present.
func (claudeAdapter) ApplyRecoveryFlag(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--continue" || p == "--resume" {
			return program
		}
	}
	return parts[0] + " --continue" + strings.TrimPrefix(program, parts[0])
}

// remoteControlNameRe matches every run of characters that are not safe
// in a bare shell token. `tmux new-session` runs the program string
// through the shell, so a --remote-control session name derived from a
// user-supplied title must be reduced to this safe subset before it is
// appended.
var remoteControlNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ApplyRemoteControlFlag inserts "--remote-control <name>" after
// "claude", naming the remote session after sessionName (sanitized to a
// shell-safe token). When the title sanitizes to nothing the name is
// omitted and Claude auto-generates one. Returns program unchanged if a
// --remote-control flag is already present or if program is empty.
func (claudeAdapter) ApplyRemoteControlFlag(program, sessionName string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--remote-control" || strings.HasPrefix(p, "--remote-control=") {
			return program
		}
	}
	flag := " --remote-control"
	if name := sanitizeRemoteControlName(sessionName); name != "" {
		flag += " " + name
	}
	return parts[0] + flag + strings.TrimPrefix(program, parts[0])
}

// sanitizeRemoteControlName reduces an arbitrary session title to a
// shell-safe, flag-safe token usable as a --remote-control name: spaces
// become dashes and any remaining unsafe characters are dropped. Returns
// "" when nothing usable remains. The result never begins with a dash,
// so it can't be mistaken for a flag by Claude's argument parser.
func sanitizeRemoteControlName(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "-")
	s = remoteControlNameRe.ReplaceAllString(s, "")
	return strings.Trim(s, "-_")
}
