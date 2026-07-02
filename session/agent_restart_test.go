package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRecoveryCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --continue", BuildRecoveryCommand("claude"))
}

func TestBuildRecoveryCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t, "claude --continue --model sonnet", BuildRecoveryCommand("claude --model sonnet"))
}

func TestBuildRecoveryCommand_ClaudeAlreadyHasContinue(t *testing.T) {
	assert.Equal(t, "claude --continue", BuildRecoveryCommand("claude --continue"))
}

func TestBuildRecoveryCommand_ClaudeAlreadyHasResume(t *testing.T) {
	assert.Equal(t, "claude --resume", BuildRecoveryCommand("claude --resume"))
}

func TestBuildRecoveryCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildRecoveryCommand("aider --model gemma"))
}

func TestBuildRecoveryCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildRecoveryCommand("codex"))
}

func TestBuildRecoveryCommand_ClaudeSubstring(t *testing.T) {
	// "claudette" should NOT match
	assert.Equal(t, "claudette", BuildRecoveryCommand("claudette"))
}

func TestBuildRecoveryCommand_ClaudeAbsolutePath(t *testing.T) {
	// Config.Program often stores the absolute path (e.g. from `which claude`).
	// The basename must still match and --continue must be appended.
	assert.Equal(t,
		"/etc/profiles/per-user/aidanb/bin/claude --continue",
		BuildRecoveryCommand("/etc/profiles/per-user/aidanb/bin/claude"),
	)
}

func TestBuildRecoveryCommand_ClaudeAbsolutePathWithFlags(t *testing.T) {
	assert.Equal(t,
		"/usr/bin/claude --continue --model sonnet",
		BuildRecoveryCommand("/usr/bin/claude --model sonnet"),
	)
}

func TestBuildRecoveryCommand_ClaudettePath(t *testing.T) {
	// Basename "claudette" must not match even at an absolute path.
	assert.Equal(t, "/usr/bin/claudette", BuildRecoveryCommand("/usr/bin/claudette"))
}

func TestBuildRemoteControlCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --remote-control fix-bug", BuildRemoteControlCommand("claude", "fix bug"))
}

func TestBuildRemoteControlCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t,
		"claude --remote-control task --model sonnet",
		BuildRemoteControlCommand("claude --model sonnet", "task"),
	)
}

func TestBuildRemoteControlCommand_Idempotent(t *testing.T) {
	assert.Equal(t, "claude --remote-control keep", BuildRemoteControlCommand("claude --remote-control keep", "other"))
}

func TestBuildRemoteControlCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildRemoteControlCommand("aider --model gemma", "task"))
}

func TestBuildRemoteControlCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildRemoteControlCommand("codex", "task"))
}

func TestBuildPermissionModeCommand_Claude(t *testing.T) {
	assert.Equal(t, "claude --permission-mode acceptEdits", BuildPermissionModeCommand("claude", "acceptEdits"))
}

func TestBuildPermissionModeCommand_ClaudeWithFlags(t *testing.T) {
	assert.Equal(t,
		"claude --permission-mode plan --model sonnet",
		BuildPermissionModeCommand("claude --model sonnet", "plan"),
	)
}

func TestBuildPermissionModeCommand_DefaultModeIsNoOp(t *testing.T) {
	assert.Equal(t, "claude --model sonnet", BuildPermissionModeCommand("claude --model sonnet", "default"))
	assert.Equal(t, "claude --model sonnet", BuildPermissionModeCommand("claude --model sonnet", ""))
}

func TestBuildPermissionModeCommand_Idempotent(t *testing.T) {
	assert.Equal(t,
		"claude --permission-mode plan",
		BuildPermissionModeCommand("claude --permission-mode plan", "acceptEdits"),
	)
}

func TestBuildPermissionModeCommand_Aider(t *testing.T) {
	assert.Equal(t, "aider --model gemma", BuildPermissionModeCommand("aider --model gemma", "acceptEdits"))
}

func TestBuildPermissionModeCommand_Unknown(t *testing.T) {
	assert.Equal(t, "codex", BuildPermissionModeCommand("codex", "acceptEdits"))
}
