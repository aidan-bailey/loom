package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultRegistryLookup(t *testing.T) {
	r := DefaultRegistry()

	cases := []struct {
		program  string
		wantName string
	}{
		{"claude", "claude"},
		{"claude --continue", "claude"},
		{"/nix/store/hash/bin/claude --model sonnet", "claude"},
		{"aider", "aider"},
		{"aider --model ollama_chat/gemma3:1b", "aider"},
		{"gemini", "gemini"},
		{"codex", "default"},
		{"claudette", "default"},
		{"", "default"},
	}
	for _, tc := range cases {
		t.Run(tc.program, func(t *testing.T) {
			got := r.Lookup(tc.program).Name()
			assert.Equal(t, tc.wantName, got)
		})
	}
}

func TestClaudeRecoveryFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "claude", "claude --continue"},
		{"with flags", "claude --model sonnet", "claude --continue --model sonnet"},
		{"already has continue", "claude --continue", "claude --continue"},
		{"already has resume", "claude --resume abc", "claude --resume abc"},
		{"absolute path", "/usr/bin/claude", "/usr/bin/claude --continue"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.ApplyRecoveryFlag(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNonClaudeAdaptersNoRecovery(t *testing.T) {
	// aider and gemini don't modify the program string — there's no
	// CLI equivalent of --continue for those agents.
	assert.Equal(t, "aider --model x", Aider().ApplyRecoveryFlag("aider --model x"))
	assert.Equal(t, "gemini", Gemini().ApplyRecoveryFlag("gemini"))
	assert.Equal(t, "codex --foo", Default().ApplyRecoveryFlag("codex --foo"))
}

func TestTrustPromptResponses(t *testing.T) {
	assert.Equal(t, TrustPromptTapEnter, Claude().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Aider().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Gemini().TrustPromptResponse())
	assert.Equal(t, TrustPromptNone, Default().TrustPromptResponse())
}
