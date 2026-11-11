package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execCall is one call of the exec seam.
type execCall struct {
	path       string
	argv, env  []string
	calledWith bool
}

// fakeExec replaces execProcess for the test, recording the call, and
// returns what it recorded.
func fakeExec(t *testing.T) *execCall {
	t.Helper()
	call := &execCall{}
	prev := execProcess
	t.Cleanup(func() { execProcess = prev })
	execProcess = func(path string, argv, env []string) error {
		*call = execCall{path: path, argv: argv, env: env, calledWith: true}
		return nil
	}
	return call
}

// daemonBuild writes a file standing for a daemon's executable, with the
// given mode, and returns its hello: its hash and absolute path.
func daemonBuild(t *testing.T, mode os.FileMode) rpc.Hello {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loom")
	content := []byte("#!/bin/sh\n# a newer loom\n")
	require.NoError(t, os.WriteFile(path, content, mode))
	require.NoError(t, os.Chmod(path, mode))
	sum := sha256.Sum256(content)
	return rpc.Hello{Version: "0.15.0", Build: "v0.15.0 abc1234", Exe: hex.EncodeToString(sum[:]), ExePath: path}
}

// TestReexecInto_ExecsTheDaemonsBuild: a newer daemon whose path names a
// file that still hashes to its build is executed in place of this
// process, with this process's arguments and the marker naming the build,
// said first.
func TestReexecInto_ExecsTheDaemonsBuild(t *testing.T) {
	markReexec(t, "")
	call := fakeExec(t)
	peer := daemonBuild(t, 0o755)
	var said []string
	err := reexecInto(peer, "0.15.0 (v0.15.0 abc1234)", time.Time{}, func(line string) { said = append(said, line) })
	require.NoError(t, err, "the seam's exec returned no error")
	require.True(t, call.calledWith, "it executed the daemon's build")
	assert.Equal(t, peer.ExePath, call.path)
	assert.Equal(t, append([]string{peer.ExePath}, os.Args[1:]...), call.argv, "the same arguments")
	assert.Contains(t, call.env, envReexec+"="+peer.Exe, "the marker names the build it executed")
	assert.Equal(t, 1, countPrefix(call.env, envReexec+"="), "one marker")
	assert.Zero(t, countPrefix(call.env, envReexecDeadline+"="), "a command with no deadline carries none")
	assert.Equal(t, []string{"the loom daemon was upgraded to 0.15.0 (v0.15.0 abc1234); carrying on under it (" + peer.ExePath + ")"}, said)
}

// TestReexecInto_AnEarlierReexecIntoAnotherBuildAllowsThisOne: the marker
// stops a loop into one build, not a later upgrade to another.
func TestReexecInto_AnEarlierReexecIntoAnotherBuildAllowsThisOne(t *testing.T) {
	markReexec(t, "an-older-daemons-hash")
	call := fakeExec(t)
	peer := daemonBuild(t, 0o755)
	require.NoError(t, reexecInto(peer, "0.15.0", time.Time{}, func(string) {}))
	require.True(t, call.calledWith)
	assert.Contains(t, call.env, envReexec+"="+peer.Exe)
	assert.Equal(t, 1, countPrefix(call.env, envReexec+"="), "the old marker is replaced")
}

// TestReexecInto_Refusals: each condition the spec names refuses the
// re-exec, with its reason, and never executes anything.
func TestReexecInto_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		peer   func(t *testing.T) rpc.Hello
		marker func(peer rpc.Hello) string
		noExec bool // the platform can't exec
		want   string
	}{
		{name: "a daemon that names no path", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.ExePath = ""
			return h
		}, want: "names no executable"},
		{name: "a relative path", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.ExePath = "bin/loom"
			return h
		}, want: "not absolute"},
		{name: "a missing file", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.ExePath = filepath.Join(t.TempDir(), "gone")
			return h
		}, want: "no such file"},
		{name: "a directory", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.ExePath = t.TempDir()
			return h
		}, want: "not a regular file"},
		{name: "a file this user can't execute", peer: func(t *testing.T) rpc.Hello {
			return daemonBuild(t, 0o644)
		}, want: "can't execute"},
		{name: "a file installed over the daemon's build since", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.Exe = "0000000000000000000000000000000000000000000000000000000000000000"
			return h
		}, want: "is no longer the daemon's build"},
		{name: "a daemon that names no hash", peer: func(t *testing.T) rpc.Hello {
			h := daemonBuild(t, 0o755)
			h.Exe = ""
			return h
		}, want: "is no longer the daemon's build"},
		{name: "a process that already re-executed into this build", peer: func(t *testing.T) rpc.Hello {
			return daemonBuild(t, 0o755)
		}, marker: func(peer rpc.Hello) string { return peer.Exe }, want: "already carried on under this build"},
		{name: "a platform that can't exec", peer: func(t *testing.T) rpc.Hello {
			return daemonBuild(t, 0o755)
		}, noExec: true, want: "can't re-execute on this platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := tc.peer(t)
			marker := ""
			if tc.marker != nil {
				marker = tc.marker(peer)
			}
			markReexec(t, marker)
			if tc.noExec {
				prev := reexecPlatform
				t.Cleanup(func() { reexecPlatform = prev })
				reexecPlatform = false
			}
			call := fakeExec(t)
			var said []string
			err := reexecInto(peer, "0.15.0", time.Time{}, func(line string) { said = append(said, line) })
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.False(t, call.calledWith, "nothing was executed")
			assert.Empty(t, said, "nothing said of carrying on")
		})
	}
}

