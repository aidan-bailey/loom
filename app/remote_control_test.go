package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
)

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
			m := &home{core: testLoop(t, core.NewForTest(core.Options{}))}
			loopOf(m).SetRCAuthForTest(tc.auth)
			assert.Equal(t, tc.want, m.remoteControlBlockedOn("", tc.rcEnabled, tc.program))
		})
	}
}

func TestRemoteControlBlockedAgreesWithComposedCommandWhenHeadroomProxyForcesRCOff(t *testing.T) {
	// A config.json (or a Session Launch Options selection) with both
	// RemoteControl and HeadroomProxy true must not report "blocked" for
	// a conflict the composed command doesn't actually have.
	opts := overlay.LaunchOptions{RemoteControl: true, HeadroomProxy: true}
	m := &home{core: testLoop(t, core.NewForTest(core.Options{}))}
	loopOf(m).SetRCAuthForTest(session.RemoteControlAuth{State: session.RemoteControlAuthBlocked})
	assert.False(t, m.remoteControlBlockedOn("", launch.EffectiveRemoteControl(opts), "claude"))
}
