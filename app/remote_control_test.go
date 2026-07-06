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

func TestHeadroomWrapProgram(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		assert.Equal(t, "claude --model sonnet", headroomWrapProgram(false, "claude --model sonnet"))
	})

	t.Run("enabled wraps the whole command", func(t *testing.T) {
		assert.Equal(t, "headroom wrap claude --model sonnet", headroomWrapProgram(true, "claude --model sonnet"))
	})

	t.Run("agent-agnostic: wraps non-claude programs too", func(t *testing.T) {
		assert.Equal(t, "headroom wrap aider --model gemma", headroomWrapProgram(true, "aider --model gemma"))
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
			HeadroomWrap:   false,
			Effort:         "default",
		}, got)
	})

	t.Run("threads through explicit overrides", func(t *testing.T) {
		cfg := &config.Config{
			ClaudeRemoteControl:  boolPtrTest(false),
			ClaudePermissionMode: stringPtrTest("plan"),
			ClaudeModel:          stringPtrTest("opus"),
			HeadroomWrap:         boolPtrTest(true),
			ClaudeEffort:         stringPtrTest("high"),
		}
		assert.Equal(t, overlay.LaunchOptions{
			RemoteControl:  false,
			PermissionMode: "plan",
			Model:          "opus",
			HeadroomWrap:   true,
			Effort:         "high",
		}, launchOptionsFromConfig(cfg))
	})
}

func TestEffectiveRemoteControl(t *testing.T) {
	assert.True(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomWrap: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomWrap: false}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: true, HeadroomWrap: true}))
	assert.False(t, effectiveRemoteControl(overlay.LaunchOptions{RemoteControl: false, HeadroomWrap: true}))
}

func TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomWrapForcesRCOff(t *testing.T) {
	// Reproduces the bug found in final review: a config.json (or a
	// Session Launch Options selection) with both RemoteControl and
	// HeadroomWrap true must not report "blocked" for a conflict the
	// composed command doesn't actually have.
	opts := overlay.LaunchOptions{RemoteControl: true, HeadroomWrap: true}
	m := &home{rcAuth: session.RemoteControlAuth{State: session.RemoteControlAuthBlocked}}
	assert.False(t, m.remoteControlBlocked(effectiveRemoteControl(opts), "claude"))
}

func TestEffortProgram(t *testing.T) {
	assert.Equal(t, "claude --effort high", effortProgram("high", "claude"))
	assert.Equal(t, "claude", effortProgram("default", "claude"))
	assert.Equal(t, "claude", effortProgram("", "claude"))
}

func TestApplyLaunchOptions_ComposesEffort(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	opts := overlay.LaunchOptions{Model: "opus", Effort: "high"}
	got := applyLaunchOptions(opts, authOK, "claude", "t")
	assert.Contains(t, got, "--effort high")
	assert.Contains(t, got, "--model opus")
}

func TestParseLaunchOptions_RoundTrip(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	cases := []struct {
		name string
		opts overlay.LaunchOptions
	}{
		{"all default", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default"}},
		{"remote control on", overlay.LaunchOptions{RemoteControl: true, PermissionMode: "default", Model: "default", Effort: "default"}},
		{"permission mode", overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "default", Effort: "default"}},
		{"model", overlay.LaunchOptions{PermissionMode: "default", Model: "opus", Effort: "default"}},
		{"effort", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "high"}},
		{"headroom wrap", overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default", HeadroomWrap: true}},
		{"all on (RC forced off by exclusivity)", overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", Effort: "high", HeadroomWrap: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			composed := applyLaunchOptions(tc.opts, authOK, "claude", "my-title")
			gotOpts, gotBase := ParseLaunchOptions(composed)
			assert.Equal(t, "claude", gotBase)
			// effectiveRemoteControl forces RemoteControl off when
			// HeadroomWrap is on, so the round-trip must reflect the
			// composed reality, not the original (possibly
			// self-contradictory) input.
			wantRC := tc.opts.RemoteControl && !tc.opts.HeadroomWrap
			assert.Equal(t, wantRC, gotOpts.RemoteControl)
			assert.Equal(t, tc.opts.PermissionMode, gotOpts.PermissionMode)
			assert.Equal(t, tc.opts.Model, gotOpts.Model)
			assert.Equal(t, tc.opts.Effort, gotOpts.Effort)
			assert.Equal(t, tc.opts.HeadroomWrap, gotOpts.HeadroomWrap)
		})
	}
}

func TestParseLaunchOptions_AbsolutePathBaseProgram(t *testing.T) {
	opts, base := ParseLaunchOptions("headroom wrap claude --model sonnet --permission-mode auto")
	assert.Equal(t, "claude", base)
	assert.Equal(t, "sonnet", opts.Model)
	assert.Equal(t, "auto", opts.PermissionMode)
	assert.True(t, opts.HeadroomWrap)
}

func TestParseLaunchOptions_UnrecognizedFlagLeftInBase(t *testing.T) {
	opts, base := ParseLaunchOptions("claude --some-other-flag value --model opus")
	assert.Equal(t, "opus", opts.Model)
	assert.Contains(t, base, "--some-other-flag value")
	assert.NotContains(t, base, "--model")
}

func TestParseLaunchOptions_EmptyProgram(t *testing.T) {
	opts, base := ParseLaunchOptions("")
	assert.Equal(t, overlay.LaunchOptions{}, opts)
	assert.Equal(t, "", base)
}

func TestApplyLaunchOptions(t *testing.T) {
	authOK := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	t.Run("stacks remote-control, permission-mode, and model", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, PermissionMode: "acceptEdits", Model: "opus", HeadroomWrap: false}
		got := applyLaunchOptions(opts, authOK, "claude", "my task")
		assert.Equal(t, "claude --model opus --permission-mode acceptEdits --remote-control my-task", got)
	})

	t.Run("headroom wrap is applied last, outermost", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "acceptEdits", Model: "opus", HeadroomWrap: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "headroom wrap claude --model opus --permission-mode acceptEdits", got)
	})

	t.Run("headroom wrap forcibly disables remote control even if both are true", func(t *testing.T) {
		opts := overlay.LaunchOptions{RemoteControl: true, HeadroomWrap: true}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "headroom wrap claude", got)
	})

	t.Run("all defaults/disabled is a no-op", func(t *testing.T) {
		opts := overlay.LaunchOptions{PermissionMode: "default", Model: "default"}
		got := applyLaunchOptions(opts, authOK, "claude", "task")
		assert.Equal(t, "claude", got)
	})
}
