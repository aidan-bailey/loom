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

func TestClaudeRemoteControlFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name    string
		program string
		session string
		want    string
	}{
		{"plain named", "claude", "fix login bug", "claude --remote-control fix-login-bug"},
		{"preserves flags", "claude --model sonnet", "My Feature", "claude --remote-control My-Feature --model sonnet"},
		{"absolute path", "/usr/bin/claude", "task", "/usr/bin/claude --remote-control task"},
		{"strips unsafe chars", "claude", "fix: cache/bug (v2)!", "claude --remote-control fix-cachebug-v2"},
		{"empty title omits name", "claude", "", "claude --remote-control"},
		{"unsanitizable title omits name", "claude", "日本語", "claude --remote-control"},
		{"idempotent bare", "claude --remote-control", "task", "claude --remote-control"},
		{"idempotent named", "claude --remote-control existing", "task", "claude --remote-control existing"},
		{"idempotent equals form", "claude --remote-control=existing", "task", "claude --remote-control=existing"},
		{"empty program", "", "task", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.ApplyRemoteControlFlag(tc.program, tc.session))
		})
	}
}

func TestClaudeRemoteControlComposesWithRecovery(t *testing.T) {
	// A persisted program already carrying --remote-control must survive a
	// later recovery rewrite, with the name kept adjacent to its flag.
	c := Claude()
	rc := c.ApplyRemoteControlFlag("claude", "my task")
	assert.Equal(t, "claude --remote-control my-task", rc)
	assert.Equal(t, "claude --continue --remote-control my-task", c.ApplyRecoveryFlag(rc))
}

func TestNonClaudeAdaptersNoRemoteControl(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyRemoteControlFlag("aider --model x", "t"))
	assert.Equal(t, "gemini", Gemini().ApplyRemoteControlFlag("gemini", "t"))
	assert.Equal(t, "codex --foo", Default().ApplyRemoteControlFlag("codex --foo", "t"))
}

func TestTrustPromptResponses(t *testing.T) {
	assert.Equal(t, TrustPromptTapEnter, Claude().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Aider().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Gemini().TrustPromptResponse())
	assert.Equal(t, TrustPromptNone, Default().TrustPromptResponse())
}
