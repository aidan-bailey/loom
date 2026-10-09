package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompareBuilds: the newer side wins the handshake. One executable is
// one build; otherwise the release decides, then the commit's time, then a
// modified tree; a tie goes to the client, which was just started, so an
// edited rebuild at one commit replaces its server.
func TestCompareBuilds(t *testing.T) {
	const (
		monday  = "2026-10-05T10:00:00Z"
		tuesday = "2026-10-06T10:00:00Z"
	)
	for _, tc := range []struct {
		name           string
		client, server Hello
		want           Order
	}{
		{"one executable", Hello{Exe: "a", Version: "0.13.0"}, Hello{Exe: "a", Version: "0.14.0"}, SameBuild},
		{"a higher release", Hello{Exe: "a", Version: "0.14.0"}, Hello{Exe: "b", Version: "0.13.9"}, ClientNewer},
		{"a lower release", Hello{Exe: "a", Version: "0.13.0"}, Hello{Exe: "b", Version: "1.0.0"}, ServerNewer},
		{"releases compare as numbers", Hello{Version: "0.10.0"}, Hello{Version: "0.9.0"}, ClientNewer},
		{"a v prefix is the same release", Hello{Version: "v0.13.0", Time: monday}, Hello{Version: "0.13.0", Time: tuesday}, ServerNewer},
		{"a release outranks its pre-release", Hello{Version: "1.0.0"}, Hello{Version: "1.0.0-rc.1"}, ClientNewer},
		{"a pre-release is below its release", Hello{Version: "1.0.0-rc.1"}, Hello{Version: "1.0.0"}, ServerNewer},
		{"pre-releases compare by identifier", Hello{Version: "1.0.0-rc.10"}, Hello{Version: "1.0.0-rc.9"}, ClientNewer},
		{"build metadata orders nothing", Hello{Version: "1.0.0+a", Time: monday}, Hello{Version: "1.0.0+b", Time: tuesday}, ServerNewer},
		{"one release: the later commit", Hello{Version: "0.13.0", Time: tuesday}, Hello{Version: "0.13.0", Time: monday}, ClientNewer},
		{"one release: the earlier commit", Hello{Version: "0.13.0", Time: monday}, Hello{Version: "0.13.0", Time: tuesday}, ServerNewer},
		{"no release: the commit decides", Hello{Time: monday}, Hello{Version: "0.13.0", Time: tuesday}, ServerNewer},
		{"one commit: a modified tree", Hello{Time: monday, Modified: true}, Hello{Time: monday}, ClientNewer},
		{"one commit: the server's modified", Hello{Time: monday}, Hello{Time: monday, Modified: true}, ServerNewer},
		{"a tie: the client", Hello{Exe: "a", Time: monday, Modified: true}, Hello{Exe: "b", Time: monday, Modified: true}, ClientNewer},
		{"nothing known: the client", Hello{}, Hello{}, ClientNewer},
		{"an unreadable time decides nothing", Hello{Time: "yesterday"}, Hello{Time: monday, Modified: true}, ServerNewer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CompareBuilds(tc.client, tc.server))
		})
	}
}

// TestSelf: this binary's hello names its protocol, build, release and
// executable.
func TestSelf(t *testing.T) {
	prev := version
	t.Cleanup(func() { version = prev })
	SetVersion("0.13.0")
	h := Self()
	assert.Equal(t, Protocol, h.Protocol)
	assert.Equal(t, build(), h.Build)
	assert.Equal(t, "0.13.0", h.Version)
	require.Len(t, h.Exe, 64, "a SHA-256 in hex")
	assert.Empty(t, h.Tmux, "a server names its tmux server itself")
}
