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

// ApplyRemoteControlFlag is a no-op for gemini — it has no remote-control mode.
func (geminiAdapter) ApplyRemoteControlFlag(program, _ string) string {
	return program
}

// ApplyPermissionModeFlag is a no-op for gemini — it has no
// permission-mode equivalent.
func (geminiAdapter) ApplyPermissionModeFlag(program, _ string) string {
	return program
}

// ApplyModelFlag is a no-op for gemini — model selection isn't exposed
// through this settings screen for non-Claude agents.
func (geminiAdapter) ApplyModelFlag(program, _ string) string {
	return program
}

// ApplyEffortFlag is a no-op for gemini — effort levels aren't exposed
// through this settings screen for non-Claude agents.
func (geminiAdapter) ApplyEffortFlag(program, _ string) string {
	return program
}
