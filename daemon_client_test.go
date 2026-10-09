package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemons is a daemonLink over scripted daemons: each dial reaches the
// current one, whose hello is builds[0] and whose pid is fakePID plus the
// stops so far, until a stop replaces it with the next. It records the
// dials and stops, and the pids the stops named.
type fakeDaemons struct {
	t       *testing.T
	builds  []rpc.Hello
	dials   int
	stops   int
	stopped []int
	clients []*rpc.Client
	// guarded lists the tmux servers mayReplace was asked about.
	guarded []string
	stderr  bytes.Buffer
}

// fakePID is the first scripted daemon's pid.
const fakePID = 4000

func (f *fakeDaemons) link(own rpc.Hello, refusal error) daemonLink {
	return daemonLink{
		own: own,
		dial: func() (*rpc.Client, rpc.Hello, int, error) {
			f.dials++
			c, _, stop, err := rpc.InProcessForTest(core.NewForTest(core.Options{}))
			require.NoError(f.t, err)
			f.t.Cleanup(stop)
			f.clients = append(f.clients, c)
			return c, f.builds[0], fakePID + f.stops, nil
		},
		stop: func(pid int) error {
			f.stops++
			f.stopped = append(f.stopped, pid)
			f.builds = f.builds[1:]
			return nil
		},
		mayReplace: func(daemonTmux string) error {
			f.guarded = append(f.guarded, daemonTmux)
			return refusal
		},
		stderr: &f.stderr,
	}
}

// TestJoin_TheNewerSideWins: the same build is used as it is; a newer
// daemon is refused (this loom must be upgraded); an older one is stopped
// and replaced with this build, but never from inside a loom session, where
// a loom under development would replace the user's daemon.
func TestJoin_TheNewerSideWins(t *testing.T) {
	ours := rpc.Hello{Exe: "ours", Version: "0.13.0", Build: "v0.13.0 abc", Tmux: "/srv/ours.sock"}
	older := rpc.Hello{Exe: "older", Version: "0.12.0", Build: "v0.12.0 def", Tmux: "/srv/older.sock"}
	newer := rpc.Hello{Exe: "newer", Version: "0.14.0", Build: "v0.14.0 123"}

	t.Run("the same build", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{ours}}
		c, err := f.link(ours, nil).join()
		require.NoError(t, err)
		assert.Same(t, f.clients[0], c)
		assert.Zero(t, f.stops)
		assert.Empty(t, f.stderr.String())
	})

	t.Run("an older daemon is replaced", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{older, ours}}
		link := f.link(ours, nil)
		stop := link.stop
		link.stop = func(pid int) error {
			assert.Equal(t, "loom: replacing the loom daemon (0.12.0 (v0.12.0 def)) with this build…\n", f.stderr.String(),
				"said before the stop, which can take a while")
			return stop(pid)
		}
		c, err := link.join()
		require.NoError(t, err)
		assert.Equal(t, 1, f.stops)
		assert.Equal(t, []int{fakePID}, f.stopped, "stopped the daemon it dialed and compared, by its pid")
		assert.Equal(t, 2, f.dials, "dialed again: Connect starts this build")
		assert.Same(t, f.clients[1], c)
		assert.Error(t, f.clients[0].FlushForTest(), "the old daemon's client was closed")
		assert.Equal(t, []string{"/srv/older.sock"}, f.guarded, "asked about the old daemon's tmux server")
	})

	t.Run("a newer daemon is refused", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{newer}}
		_, err := f.link(ours, nil).join()
		require.Error(t, err)
		assert.Equal(t, "the loom daemon is 0.14.0 (v0.14.0 123), newer than this loom (0.13.0 (v0.13.0 abc)): upgrade loom, or run `loom serve stop`", err.Error())
		assert.Zero(t, f.stops)
	})

	t.Run("inside loom, an older daemon is kept", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{older, ours}}
		refusal := errors.New("this loom runs in loom's tmux session loom_agent, on the daemon's tmux server")
		_, err := f.link(ours, refusal).join()
		require.Error(t, err)
		assert.ErrorIs(t, err, refusal)
		assert.Equal(t, "the loom daemon is 0.12.0 (v0.12.0 def), older than this loom (0.13.0 (v0.13.0 abc)), and "+
			"this loom runs in loom's tmux session loom_agent, on the daemon's tmux server: a loom inside loom never "+
			"replaces the daemon; run the new loom outside loom, or stop the daemon deliberately first (`loom serve stop`)", err.Error())
		assert.NotContains(t, err.Error(), tmux.EnvTmuxSocket, "another tmux server changes nothing")
		assert.Zero(t, f.stops, "the user's daemon is never stopped")
		assert.Empty(t, f.stderr.String(), "nothing replaced")
	})

	t.Run("a daemon that stays older after its replacement", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{older, older}}
		_, err := f.link(ours, nil).join()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "even after replacing it")
		assert.Equal(t, 1, f.stops, "replaced once, not in a loop")
	})
}

