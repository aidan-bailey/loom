package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
)

func boolPtrTest(b bool) *bool { return &b }

func stringPtrTest(s string) *string { return &s }

func TestRemoteControlProgram(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	authBlocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}
	authUnknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	t.Run("enabled + auth OK rewrites claude", func(t *testing.T) {
		assert.Equal(t, "claude --remote-control fix-bug", remoteControlProgram(true, authOK, "claude", "fix bug"))
	})

	t.Run("auth Blocked leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(true, authBlocked, "claude", "task"))
	})

	t.Run("auth Unknown leaves program untouched (fail closed)", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(true, authUnknown, "claude", "task"))
	})

	t.Run("disabled leaves program untouched even when auth OK", func(t *testing.T) {
		assert.Equal(t, "claude", remoteControlProgram(false, authOK, "claude", "task"))
	})

	t.Run("non-claude program is a no-op even when enabled + auth OK", func(t *testing.T) {
		assert.Equal(t, "aider --model x", remoteControlProgram(true, authOK, "aider --model x", "task"))
	})
}

func TestRemoteControlBlocked(t *testing.T) {
	blocked := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	unknown := session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}

	cases := []struct {
		name      string
		rcEnabled bool
		auth      session.RemoteControlAuth
		program   string
		want      bool
	}{
		{"enabled + claude + blocked", true, blocked, "claude", true},
		{"enabled + claude + ok", true, ok, "claude", false},
		{"enabled + claude + unknown", true, unknown, "claude", false},
		{"enabled + non-claude + blocked", true, blocked, "aider", false},
		{"disabled + claude + blocked", false, blocked, "claude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &home{rcAuth: tc.auth}
			assert.Equal(t, tc.want, m.remoteControlBlocked(tc.rcEnabled, tc.program))
		})
	}
}

func TestPermissionModeProgram(t *testing.T) {
	t.Run("default mode is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", permissionModeProgram("default", "claude --model sonnet"))
	})

	t.Run("explicit mode is injected", func(t *testing.T) {
		assert.Equal(t, "claude --permission-mode acceptEdits --model sonnet", permissionModeProgram("acceptEdits", "claude --model sonnet"))
	})

	t.Run("non-claude program is a no-op", func(t *testing.T) {
		assert.Equal(t, "aider --model gemma", permissionModeProgram("acceptEdits", "aider --model gemma"))
	})
}

func TestModelProgram(t *testing.T) {
	t.Run("default model is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --permission-mode plan", modelProgram("default", "claude --permission-mode plan"))
	})

	t.Run("explicit model is injected", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet --permission-mode plan", modelProgram("sonnet", "claude --permission-mode plan"))
	})

	t.Run("non-claude program is a no-op", func(t *testing.T) {
		assert.Equal(t, "aider --model gemma", modelProgram("sonnet", "aider --model gemma"))
	})
}

func TestLaunchOptionsFromConfig(t *testing.T) {
	t.Run("nil cfg returns zero value", func(t *testing.T) {
		assert.Equal(t, overlay.LaunchOptions{}, launchOptionsFromConfig(nil))
	})

	t.Run("populated cfg maps every field", func(t *testing.T) {
		got := launchOptionsFromConfig(config.DefaultConfig())
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  true,
			PermissionMode: "default",
			Model:          "default",
			HeadroomProxy:  false,
		}, got)
	})

	t.Run("threads through explicit overrides", func(t *testing.T) {
		cfg := &config.Config{
			ClaudeRemoteControl:  boolPtrTest(false),
			ClaudePermissionMode: stringPtrTest("plan"),
			ClaudeModel:          stringPtrTest("opus"),
			HeadroomProxy:        boolPtrTest(true),
		}
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  false,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomProxy:  true,
		}, launchOptionsFromConfig(cfg))
	})
}

func TestEffectiveRemoteControl(t *testing.T) {
	assert.True(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomProxy: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomProxy: true}))
}

func TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff(t *testing.T) {
	// A config.json (or a Session Launch Options selection) with both
	// RemoteControl and HeadroomProxy true must not report "blocked" for
	// a conflict the composed command doesn't actually have.
	opts := overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}
	m := &home{rcAuth: session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}}
	assert.False(t, m.remoteControlBlocked(effectiveRemoteControl(opts), "claude"))
}

func TestApplyLaunchOptions(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	t.Run("stacks remote-control, permission-mode, and model", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", HeadroomProxy: false}
		got := applyLaunchOptions(opts, authOK, "claude", "my task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
	})

	t.Run("headroom proxy never touches program", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "opus", HeadroomProxy: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits", got)
	})

	t.Run("headroom proxy forcibly disables remote control even if both are true", func(t *testing.T) {
		// applyLaunchOptions calls remoteControlProgram with
		// effectiveRemoteControl(opts), not raw opts.RemoteControl — this
		// is the authoritative enforcement of the RC/HeadroomProxy
		// exclusivity rule (see TestEffectiveRemoteControl), not just a
		// UI-level nicety.
		opts := overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})

	t.Run("all defaults/disabled is a no-op", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "default"}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})
}
