package agent

type geminiAdapter struct{}

// Gemini returns the adapter for the gemini agent.
func Gemini() Adapter { return geminiAdapter{} }

// Name implements Adapter.
func (geminiAdapter) Name() string { return "gemini" }

// Matches implements Adapter.
func (geminiAdapter) Matches(program string) bool {
	return basenameMatch(program, "gemini")
}

// TrustPromptPatterns implements Adapter.
func (geminiAdapter) TrustPromptPatterns() []string {
	return []string{"Open documentation url for more info"}
}

// TrustPromptResponse implements Adapter.
func (geminiAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapDAndEnter
}

// PendingPromptPattern implements Adapter.
func (geminiAdapter) PendingPromptPattern() string {
	return "Yes, allow once"
}

// ApplyRecoveryFlag is a no-op for gemini.
func (geminiAdapter) ApplyRecoveryFlag(program string) string {
	return program
}
