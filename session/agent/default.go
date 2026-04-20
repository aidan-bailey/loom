package agent

type defaultAdapter struct{}

// Default returns the fallback adapter used when no registered adapter
// matches. It disables agent-specific features (trust-prompt handling,
// auto-yes detection, recovery-flag insertion) and returns the program
// string unchanged.
func Default() Adapter { return defaultAdapter{} }

// Name implements Adapter.
func (defaultAdapter) Name() string { return "default" }

// Matches implements Adapter. The default adapter matches every
// program name, making it the catch-all fallback.
func (defaultAdapter) Matches(_ string) bool { return true }

// TrustPromptPatterns implements Adapter.
func (defaultAdapter) TrustPromptPatterns() []string { return nil }

// TrustPromptResponse implements Adapter.
func (defaultAdapter) TrustPromptResponse() TrustPromptAction { return TrustPromptNone }

// PendingPromptPattern implements Adapter.
func (defaultAdapter) PendingPromptPattern() string { return "" }

// ApplyRecoveryFlag implements Adapter.
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
