package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
)

func TestRemoteControlProgram(t *testing.T) {
	enabled := &config.Config{ClaudeRemoteControl: boolPtrTest(true)}
	disabled := &config.Config{ClaudeRemoteControl: boolPtrTest(false)}
	defaultOn := &config.Config{} // nil pointer => enabled by default

	t.Run("enabled rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control fix-bug", remoteControlProgram(enabled, "claude", "fix bug"))
	})

	t.Run("default (nil toggle) rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control task", remoteControlProgram(defaultOn, "claude", "task"))
	})

	t.Run("disabled leaves program untouched", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(disabled, "claude", "task"))
	})

	t.Run("nil config leaves program untouched", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(nil, "claude", "task"))
	})

	t.Run("non-claude program is a no-op even when enabled", func(t *testing.T) {
		assert.Equal(t, "aider --model x", remoteControlProgram(enabled, "aider --model x", "task"))
	})
}

func boolPtrTest(b bool) *bool { return &b }