// TestReexecInto_AFailedExecIsItsError: an exec the system refuses
// returns, and its error is the reason.
func TestReexecInto_AFailedExecIsItsError(t *testing.T) {
	markReexec(t, "")
	prev := execProcess
	t.Cleanup(func() { execProcess = prev })
	refused := errors.New("exec format error")
	execProcess = func(string, []string, []string) error { return refused }
	err := reexecInto(daemonBuild(t, 0o755), "0.15.0", time.Time{}, func(string) {})
	assert.ErrorIs(t, err, refused)
}

// countPrefix is how many of env's entries start with prefix.
func countPrefix(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

// markReexec sets the marker this process took from its environment at
// startup, as a process that re-executed into build would have it.
func markReexec(t *testing.T, build string) {
	t.Helper()
	prev := reexecMarker
	t.Cleanup(func() { reexecMarker = prev })
	reexecMarker = build
}

// inheritDeadline sets the deadline this process took from its
// environment at startup.
func inheritDeadline(t *testing.T, d time.Time) {
	t.Helper()
	prev := reexecDeadline
	t.Cleanup(func() { reexecDeadline = prev })
	reexecDeadline = d
}

// TestReexecInto_CarriesTheDeadline: a bounded wait's deadline crosses the
// exec, so the new process stops where the old one would have.
func TestReexecInto_CarriesTheDeadline(t *testing.T) {
	markReexec(t, "")
	call := fakeExec(t)
	deadline := time.Now().Add(20 * time.Second)
	require.NoError(t, reexecInto(daemonBuild(t, 0o755), "0.15.0", deadline, func(string) {}))
	require.True(t, call.calledWith)
	assert.Contains(t, call.env, envReexecDeadline+"="+strconv.FormatInt(deadline.UnixNano(), 10))
	assert.Equal(t, 1, countPrefix(call.env, envReexecDeadline+"="))
}

// TestTakeReexecEnv: the marker and the deadline are read once and
// removed from the environment, so no job or agent this process starts
// inherits them; a malformed deadline is ignored, a past one kept.
func TestTakeReexecEnv(t *testing.T) {
	past := time.Now().Add(-time.Minute).Round(0)
	soon := time.Now().Add(time.Minute).Round(0)
	cases := []struct {
		name, marker, deadline string
		want                   time.Time
	}{
		{name: "neither", want: time.Time{}},
		{name: "a deadline to come", marker: "abc", deadline: strconv.FormatInt(soon.UnixNano(), 10), want: soon},
		{name: "a past deadline", marker: "abc", deadline: strconv.FormatInt(past.UnixNano(), 10), want: past},
		{name: "a malformed deadline", marker: "abc", deadline: "tomorrow", want: time.Time{}},
		{name: "a deadline of no time", marker: "abc", deadline: "0", want: time.Time{}},
		{name: "a negative deadline", marker: "abc", deadline: "-5", want: time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envReexec, tc.marker)
			t.Setenv(envReexecDeadline, tc.deadline)
			marker, deadline := takeReexecEnv()
			assert.Equal(t, tc.marker, marker)
			assert.True(t, tc.want.Equal(deadline), "want %v, got %v", tc.want, deadline)
			_, set := os.LookupEnv(envReexec)
			assert.False(t, set, "the marker is gone from the environment")
			_, set = os.LookupEnv(envReexecDeadline)
			assert.False(t, set, "the deadline is gone from the environment")
		})
	}
}

// TestWaitDeadline: a bounded wait stops at now+bound, or at the deadline
// it inherited across a re-exec when that comes first, past or not.
func TestWaitDeadline(t *testing.T) {
	const bound = 170 * time.Second
	t.Run("none inherited", func(t *testing.T) {
		inheritDeadline(t, time.Time{})
		got := waitDeadline(bound)
		assert.WithinDuration(t, time.Now().Add(bound), got, time.Second)
	})
	t.Run("an earlier one inherited", func(t *testing.T) {
		d := time.Now().Add(10 * time.Second)
		inheritDeadline(t, d)
		assert.Equal(t, d, waitDeadline(bound))
	})
	t.Run("a later one inherited", func(t *testing.T) {
		inheritDeadline(t, time.Now().Add(time.Hour))
		assert.WithinDuration(t, time.Now().Add(bound), waitDeadline(bound), time.Second, "the bound still caps it")
	})
	t.Run("a past one inherited", func(t *testing.T) {
		d := time.Now().Add(-time.Second)
		inheritDeadline(t, d)
		assert.Equal(t, d, waitDeadline(bound), "the wait ends at once with its ordinary timeout")
	})
}
