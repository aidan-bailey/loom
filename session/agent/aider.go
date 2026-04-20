package agent

type aiderAdapter struct{}

// Aider returns the adapter for the aider agent.
func Aider() Adapter { return aiderAdapter{} }

// Name implements Adapter.
func (aiderAdapter) Name() string { return "aider" }

// Matches implements Adapter.
func (aiderAdapter) Matches(program string) bool {
	return basenameMatch(program, "aider")
}

// TrustPromptPatterns implements Adapter.
func (aiderAdapter) TrustPromptPatterns() []string {
	return []string{"Open documentation url for more info"}
}

// TrustPromptResponse implements Adapter.
func (aiderAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapDAndEnter
}

// PendingPromptPattern implements Adapter.
func (aiderAdapter) PendingPromptPattern() string {
	return "(Y)es/(N)o/(D)on't ask again"
}

// ApplyRecoveryFlag is a no-op for aider — there's no equivalent of
// --continue in aider's CLI.
func (aiderAdapter) ApplyRecoveryFlag(program string) string {
	return program
}
