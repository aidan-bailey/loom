package agent

import "strings"

type claudeAdapter struct{}

// Claude returns the adapter for the Claude Code agent.
func Claude() Adapter { return claudeAdapter{} }

func (claudeAdapter) Name() string { return "claude" }

func (claudeAdapter) Matches(program string) bool {
	return basenameMatch(program, "claude")
}

func (claudeAdapter) TrustPromptPatterns() []string {
	return []string{
		"Do you trust the files in this folder?",
		"new MCP server",
	}
}

func (claudeAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapEnter
}

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
