package agent

type aiderAdapter struct{}

// Aider returns the adapter for the aider agent.
func Aider() Adapter { return aiderAdapter{} }

func (aiderAdapter) Name() string { return "aider" }

func (aiderAdapter) Matches(program string) bool {
	return basenameMatch(program, "aider")
}

func (aiderAdapter) TrustPromptPatterns() []string {
	return []string{"Open documentation url for more info"}
}

func (aiderAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapDAndEnter
}

func (aiderAdapter) PendingPromptPattern() string {
	return "(Y)es/(N)o/(D)on't ask again"
}

// ApplyRecoveryFlag is a no-op for aider — there's no equivalent of
// --continue in aider's CLI.
func (aiderAdapter) ApplyRecoveryFlag(program string) string {
	return program
}
