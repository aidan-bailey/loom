package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
)

func TestRemoteControlProgram(t *testing.T) {
	enabled := &config.Config{ClaudeRemoteControl: boolPtrTest(true)}
	disabled := &config.Config{ClaudeRemoteControl: boolPtrTest(false)}
	defaultOn := &config.Config{} // nil pointer => enabled by default

	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	authBlocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	authUnknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	t.Run("enabled + auth OK rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control fix-bug", remoteControlProgram(enabled, authOK, "claude", "fix bug"))
	})

	t.Run("default (nil toggle) + auth OK rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control task", remoteControlProgram(defaultOn, authOK, "claude", "task"))
	})

	t.Run("auth Blocked leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(enabled, authBlocked, "claude", "task"))
	})

	t.Run("auth Unknown leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(enabled, authUnknown, "claude", "task"))
	})

	t.Run("disabled leaves program untouched even when auth OK", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(disabled, authOK, "claude", "task"))
	})

	t.Run("nil config leaves program untouched", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(nil, authOK, "claude", "task"))
	})

	t.Run("non-claude program is a no-op even when enabled + auth OK", func(t *testing.T) {
		assert.Equal(t, "aider --model x", remoteControlProgram(enabled, authOK, "aider --model x", "task"))
	})
}

func TestRemoteControlBlocked(t *testing.T) {
	enabled := &config.Config{ClaudeRemoteControl: boolPtrTest(true)}
	disabled := &config.Config{ClaudeRemoteControl: boolPtrTest(false)}
	blocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	unknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	cases := []struct {
		name    string
		cfg     *config.Config
		auth    session.RemoteControlAuth
		program string
		want    bool
	}{
		{"enabled + claude + blocked", enabled, blocked, "claude", true},
		{"enabled + claude + ok", enabled, ok, "claude", false},
		{"enabled + claude + unknown", enabled, unknown, "claude", false},
		{"enabled + non-claude + blocked", enabled, blocked, "aider", false},
		{"disabled + claude + blocked", disabled, blocked, "claude", false},
		{"nil config", nil, blocked, "claude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &home{appConfig: tc.cfg, rcAuth: tc.auth}
			assert.Equal(t, tc.want, m.remoteControlBlocked(tc.program))
		})
	}
}

func boolPtrTest(b bool) *bool { return &b }