// TestJoin_AnotherProtocol: a daemon of another protocol can't be talked
// to, but its hello still tells its build: an older one is replaced, and
// one from before servers said hello first counts as older.
func TestJoin_AnotherProtocol(t *testing.T) {
	ours := rpc.Hello{Protocol: rpc.Protocol, Exe: "ours", Version: "0.13.0"}
	for _, tc := range []struct {
		name string
		peer *rpc.Hello
	}{
		{"an older daemon", &rpc.Hello{Protocol: rpc.Protocol - 1, Exe: "older", Version: "0.12.0"}},
		{"a daemon that sent no hello", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dials, stops := 0, 0
			var replacement *rpc.Client
			link := daemonLink{
				own: ours,
				dial: func() (*rpc.Client, rpc.Hello, int, error) {
					dials++
					if stops == 0 {
						var peer rpc.Hello
						if tc.peer != nil {
							peer = *tc.peer
						}
						return nil, peer, fakePID, &rpc.MismatchError{Peer: tc.peer}
					}
					c, _, stop, err := rpc.InProcessForTest(core.NewForTest(core.Options{}))
					require.NoError(t, err)
					t.Cleanup(stop)
					replacement = c
					return c, ours, fakePID + 1, nil
				},
				stop: func(pid int) error {
					assert.Equal(t, fakePID, pid, "the daemon of another protocol it dialed")
					stops++
					return nil
				},
				mayReplace: func(string) error { return nil },
				stderr:     io.Discard,
			}
			c, err := link.join()
			require.NoError(t, err)
			assert.Same(t, replacement, c)
			assert.Equal(t, 1, stops)
		})
	}

	t.Run("any other dial error is the join's", func(t *testing.T) {
		boom := errors.New("the loom daemon did not start")
		link := daemonLink{
			own:        ours,
			dial:       func() (*rpc.Client, rpc.Hello, int, error) { return nil, rpc.Hello{}, 0, boom },
			stop:       func(int) error { t.Error("stopped"); return nil },
			mayReplace: func(string) error { return nil },
			stderr:     io.Discard,
		}
		_, err := link.join()
		assert.ErrorIs(t, err, boom)
	})
}

// TestJoin_PinsTheDaemonsTmuxServer: once joined, every tmux command this
// process runs goes to the daemon's server, whatever its environment
// says: the daemon's sessions are there.
func TestJoin_PinsTheDaemonsTmuxServer(t *testing.T) {
	t.Setenv(tmux.EnvTmuxSocket, "elsewhere")
	t.Cleanup(func() { tmux.UseServer("") })
	ours := rpc.Hello{Exe: "ours", Version: "0.13.0", Tmux: "/srv/daemon.sock"}
	f := &fakeDaemons{t: t, builds: []rpc.Hello{ours}}
	_, err := f.link(ours, nil).join()
	require.NoError(t, err)
	args := tmux.Command(context.Background(), "list-sessions").Args
	assert.True(t, slices.Equal([]string{"tmux", "-S", "/srv/daemon.sock", "list-sessions"}, args), "got %q", args)
}

