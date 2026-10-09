package rpc

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noticesOf are the Notices among evs.
func noticesOf(evs []core.Event) []core.Notice {
	var out []core.Notice
	for _, ev := range evs {
		if n, ok := ev.(core.Notice); ok {
			out = append(out, n)
		}
	}
	return out
}

// dialTo dials srv over a pipe; the client closes when the test ends.
func dialTo(t *testing.T, srv *Server) *Client {
	t.Helper()
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// failingKill makes the model raise a Notice: an unstarted instance's kill
// job fails (it has no worktree). It returns the loop serving that model
// and the instance's ID.
func failingKill(t *testing.T) (*core.Loop, core.InstanceID) {
	t.Helper()
	inst := running(t, "u")
	m := core.NewForTest(core.Options{})
	m.SetWorkspacesForTest(workspace(t, "a", inst))
	loop := core.StartForTest(m)
	t.Cleanup(loop.Stop)
	return loop, m.IDOfForTest(inst)
}

// A daemon runs with no client connected, and a Notice raised then (a job
// that failed, the boot's) must still reach the user: the next connection
// is sent it, once, after its snapshot.
func TestServe_ANoticeNoClientWasThereForReachesTheNext(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	srv.Keep(core.Notice{Info: "from the boot"})

	loop.Kill(id, 0) // no client: straight to the model
	for _, j := range loop.JobsForTest() {
		loop.DeliverForTest(j())
	}

	first := dialTo(t, srv)
	got := noticesOf(first.Sync())
	require.Len(t, got, 2)
	assert.Equal(t, "from the boot", got[0].Info)
	assert.Error(t, got[1].Err, "the failed kill's notice")

	second := dialTo(t, srv)
	assert.Empty(t, noticesOf(second.Sync()), "a kept notice is sent once")
}

// A request's Notice goes to the client that made it; if that client has
// gone by the time its job fails, the next client is told instead, with no
// request named, rather than nobody.
func TestServe_ARequestsNoticeOutlivesItsClient(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)

	gone := dialTo(t, srv)
	gone.Kill(id, 3)
	require.NoError(t, gone.FlushForTest())
	jobs := loop.JobsForTest()
	gone.Close()
	require.Eventually(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.conns) == 0
	}, 5*time.Second, time.Millisecond)
	for _, j := range jobs {
		loop.DeliverForTest(j())
	}

	next := dialTo(t, srv)
	got := noticesOf(next.Sync())
	require.Len(t, got, 1)
	assert.Error(t, got[0].Err)
	assert.Zero(t, got[0].Req, "the request was another client's")
}

// A request's Notice whose client has gone goes to the clients still
// connected, at once, rather than wait for one that may come much later.
func TestServe_AGoneClientsNoticeReachesTheOthers(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)

	stays := dialTo(t, srv)
	gone := dialTo(t, srv)
	gone.Kill(id, 3)
	require.NoError(t, gone.FlushForTest())
	jobs := loop.JobsForTest()
	gone.Close()
	require.Eventually(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.conns) == 1
	}, 5*time.Second, time.Millisecond)
	for _, j := range jobs {
		loop.DeliverForTest(j())
	}

	require.NoError(t, stays.FlushForTest())
	got := noticesOf(stays.Sync())
	require.Len(t, got, 1)
	assert.Error(t, got[0].Err)
	assert.Zero(t, got[0].Req, "the request was another client's")

	next := dialTo(t, srv)
	assert.Empty(t, noticesOf(next.Sync()), "told once, not kept as well")
}

// The backlog holds the newest notices, so a daemon left alone for long
// cannot grow without bound.
func TestServe_TheBacklogIsBounded(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	for i := range maxBacklog + 5 {
		srv.Keep(core.Notice{Info: string(rune('a' + i%26))})
	}
	got := noticesOf(dialTo(t, srv).Sync())
	assert.Len(t, got, maxBacklog)
}

// A model that panicked is as good as gone: Fatal tells whatever runs the
// server (a daemon, which then exits).
func TestServe_FatalSaysTheModelIsGone(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	c := dialTo(t, srv)
	select {
	case <-srv.Fatal():
		t.Fatal("fatal before anything failed")
	default:
	}
	_ = catch(func() { loop.DeliverForTest(core.StartResult{}) }) // a nil instance panics the model
	_ = catch(func() { _ = c.FlushForTest() })
	select {
	case <-srv.Fatal():
	case <-time.After(5 * time.Second):
		t.Fatal("Fatal was never closed")
	}
	assert.Error(t, srv.FatalError())
}

// A connection that never says hello is closed: on a socket anyone of the
// user's can dial, a silent peer must not hold a server goroutine for good.
func TestServe_ASilentPeerIsDropped(t *testing.T) {
	prev := helloTimeout
	helloTimeout = 50 * time.Millisecond
	t.Cleanup(func() { helloTimeout = prev })
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	done := make(chan error, 1)
	go func() {
		_, err := b.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, io.EOF)
	case <-time.After(5 * time.Second):
		t.Fatal("the silent connection was never closed")
	}
}
