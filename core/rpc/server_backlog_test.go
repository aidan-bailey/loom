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

// dialAsTo is dialTo for a client that names its role.
func dialAsTo(t *testing.T, srv *Server, role string) *Client {
	t.Helper()
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := DialAs(b, role)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// raiseNotice has the model raise a Notice no request is behind: the kill of
// the instance failingKill returned fails, and its job lands.
func raiseNotice(t *testing.T, loop *core.Loop, id core.InstanceID) {
	t.Helper()
	loop.Kill(id, 0)
	for _, j := range loop.JobsForTest() {
		loop.DeliverForTest(j())
	}
}

// raiseRequestNotice has c make the failing kill of id as its request 3, and
// the job lands: the model raises a Notice that names c's request.
func raiseRequestNotice(t *testing.T, loop *core.Loop, c *Client, id core.InstanceID) {
	t.Helper()
	c.Kill(id, 3)
	require.NoError(t, c.FlushForTest())
	for _, j := range loop.JobsForTest() {
		loop.DeliverForTest(j())
	}
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

// The web layer is always connected, so a notice raised while it is the only
// connection must still reach the next TUI: the web layer is told, and the
// notice is kept for whoever else comes.
func TestServe_ANoticeRaisedWhileOnlyTheWebLayerIsConnectedWaitsForTheNextTUI(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	web := dialAsTo(t, srv, RoleWeb)

	raiseNotice(t, loop, id)
	require.NoError(t, web.FlushForTest())
	told := noticesOf(web.Sync())
	require.Len(t, told, 1, "the web layer is told, as any connection is")
	assert.Error(t, told[0].Err)

	tui := dialTo(t, srv)
	kept := noticesOf(tui.Sync())
	require.Len(t, kept, 1, "and the next TUI is sent it too")
	assert.Error(t, kept[0].Err)
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "a kept notice is sent once")
}

// The other side of the rule: with a TUI connected beside the web layer the
// notice reached a client, so both are told and nothing is kept.
func TestServe_ANoticeIsNotKeptWhileATUIIsConnectedBesideTheWebLayer(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	web := dialAsTo(t, srv, RoleWeb)
	tui := dialTo(t, srv)

	raiseNotice(t, loop, id)
	require.NoError(t, web.FlushForTest())
	require.NoError(t, tui.FlushForTest())
	assert.Len(t, noticesOf(web.Sync()), 1)
	assert.Len(t, noticesOf(tui.Sync()), 1)
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "told to the TUI, so not kept as well")
}

// A web connection that joins takes none of the backlog: the notices a
// daemon's boot kept wait for the TUI, and the TUI gets them once.
func TestServe_AWebConnectionIsNotSentTheBacklog(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	srv.Keep(core.Notice{Info: "from the boot"})

	web := dialAsTo(t, srv, RoleWeb)
	assert.Empty(t, noticesOf(web.Sync()), "the web layer takes none of the backlog")

	tui := dialTo(t, srv)
	got := noticesOf(tui.Sync())
	require.Len(t, got, 1, "it waits for the TUI")
	assert.Equal(t, "from the boot", got[0].Info)
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "and is sent once")
}

// Only the role `web` is exempt: a client naming none, or a role this build
// does not know (a newer peer's), takes the backlog as ever.
func TestServe_AConnectionWithAnyOtherRoleIsAClient(t *testing.T) {
	for _, tc := range []struct{ name, role string }{
		{"none", ""},
		{"one this build does not know", "tui"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := core.StartForTest(core.NewForTest(core.Options{}))
			t.Cleanup(loop.Stop)
			srv := NewServer(loop)
			t.Cleanup(srv.Close)
			srv.Keep(core.Notice{Info: "from the boot"})

			c := dialAsTo(t, srv, tc.role)
			got := noticesOf(c.Sync())
			require.Len(t, got, 1, "an ordinary client takes the backlog")
			assert.Equal(t, "from the boot", got[0].Info)
		})
	}
}

// A request's Notice whose client has gone is told to the connections still
// there; when the only one left is the web layer's, it is kept for the next
// TUI as well.
func TestServe_AGoneClientsNoticeWaitsForTheNextTUIWhenOnlyTheWebLayerStays(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)

	stays := dialAsTo(t, srv, RoleWeb)
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
	told := noticesOf(stays.Sync())
	require.Len(t, told, 1, "the web layer is told")
	assert.Zero(t, told[0].Req, "the request was another client's")

	next := dialTo(t, srv)
	kept := noticesOf(next.Sync())
	require.Len(t, kept, 1, "and the next TUI is sent it")
	assert.Error(t, kept[0].Err)
	assert.Zero(t, kept[0].Req)
}

// A Notice naming a request of the web layer's own is the web layer's, with
// its request, and also handled as if no web connection existed: a TUI
// connected beside it is told too, since a stash notice may be the only
// record of a SHA and the phone may never see it. It reached a client, so it
// is not kept as well.
func TestServe_ANoticeOfAWebRequestAlsoReachesTheTUI(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	web := dialAsTo(t, srv, RoleWeb)
	tui := dialTo(t, srv)

	raiseRequestNotice(t, loop, web, id)
	require.NoError(t, web.FlushForTest())
	require.NoError(t, tui.FlushForTest())
	told := noticesOf(web.Sync())
	require.Len(t, told, 1, "the web layer is told, once")
	assert.Equal(t, core.ReqID(3), told[0].Req, "it names the web layer's own request")
	seen := noticesOf(tui.Sync())
	require.Len(t, seen, 1, "and the TUI is told too")
	assert.Error(t, seen[0].Err)
	assert.Zero(t, seen[0].Req, "the request was the web layer's")
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "told to the TUI, so not kept as well")
}

// With only the web layer connected, the Notice of its own request is told
// to it and also kept: the next TUI is sent it from the backlog, once.
func TestServe_ANoticeOfAWebRequestWaitsForTheNextTUIWhenOnlyTheWebLayerIsConnected(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	web := dialAsTo(t, srv, RoleWeb)

	raiseRequestNotice(t, loop, web, id)
	require.NoError(t, web.FlushForTest())
	told := noticesOf(web.Sync())
	require.Len(t, told, 1, "the web layer is told, once")
	assert.Equal(t, core.ReqID(3), told[0].Req, "it names the web layer's own request")

	tui := dialTo(t, srv)
	kept := noticesOf(tui.Sync())
	require.Len(t, kept, 1, "and the next TUI is sent it from the backlog")
	assert.Error(t, kept[0].Err)
	assert.Zero(t, kept[0].Req, "the request was the web layer's")
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "a kept notice is sent once")
}

// The other side: a TUI's own request's Notice goes to that TUI alone, the
// web layer connected beside it is not told, and nothing is kept.
func TestServe_ANoticeOfATUIsRequestIsTheTUIsAlone(t *testing.T) {
	loop, id := failingKill(t)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	web := dialAsTo(t, srv, RoleWeb)
	tui := dialTo(t, srv)

	raiseRequestNotice(t, loop, tui, id)
	require.NoError(t, tui.FlushForTest())
	require.NoError(t, web.FlushForTest())
	got := noticesOf(tui.Sync())
	require.Len(t, got, 1)
	assert.Equal(t, core.ReqID(3), got[0].Req, "it names the TUI's own request")
	assert.Empty(t, noticesOf(web.Sync()), "a TUI's request's notice is its requester's alone")
	assert.Empty(t, noticesOf(dialTo(t, srv).Sync()), "and it is not kept")
}
