package rpc

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerClose_EndsAConnectionBeforeHello: Close ends every connection
// Serve was given, including one whose peer has not said hello and never
// will; it used to wait on that connection's read for good.
func TestServerClose_EndsAConnectionBeforeHello(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	a, b := net.Pipe()
	t.Cleanup(func() { b.Close() })
	srv.Serve(a)

	within(t, 5*time.Second, "Close", srv.Close)
	var err error
	within(t, 5*time.Second, "Read", func() { _, err = b.Read(make([]byte, 1)) })
	assert.ErrorIs(t, err, io.EOF, "the peer sees its connection end")
}

// TestServe_AfterCloseClosesAtOnce: a connection handed to a closed server
// is closed, and no goroutine serves it.
func TestServe_AfterCloseClosesAtOnce(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	srv.Close()

	a, b := net.Pipe()
	t.Cleanup(func() { b.Close() })
	within(t, 5*time.Second, "Serve", func() { srv.Serve(a) })
	var err error
	within(t, 5*time.Second, "Read", func() { _, err = b.Read(make([]byte, 1)) })
	assert.ErrorIs(t, err, io.EOF, "closed at once")
	srv.mu.Lock()
	open := len(srv.open)
	srv.mu.Unlock()
	assert.Zero(t, open, "and not tracked")
}

// TestServerConn_CloseGivesUpOnAPeerThatNeverReads: a refused hello's reply
// is flushed for at most closeFlushTimeout, then the connection is closed
// under the blocked write, so its goroutine ends.
func TestServerConn_CloseGivesUpOnAPeerThatNeverReads(t *testing.T) {
	old := closeFlushTimeout
	closeFlushTimeout = 50 * time.Millisecond
	t.Cleanup(func() { closeFlushTimeout = old })
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	t.Cleanup(func() { b.Close() })
	srv.Serve(a)

	require.NoError(t, json.NewEncoder(b).Encode(Frame{Hello: &Hello{Protocol: Protocol + 1}}))
	time.Sleep(500 * time.Millisecond) // far past the timeout, and the peer reads nothing
	var n int
	var err error
	within(t, 5*time.Second, "Read", func() { n, err = b.Read(make([]byte, 64)) })
	assert.Zero(t, n, "the refusal was never delivered")
	assert.ErrorIs(t, err, io.EOF, "the server gave up and closed")
}

// TestServe_ALateJoinerIsNotSentWhatWasPublishedBeforeItJoined: the events a
// connection is sent are the ones published after it joined, bar the
// snapshot. A Reply or Notice produced before it came is for the clients that
// were here; the newcomer is not registered until after they are sent it.
func TestServe_ALateJoinerIsNotSentWhatWasPublishedBeforeItJoined(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	dialTo := func() *Client {
		a, b := net.Pipe()
		srv.Serve(a)
		c, err := Dial(b)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}
	replies := func(c *Client) []uint64 {
		var out []uint64
		for _, ev := range c.Sync() {
			if r, ok := ev.(core.Reply); ok {
				out = append(out, uint64(r.Req))
			}
		}
		return out
	}
	first := dialTo()
	first.Sync()

	loop.Kill(99, 7) // straight to the model: refused, with a Reply nothing has published yet
	late := dialTo()
	require.NoError(t, first.FlushForTest())

	assert.Equal(t, []uint64{7}, replies(first), "the client already here is sent it")
	assert.Empty(t, replies(late), "the one that joined after is not")
}

// TestServe_ALateJoinerStartsFromThePublishedBaseline: a change made to the
// model and undone before the next publish shows in no diff, so a client
// that connects between the two must not be left with the first. Connecting
// publishes before it takes its snapshot, so the snapshot is the baseline
// the next diff starts from. The late joiner is driven by hand: Dial's own
// barrier would publish before the undo.
func TestServe_ALateJoinerStartsFromThePublishedBaseline(t *testing.T) {
	model := core.NewForTest(core.Options{})
	model.SetRCAuth(session.RemoteControlAuth{Reason: "a"})
	loop := core.StartForTest(model)
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a1, b1 := net.Pipe()
	srv.Serve(a1)
	first, err := Dial(b1)
	require.NoError(t, err)
	t.Cleanup(first.Close)

	loop.SetRCAuthForTest(session.RemoteControlAuth{Reason: "b"}) // straight to the model: nothing publishes it
	a2, b2 := net.Pipe()
	srv.Serve(a2)
	t.Cleanup(func() { b2.Close() })
	reasons := make(chan string, 8) // each ModelChanged the late joiner is sent
	go func() {
		dec := json.NewDecoder(b2)
		for {
			var f Frame
			if dec.Decode(&f) != nil {
				return
			}
			if f.Event != "ModelChanged" {
				continue
			}
			if ev, err := decodeEvent(f); err == nil {
				reasons <- ev.(core.ModelChanged).View.RCAuth.Reason
			}
		}
	}()
	require.NoError(t, json.NewEncoder(b2).Encode(Frame{Hello: &Hello{Protocol: Protocol}}))
	next := func(what string) string {
		t.Helper()
		select {
		case p := <-reasons:
			return p
		case <-time.After(5 * time.Second):
			t.Fatalf("no ModelChanged for %s", what)
			return ""
		}
	}
	assert.Equal(t, "b", next("the snapshot"))
	require.NoError(t, first.FlushForTest())
	assert.Equal(t, "b", first.RCAuth().Reason, "the connection already here is sent what was published")

	loop.SetRCAuthForTest(session.RemoteControlAuth{Reason: "a"}) // undone before the next publish
	require.NoError(t, first.FlushForTest())
	assert.Equal(t, "a", next("the undo"), "the late joiner follows the undo")
}
