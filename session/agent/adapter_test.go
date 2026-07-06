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

func TestClaudeRecoveryFlagThroughHeadroomWrap(t *testing.T) {
	c := Claude()
	assert.Equal(t, "headroom wrap claude --continue", c.ApplyRecoveryFlag("headroom wrap claude"))
	assert.Equal(t, "headroom wrap claude --continue --model opus", c.ApplyRecoveryFlag("headroom wrap claude --model opus"))
	assert.Equal(t, "headroom wrap claude --continue", c.ApplyRecoveryFlag("headroom wrap claude --continue"))
}

func TestDefaultRegistryLookupThroughHeadroomWrap(t *testing.T) {
	r := DefaultRegistry()
	assert.Equal(t, "claude", r.Lookup("headroom wrap claude").Name())
	assert.Equal(t, "claude", r.Lookup("headroom wrap claude --model opus").Name())
	assert.Equal(t, "aider", r.Lookup("headroom wrap aider --model gemma").Name())
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

func TestClaudePermissionModeFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name    string
		program string
		mode    string
		want    string
	}{
		{"plain", "claude", "acceptEdits", "claude --permission-mode acceptEdits"},
		{"preserves flags", "claude --model sonnet", "plan", "claude --permission-mode plan --model sonnet"},
		{"absolute path", "/usr/bin/claude", "bypassPermissions", "/usr/bin/claude --permission-mode bypassPermissions"},
		{"empty mode is no-op", "claude --model sonnet", "", "claude --model sonnet"},
		{"\"default\" mode is no-op", "claude --model sonnet", "default", "claude --model sonnet"},
		{"idempotent bare", "claude --permission-mode acceptEdits", "plan", "claude --permission-mode acceptEdits"},
		{"idempotent equals form", "claude --permission-mode=acceptEdits", "plan", "claude --permission-mode=acceptEdits"},
		{"empty program", "", "acceptEdits", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.ApplyPermissionModeFlag(tc.program, tc.mode))
		})
	}
}

func TestClaudePermissionModeComposesWithRemoteControl(t *testing.T) {
	// permission-mode and remote-control are applied by two independent
	// wrapper calls at instance-creation time (app/remote_control.go);
	// each must only ever touch its own flag name so composing them in
	// either order still produces one well-formed command.
	c := Claude()
	withRC := c.ApplyRemoteControlFlag("claude", "my task")
	assert.Equal(t, "claude --remote-control my-task", withRC)
	assert.Equal(t, "claude --permission-mode acceptEdits --remote-control my-task", c.ApplyPermissionModeFlag(withRC, "acceptEdits"))
}

func TestNonClaudeAdaptersNoPermissionMode(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyPermissionModeFlag("aider --model x", "acceptEdits"))
	assert.Equal(t, "gemini", Gemini().ApplyPermissionModeFlag("gemini", "acceptEdits"))
	assert.Equal(t, "codex --foo", Default().ApplyPermissionModeFlag("codex --foo", "acceptEdits"))
}

func TestClaudeModelFlag(t *testing.T) {
	c := Claude()

	cases := []struct {
		name    string
		program string
		model   string
		want    string
	}{
		{"plain", "claude", "sonnet", "claude --model sonnet"},
		{"preserves flags", "claude --permission-mode plan", "opus", "claude --model opus --permission-mode plan"},
		{"absolute path", "/usr/bin/claude", "haiku", "/usr/bin/claude --model haiku"},
		{"empty model is no-op", "claude --permission-mode plan", "", "claude --permission-mode plan"},
		{"\"default\" model is no-op", "claude --permission-mode plan", "default", "claude --permission-mode plan"},
		{"idempotent bare", "claude --model sonnet", "opus", "claude --model sonnet"},
		{"idempotent equals form", "claude --model=sonnet", "opus", "claude --model=sonnet"},
		{"empty program", "", "sonnet", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.ApplyModelFlag(tc.program, tc.model))
		})
	}
}

func TestClaudeModelComposesWithPermissionModeAndRemoteControl(t *testing.T) {
	c := Claude()
	withRC := c.ApplyRemoteControlFlag("claude", "my task")
	withPM := c.ApplyPermissionModeFlag(withRC, "acceptEdits")
	got := c.ApplyModelFlag(withPM, "opus")
	assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
}

func TestNonClaudeAdaptersNoModelFlag(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyModelFlag("aider --model x", "opus"))
	assert.Equal(t, "gemini", Gemini().ApplyModelFlag("gemini", "opus"))
	assert.Equal(t, "codex --foo", Default().ApplyModelFlag("codex --foo", "opus"))
}

func TestClaudeEffortFlag(t *testing.T) {
	c := Claude()
	assert.Equal(t, "claude --effort high", c.ApplyEffortFlag("claude", "high"))
	assert.Equal(t, "claude", c.ApplyEffortFlag("claude", ""))
	assert.Equal(t, "claude", c.ApplyEffortFlag("claude", "default"))
	assert.Equal(t, "claude --effort high", c.ApplyEffortFlag("claude --effort high", "max"), "idempotent: existing flag wins")
	assert.Equal(t, "claude --effort low --model opus", c.ApplyEffortFlag("claude --model opus", "low"))
}

func TestNonClaudeAdaptersNoEffortFlag(t *testing.T) {
	assert.Equal(t, "aider --model x", Aider().ApplyEffortFlag("aider --model x", "high"))
	assert.Equal(t, "gemini", Gemini().ApplyEffortFlag("gemini", "high"))
	assert.Equal(t, "codex --foo", Default().ApplyEffortFlag("codex --foo", "high"))
}

func TestTrustPromptResponses(t *testing.T) {
	assert.Equal(t, TrustPromptTapEnter, Claude().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Aider().TrustPromptResponse())
	assert.Equal(t, TrustPromptTapDAndEnter, Gemini().TrustPromptResponse())
	assert.Equal(t, TrustPromptNone, Default().TrustPromptResponse())
}