// TestReplaceGuard: a loom inside loom never replaces the daemon its
// session runs on, whatever server its own environment names; a sandbox
// replaces its own daemon, on the sandbox's server.
func TestReplaceGuard(t *testing.T) {
	const (
		users   = "/tmp/tmux-1000/default"
		sandbox = "/tmp/tmux-1000/loomdev-demo"
	)
	// A socket reached through a symlinked directory: tmux resolves its
	// socket dir, a daemon may not have.
	real := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(real, "default"), nil, 0o600))
	link := filepath.Join(t.TempDir(), "tmp")
	require.NoError(t, os.Symlink(real, link))

	inSession := func(name string) func() (string, error) {
		return func() (string, error) { return name, nil }
	}
	noAnswer := func() (string, error) { return "", errors.New("tmux display-message: timed out") }
	for _, tc := range []struct {
		name       string
		env        map[string]string
		enclosing  func() (string, error)
		daemonTmux string
		refuses    string
	}{
		{name: "outside tmux", daemonTmux: users,
			enclosing: func() (string, error) { t.Error("asked for no session"); return "", nil }},
		{name: "in an agent's pane, on the daemon's server", env: map[string]string{"TMUX": users + ",1234,0"},
			enclosing: inSession("loom_agent"), daemonTmux: users,
			refuses: "this loom runs in loom's tmux session loom_agent, on the daemon's tmux server"},
		{name: "in an agent's pane, another tmux server named", env: map[string]string{"TMUX": users + ",1234,0", tmux.EnvTmuxSocket: "x"},
			enclosing: inSession("loom_agent"), daemonTmux: users,
			refuses: "this loom runs in loom's tmux session loom_agent, on the daemon's tmux server"},
		{name: "in an agent's pane, another global dir named", env: map[string]string{"TMUX": users + ",1234,0", config.EnvGlobalDir: "/elsewhere"},
			enclosing: inSession("loom_agent"), daemonTmux: users,
			refuses: "this loom runs in loom's tmux session loom_agent, on the daemon's tmux server"},
		{name: "in a claude-squad session", env: map[string]string{"TMUX": users + ",1234,0"},
			enclosing: inSession("claudesquad_agent"), daemonTmux: users,
			refuses: "this loom runs in loom's tmux session claudesquad_agent, on the daemon's tmux server"},
		{name: "the daemon's server spelled otherwise", env: map[string]string{"TMUX": "/tmp//tmux-1000/./default,1234,0"},
			enclosing: inSession("loom_agent"), daemonTmux: users,
			refuses: "on the daemon's tmux server"},
		{name: "the daemon's server through a symlink", env: map[string]string{"TMUX": filepath.Join(real, "default") + ",1234,0"},
			enclosing: inSession("loom_agent"), daemonTmux: filepath.Join(link, "default"),
			refuses: "on the daemon's tmux server"},
		{name: "a session it can't name, on the daemon's server", env: map[string]string{"TMUX": users + ",1234,0"},
			enclosing: noAnswer, daemonTmux: users,
			refuses: "this loom runs inside tmux on the daemon's tmux server, in a session it could not name (tmux display-message: timed out)"},
		{name: "a session of the user's own, on the daemon's server", env: map[string]string{"TMUX": users + ",1234,0"},
			enclosing: inSession("work"), daemonTmux: users},
		{name: "a sandbox: its daemon on its own server", env: map[string]string{"TMUX": users + ",1234,0", tmux.EnvTmuxSocket: "loomdev-demo", config.EnvGlobalDir: "/sandbox/global"},
			enclosing: inSession("loom_agent"), daemonTmux: sandbox},
		{name: "a daemon that named no server", env: map[string]string{"TMUX": users + ",1234,0"},
			enclosing: inSession("loom_agent"),
			refuses:   "this loom runs in loom's tmux session loom_agent, and the daemon did not name its tmux server"},
		{name: "a sandbox's daemon that named no server", env: map[string]string{"TMUX": users + ",1234,0", config.EnvGlobalDir: "/sandbox/global"},
			enclosing: inSession("loom_agent")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			err := replaceGuard(getenv, tc.enclosing, tc.daemonTmux)
			if tc.refuses == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.refuses)
		})
	}
}

// TestHandshake_IsBounded: a daemon that accepts the connection but never
// says hello fails the handshake when its bound passes, rather than hang
// loom; once the client is up, the bound is lifted, or it would end the
// connection.
func TestHandshake_IsBounded(t *testing.T) {
	t.Run("a daemon that never answers", func(t *testing.T) {
		ours, theirs := net.Pipe()
		t.Cleanup(func() { theirs.Close() })
		go func() { _, _ = io.Copy(io.Discard, theirs) }() // reads the hello, answers nothing
		done := make(chan error, 1)
		go func() {
			_, err := handshake(ours, 50*time.Millisecond)
			done <- err
		}()
		select {
		case err := <-done:
			assert.Error(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the handshake outlived its bound")
		}
	})

	t.Run("a daemon that answers", func(t *testing.T) {
		loop := core.StartForTest(core.NewForTest(core.Options{}))
		srv := rpc.NewServer(loop)
		t.Cleanup(func() { srv.Close(); loop.Stop() })
		ours, theirs := net.Pipe()
		srv.Serve(theirs)
		c, err := handshake(ours, 50*time.Millisecond)
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		time.Sleep(200 * time.Millisecond)
		assert.NoError(t, c.FlushForTest(), "the connection outlives the handshake's bound")
	})
}
