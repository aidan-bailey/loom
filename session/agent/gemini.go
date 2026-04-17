package agent

type geminiAdapter struct{}

// Gemini returns the adapter for the gemini agent.
func Gemini() Adapter { return geminiAdapter{} }

func (geminiAdapter) Name() string { return "gemini" }

func (geminiAdapter) Matches(program string) bool {
	return basenameMatch(program, "gemini")
}

func (geminiAdapter) TrustPromptPatterns() []string {
	return []string{"Open documentation url for more info"}
}

func (geminiAdapter) TrustPromptResponse() TrustPromptAction {
	return TrustPromptTapDAndEnter
}

func (geminiAdapter) PendingPromptPattern() string {
	return "Yes, allow once"
}

// ApplyRecoveryFlag is a no-op for gemini.
func (geminiAdapter) ApplyRecoveryFlag(program string) string {
	return program
}
