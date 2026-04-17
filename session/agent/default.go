package agent

type defaultAdapter struct{}

// Default returns the fallback adapter used when no registered adapter
// matches. It disables agent-specific features (trust-prompt handling,
// auto-yes detection, recovery-flag insertion) and returns the program
// string unchanged.
func Default() Adapter { return defaultAdapter{} }

func (defaultAdapter) Name() string                            { return "default" }
func (defaultAdapter) Matches(_ string) bool                   { return true }
func (defaultAdapter) TrustPromptPatterns() []string           { return nil }
func (defaultAdapter) TrustPromptResponse() TrustPromptAction  { return TrustPromptNone }
func (defaultAdapter) PendingPromptPattern() string            { return "" }
func (defaultAdapter) ApplyRecoveryFlag(program string) string { return program }

// DefaultRegistry returns the registry pre-populated with all built-in
// adapters and the fallback.
func DefaultRegistry() *Registry {
	return NewRegistry(
		Default(),
		Claude(),
		Aider(),
		Gemini(),
	)
}
