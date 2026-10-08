package rpc

import (
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var reqIDType = reflect.TypeOf(core.ReqID(0))

// Every event that names a request is sent as forConn says: the client that
// made the request gets its own ID back, and no other client is told it.
// An event type gaining a request ID it forgot would hand every client the
// server's IDs, which collide with their own.
func TestForConn_CoversEveryEventNamingARequest(t *testing.T) {
	const mine, theirs = 2, 1
	tagged := core.ReqID(mine)<<connShift | 5
	for _, ev := range core.EventTypes() {
		typ := reflect.TypeOf(ev)
		v := reflect.New(typ).Elem()
		var fields []int
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Type == reqIDType {
				v.Field(i).Set(reflect.ValueOf(tagged))
				fields = append(fields, i)
			}
		}
		if len(fields) == 0 {
			continue
		}
		e := v.Interface().(core.Event)
		require.True(t, routed(e), "%s names a request", typ.Name())

		got, ok := forConn(e, mine)
		require.True(t, ok, "%s reaches the client that made the request", typ.Name())
		for _, i := range fields {
			assert.Equal(t, core.ReqID(5), reflect.ValueOf(got).Field(i).Interface(), "%s carries the client's own ID", typ.Name())
		}
		if got, ok := forConn(e, theirs); ok {
			for _, i := range fields {
				assert.Zero(t, reflect.ValueOf(got).Field(i).Interface(), "%s names no request to another client", typ.Name())
			}
		}
	}
}

func TestTagReq(t *testing.T) {
	tag := tagReq(3)
	req := core.ReqID(0)
	require.NoError(t, tag(&req))
	assert.Zero(t, req, "no Reply wanted: nothing to route")

	req = 7
	require.NoError(t, tag(&req))
	assert.Equal(t, core.ReqID(3)<<connShift|7, req)
	back, mine := local(req, 3)
	assert.Equal(t, core.ReqID(7), back)
	assert.True(t, mine)
	_, mine = local(req, 4)
	assert.False(t, mine)

	req = 1 << connShift
	var we *core.WireError
	require.True(t, errors.As(tag(&req), &we), "a client's IDs stay below 2^32")
	assert.Equal(t, core.CodeProtocol, we.Code)
}

// twoClients serves loop to two clients over pipes, the first connection 1
// and the second 2, both closed when the test ends.
func twoClients(t *testing.T, loop *core.Loop) (*Client, *Client) {
	t.Helper()
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	dial := func() *Client {
		a, b := net.Pipe()
		srv.Serve(a)
		c, err := Dial(b)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}
	return dial(), dial()
}

func repliesOf(evs []core.Event) []core.Reply {
	var out []core.Reply
	for _, ev := range evs {
		if r, ok := ev.(core.Reply); ok {
			out = append(out, r)
		}
	}
	return out
}

// Two clients both number their requests from 1: each gets the Reply to
// its own request 5, never the other's.
func TestServe_EachClientGetsTheRepliesToItsOwnRequests(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	a, b := twoClients(t, loop)

	a.Kill(99, 5) // no such session: refused at once
	b.Kill(98, 5)
	require.NoError(t, a.FlushForTest())
	require.NoError(t, b.FlushForTest())

	ra, rb := repliesOf(a.Sync()), repliesOf(b.Sync())
	require.Len(t, ra, 1)
	require.Len(t, rb, 1)
	assert.Equal(t, core.ReqID(5), ra[0].Req)
	assert.Equal(t, core.InstanceID(99), ra[0].ID)
	assert.Equal(t, core.ReqID(5), rb[0].Req)
	assert.Equal(t, core.InstanceID(98), rb[0].ID)
}

// A request's job reports to the client that made it: the Notice of its
// failure goes to that client alone, and a Started it causes names the
// request to that client and none to the other, which only attaches the
// new session's pane.
func TestServe_ARequestsEventsNameItOnlyToItsClient(t *testing.T) {
	inst := running(t, "s")
	unstarted := running(t, "u")
	ws := workspace(t, "a", inst)
	m := core.NewForTest(core.Options{})
	m.SetWorkspacesForTest(nil, []*core.Workspace{ws})
	loop := core.StartForTest(m)
	t.Cleanup(loop.Stop)
	a, b := twoClients(t, loop)
	a.Sync()
	b.Sync()

	// A Started a's request 4 caused (connection 1), and one nobody asked for.
	loop.DeliverForTest(core.CausedForTest(1<<connShift|4, core.StartResult{Instance: inst, Owner: ws}))
	loop.DeliverForTest(core.StartResult{Instance: inst, Owner: ws})
	require.NoError(t, a.FlushForTest())
	require.NoError(t, b.FlushForTest())
	reqs := func(evs []core.Event) []core.ReqID {
		var out []core.ReqID
		for _, ev := range evs {
			if s, ok := ev.(core.Started); ok {
				out = append(out, s.Req)
			}
		}
		return out
	}
	assert.Equal(t, []core.ReqID{4, 0}, reqs(a.Sync()), "the client that asked is named its request")
	assert.Equal(t, []core.ReqID{0, 0}, reqs(b.Sync()), "the other client is told of the start, not the request")

	// a kills an instance that never started: its job fails (no worktree).
	ws.AddForTest(unstarted)
	id := m.IDOfForTest(unstarted)
	a.Kill(id, 3)
	for _, j := range loop.JobsForTest() {
		loop.DeliverForTest(j())
	}
	require.NoError(t, a.FlushForTest())
	require.NoError(t, b.FlushForTest())
	notices := func(evs []core.Event) []core.Notice {
		var out []core.Notice
		for _, ev := range evs {
			if n, ok := ev.(core.Notice); ok {
				out = append(out, n)
			}
		}
		return out
	}
	evsA, evsB := a.Sync(), b.Sync()
	require.Len(t, notices(evsA), 1, "the client that asked is shown its job's failure")
	assert.Equal(t, core.ReqID(3), notices(evsA)[0].Req)
	assert.Len(t, repliesOf(evsA), 1)
	assert.Empty(t, notices(evsB), "the other client is not")
	assert.Empty(t, repliesOf(evsB))
}

// Each client's selected row has its full diff refreshed: the model is told
// the union, which loses a client's row when it goes.
func TestServe_EveryClientsSelectionReachesTheModel(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	a, b := twoClients(t, loop)

	a.SetSelected(7)
	b.SetSelected(9)
	require.NoError(t, a.FlushForTest())
	require.NoError(t, b.FlushForTest())
	assert.Equal(t, []core.InstanceID{7, 9}, loop.SelectedForTest())

	b.SetSelected(7)
	require.NoError(t, b.FlushForTest())
	assert.Equal(t, []core.InstanceID{7}, loop.SelectedForTest(), "a row two clients select is named once")

	b.SetSelected(9)
	require.NoError(t, b.FlushForTest())
	a.Close()
	require.Eventually(t, func() bool {
		return reflect.DeepEqual([]core.InstanceID{9}, loop.SelectedForTest())
	}, 5*time.Second, time.Millisecond, "a client that goes takes its selection with it")
}
