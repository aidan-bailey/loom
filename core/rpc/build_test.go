package rpc

import (
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompareBuilds: the newer side wins the handshake. One executable is
// one build; otherwise the release decides, then the commit's time, then a
// modified tree, then the protocol, then, when both name their commit, the
// executable's time; a full tie is otherwise the same build. Each rule
// both ways round.
func TestCompareBuilds(t *testing.T) {
	const (
		monday  = "2026-10-05T10:00:00Z"
		tuesday = "2026-10-06T10:00:00Z"
		// A rebuild a moment later: the times differ below the second.
		built   = "2026-10-06T10:00:00.1Z"
		rebuilt = "2026-10-06T10:00:00.2Z"
		// A Nix store's executables are dated at the epoch.
		epoch = "1970-01-01T00:00:01Z"
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
		{"an unreadable time decides nothing", Hello{Time: "yesterday"}, Hello{Time: monday, Modified: true}, ServerNewer},
		{"a tie: the higher protocol", Hello{Exe: "a", Protocol: 3}, Hello{Exe: "b", Protocol: 2}, ClientNewer},
		{"a tie: the server's higher protocol", Hello{Exe: "a", Protocol: 2}, Hello{Exe: "b", Protocol: 3}, ServerNewer},
		{"a server that sent no hello is older", Hello{Exe: "a", Protocol: 2, Version: "0.13.0", ExeTime: rebuilt}, Hello{}, ClientNewer},
		{"a tie: the client's rebuilt executable", Hello{Exe: "a", Time: monday, Modified: true, ExeTime: rebuilt}, Hello{Exe: "b", Time: monday, Modified: true, ExeTime: built}, ClientNewer},
		{"a tie: the server's rebuilt executable", Hello{Exe: "a", Time: monday, Modified: true, ExeTime: built}, Hello{Exe: "b", Time: monday, Modified: true, ExeTime: rebuilt}, ServerNewer},
		{"a tie: one clean commit rebuilt, the client's later", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: rebuilt}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: built}, ClientNewer},
		{"a tie: one clean commit rebuilt, the server's later", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: built}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: rebuilt}, ServerNewer},
		{"a tie: one release installed twice, the client's later", Hello{Exe: "a", Version: "0.13.0", ExeTime: tuesday}, Hello{Exe: "b", Version: "0.13.0", ExeTime: monday}, SameBuild},
		{"a tie: one release installed twice, the server's later", Hello{Exe: "a", Version: "0.13.0", ExeTime: monday}, Hello{Exe: "b", Version: "0.13.0", ExeTime: tuesday}, SameBuild},
		{"a tie: a Nix build of a release's daemon", Hello{Exe: "a", Version: "0.13.0", ExeTime: epoch}, Hello{Exe: "b", Version: "0.13.0", ExeTime: tuesday}, SameBuild},
		// A Nix build names its commit (the flake stamps it), and its
		// executable is dated at the epoch, which tells nothing.
		{"a tie: a Nix build of a release's commit", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: epoch}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: tuesday}, SameBuild},
		{"a tie: a release's daemon, a Nix build of its commit", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: tuesday}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: epoch}, SameBuild},
		{"a tie: the epoch itself tells nothing", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: "1970-01-01T00:00:00Z"}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: tuesday}, SameBuild},
		{"two Nix builds: the later commit", Hello{Exe: "a", Version: "0.13.0", Time: tuesday, ExeTime: epoch}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: epoch}, ClientNewer},
		{"two Nix builds: the server's later commit", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: epoch}, Hello{Exe: "b", Version: "0.13.0", Time: tuesday, ExeTime: epoch}, ServerNewer},
		{"two Nix builds of one commit", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: epoch}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: epoch}, SameBuild},
		{"a tie: only the client names its commit", Hello{Exe: "a", Version: "0.13.0", Time: monday, ExeTime: tuesday}, Hello{Exe: "b", Version: "0.13.0", ExeTime: monday}, SameBuild},
		{"a tie: only the server names its commit", Hello{Exe: "a", Version: "0.13.0", ExeTime: tuesday}, Hello{Exe: "b", Version: "0.13.0", Time: monday, ExeTime: monday}, SameBuild},
		{"a tie: executables of one time", Hello{Exe: "a", Version: "0.13.0", ExeTime: monday}, Hello{Exe: "b", Version: "0.13.0", ExeTime: monday}, SameBuild},
		{"a tie: the client's executable time unknown", Hello{Exe: "a", Version: "0.13.0"}, Hello{Exe: "b", Version: "0.13.0", ExeTime: monday}, SameBuild},
		{"a tie: the server's executable time unknown", Hello{Exe: "a", Version: "0.13.0", ExeTime: monday}, Hello{Exe: "b", Version: "0.13.0"}, SameBuild},
		{"a tie: the client's executable unreadable", Hello{Version: "0.13.0"}, Hello{Exe: "b", Version: "0.13.0", ExeTime: monday}, SameBuild},
		{"a tie: the server's executable unreadable", Hello{Exe: "a", Version: "0.13.0", ExeTime: monday}, Hello{Version: "0.13.0"}, SameBuild},
		{"neither executable readable", Hello{Version: "0.13.0"}, Hello{Version: "0.13.0"}, SameBuild},
		{"nothing known", Hello{}, Hello{}, SameBuild},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CompareBuilds(tc.client, tc.server))
		})
	}
}

