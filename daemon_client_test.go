package main

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemons is a daemonLink over scripted daemons: each dial reaches the
// current one, whose hello is builds[0] until a stop replaces it with the
// next. It records the dials and stops.
type fakeDaemons struct {
	t       *testing.T
	builds  []rpc.Hello
	dials   int
	stops   int
	clients []*rpc.Client
}

func (f *fakeDaemons) link(own rpc.Hello, nested error) daemonLink {
	return daemonLink{
		own: own,
		dial: func() (*rpc.Client, rpc.Hello, error) {
			f.dials++
			c, _, stop, err := rpc.InProcessForTest(core.NewForTest(core.Options{}))
			require.NoError(f.t, err)
			f.t.Cleanup(stop)
			f.clients = append(f.clients, c)
			return c, f.builds[0], nil
		},
		stop: func() error {
			f.stops++
			f.builds = f.builds[1:]
			return nil
		},
		nested: func() error { return nested },
	}
}

// TestJoin_TheNewerSideWins: the same build is used as it is; a newer
// daemon is refused (this loom must be upgraded); an older one is stopped
// and replaced with this build, but never from inside a loom session, where
// a loom under development would replace the user's daemon.
func TestJoin_TheNewerSideWins(t *testing.T) {
	ours := rpc.Hello{Exe: "ours", Version: "0.13.0", Build: "v0.13.0 abc"}
	older := rpc.Hello{Exe: "older", Version: "0.12.0", Build: "v0.12.0 def"}
	newer := rpc.Hello{Exe: "newer", Version: "0.14.0", Build: "v0.14.0 123"}

	t.Run("the same build", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{ours}}
		c, err := f.link(ours, nil).join()
		require.NoError(t, err)
		assert.Same(t, f.clients[0], c)
		assert.Zero(t, f.stops)
	})

	t.Run("an older daemon is replaced", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{older, ours}}
		c, err := f.link(ours, nil).join()
		require.NoError(t, err)
		assert.Equal(t, 1, f.stops)
		assert.Equal(t, 2, f.dials, "dialed again: Connect starts this build")
		assert.Same(t, f.clients[1], c)
		assert.Error(t, f.clients[0].FlushForTest(), "the old daemon's client was closed")
	})

	t.Run("a newer daemon is refused", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{newer}}
		_, err := f.link(ours, nil).join()
		require.Error(t, err)
		assert.Equal(t, "the loom daemon is 0.14.0 (v0.14.0 123), newer than this loom (0.13.0 (v0.13.0 abc)): upgrade loom, or run `loom serve stop`", err.Error())
		assert.Zero(t, f.stops)
	})

	t.Run("inside a loom session, an older daemon is kept", func(t *testing.T) {
		f := &fakeDaemons{t: t, builds: []rpc.Hello{older, ours}}
		_, err := f.link(ours, &tmux.NestedError{Session: "loom_agent"}).join()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a loom inside a loom session never replaces it")
		assert.Zero(t, f.stops, "the user's daemon is never stopped")
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
	ours := rpc.Hello{Exe: "ours", Version: "0.13.0"}
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
				dial: func() (*rpc.Client, rpc.Hello, error) {
					dials++
					if stops == 0 {
						var peer rpc.Hello
						if tc.peer != nil {
							peer = *tc.peer
						}
						return nil, peer, &rpc.MismatchError{Peer: tc.peer}
					}
					c, _, stop, err := rpc.InProcessForTest(core.NewForTest(core.Options{}))
					require.NoError(t, err)
					t.Cleanup(stop)
					replacement = c
					return c, ours, nil
				},
				stop:   func() error { stops++; return nil },
				nested: func() error { return nil },
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
			own:    ours,
			dial:   func() (*rpc.Client, rpc.Hello, error) { return nil, rpc.Hello{}, boom },
			stop:   func() error { t.Error("stopped"); return nil },
			nested: func() error { return nil },
		}
		_, err := link.join()
		assert.ErrorIs(t, err, boom)
	})
}