// TestCommitTime: a hello names Go's vcs.time when Go stamped one, else
// the commit time the build stamped (commitUnix, as the Nix flake does),
// when that is a positive number of seconds.
func TestCommitTime(t *testing.T) {
	for _, tc := range []struct {
		name, vcsTime, stamp, want string
	}{
		{"Go's vcs.time", "2026-10-05T10:00:00Z", "", "2026-10-05T10:00:00Z"},
		{"Go's vcs.time outranks a stamp", "2026-10-05T10:00:00Z", "1", "2026-10-05T10:00:00Z"},
		{"a stamp, in UTC", "", "1759658400", "2025-10-05T10:00:00Z"},
		{"no stamp", "", "", ""},
		{"a zero stamp (no lastModified)", "", "0", ""},
		{"a negative stamp", "", "-5", ""},
		{"a stamp that is no number", "", "monday", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, commitTime(tc.vcsTime, tc.stamp))
		})
	}
}

// TestSelf_NamesTheStampedCommitTime: a build with no VCS information
// (a Nix build; a test binary too) names the commit time it was stamped
// with.
func TestSelf_NamesTheStampedCommitTime(t *testing.T) {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.time" {
				t.Skip("this binary names its commit through Go's VCS stamp")
			}
		}
	}
	prev := commitUnix
	t.Cleanup(func() { commitUnix = prev })

	commitUnix = "1759658400"
	assert.Equal(t, "2025-10-05T10:00:00Z", readIdentity().Time)
	commitUnix = ""
	assert.Empty(t, readIdentity().Time, "nothing stamped, nothing named")
}

// TestSelf: this binary's hello names its protocol, build, release and
// executable, with the executable's time.
func TestSelf(t *testing.T) {
	prev := version
	t.Cleanup(func() { version = prev })
	SetVersion("0.13.0")
	h := Self()
	assert.Equal(t, Protocol, h.Protocol)
	assert.Equal(t, build(), h.Build)
	assert.Equal(t, "0.13.0", h.Version)
	require.Len(t, h.Exe, 64, "a SHA-256 in hex")
	exeTime, err := time.Parse(time.RFC3339Nano, h.ExeTime)
	require.NoError(t, err, "the executable's time, RFC 3339")
	assert.Equal(t, time.UTC, exeTime.Location())
	assert.False(t, exeTime.After(time.Now()), "the time it was built, before it ran")
	assert.Empty(t, h.Tmux, "a server names its tmux server itself")
}
