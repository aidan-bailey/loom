package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// killCounter is a backend that counts the Kill calls that reach it.
type killCounter struct {
	*core.Loop
	kills atomic.Int32
}

func (k *killCounter) Kill(id core.InstanceID, req core.ReqID) {
	k.kills.Add(1)
	k.Loop.Kill(id, req)
}

// heldKill is a backend whose Kill blocks until release is closed, saying
// on entered that it was called.
type heldKill struct {
	*core.Loop
	entered chan struct{}
	release chan struct{}
}

func (h *heldKill) Kill(id core.InstanceID, req core.ReqID) {
	h.entered <- struct{}{}
	<-h.release
	h.Loop.Kill(id, req)
}

// TestBye_WaitsForTheCallsItLetIn: a call already on its way to the model
// when the bye comes is let finish before Bye returns, so whatever job it
// starts is in flight when the daemon waits for jobs (core.Loop.Quiesce).
func TestBye_WaitsForTheCallsItLetIn(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	bk := &heldKill{Loop: loop, entered: make(chan struct{}, 1), release: make(chan struct{})}
	srv := byeServer(t, bk)
	var once sync.Once
	release := func() { once.Do(func() { close(bk.release) }) }
	t.Cleanup(release) // before the server's Close, which waits for the call
	c, _ := dialServer(t, srv)

	go c.Kill(1, 0)
	select {
	case <-bk.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never reached the backend")
	}
	byed := make(chan struct{})
	go func() {
		defer close(byed)
		srv.Bye()
	}()
	waitStopping(t, c)
	select {
	case <-byed:
		t.Fatal("Bye returned while a call it let in was still running")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-byed:
	case <-time.After(5 * time.Second):
		t.Fatal("Bye never returned")
	}
}

// TestBye_WaitsForTheRepliesOfTheCallsItLetIn: a call Bye let in has its
// reply queued before Bye returns, so a Close right after it flushes the
// reply rather than dropping it. The server's afterCall hook holds the call
// between its return from the backend and its reply.
func TestBye_WaitsForTheRepliesOfTheCallsItLetIn(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	entered, held := make(chan struct{}, 1), make(chan struct{})
	srv.afterCall = func(method string) {
		if method == "Open" {
			entered <- struct{}{}
			<-held
		}
	}
	var once sync.Once
	release := func() { once.Do(func() { close(held) }) }
	t.Cleanup(srv.Close)
	t.Cleanup(release) // before the server's Close, which waits for the call
	c, _ := dialServer(t, srv)

	opened := make(chan error, 1)
	go func() {
		_, err := c.Open(99)
		opened <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never returned from the backend")
	}
	byed := make(chan struct{})
	go func() {
		defer close(byed)
		srv.Bye()
	}()
	waitStopping(t, c)
	select {
	case <-byed:
		t.Fatal("Bye returned before the reply of a call it let in was queued")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-byed:
	case <-time.After(5 * time.Second):
		t.Fatal("Bye never returned")
	}
	within(t, 10*time.Second, "Close", srv.Close)
	select {
	case err := <-opened:
		require.Error(t, err, "nothing serves workspace 99")
		assert.False(t, errors.Is(err, core.ErrUnavailable), "the model's own answer, not the closed connection: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the request never returned")
	}
}

// recordedConn is the server's end of a connection, keeping every byte the
// server read from it: what reached the server.
type recordedConn struct {
	net.Conn
	mu   sync.Mutex
	read bytes.Buffer
}

func (r *recordedConn) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	r.mu.Lock()
	r.read.Write(p[:n])
	r.mu.Unlock()
	return n, err
}

func (r *recordedConn) received() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.read.String()
}

// byeServer serves b, closed when the test ends.
func byeServer(t *testing.T, b Backend) *Server {
	t.Helper()
	srv := NewServer(b)
	t.Cleanup(srv.Close)
	return srv
}

// dialServer connects a daemon's client (Dial) to srv over a pipe, closed
// when the test ends; the server's end is returned too.
func dialServer(t *testing.T, srv *Server) (*Client, *recordedConn) {
	t.Helper()
	a, b := net.Pipe()
	rc := &recordedConn{Conn: a}
	srv.Serve(rc)
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c, rc
}

// waitStopping waits until c has heard the bye.
func waitStopping(t *testing.T, c *Client) {
	t.Helper()
	require.Eventually(t, c.Stopping, 5*time.Second, 5*time.Millisecond, "the client heard the bye")
}

// waitLost waits until c has lost the model.
func waitLost(t *testing.T, c *Client) {
	t.Helper()
	require.Eventually(t, func() bool { return c.Err() != nil }, 5*time.Second, 5*time.Millisecond, "the client lost the model")
}

// TestBye_ReachesEveryConnection: a stopping server tells every client,
// and stopping is no loss: neither reports one.
func TestBye_ReachesEveryConnection(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := byeServer(t, loop)
	a, _ := dialServer(t, srv)
	b, _ := dialServer(t, srv)
	assert.False(t, a.Stopping())

	srv.Bye()
	waitStopping(t, a)
	waitStopping(t, b)
	assert.NoError(t, a.Err(), "stopping is not a loss")
	assert.NoError(t, b.Err())
}

// TestBye_ARequestAfterItIsRefusedLocally: once a client has heard the bye,
// a request fails at once as unavailable, a refusal, and is never written:
// nothing reaches the server.
func TestBye_ARequestAfterItIsRefusedLocally(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	bk := &killCounter{Loop: loop}
	srv := byeServer(t, bk)
	c, rc := dialServer(t, srv)

	srv.Bye()
	waitStopping(t, c)
	var err error
	within(t, 5*time.Second, "a request while stopping", func() { _, err = c.Open(1) })
	assert.ErrorIs(t, err, core.ErrUnavailable)
	assert.ErrorIs(t, err, core.ErrRefused, "the TUI's refusal path shows it")
	assert.Contains(t, err.Error(), "stopping")
	within(t, 5*time.Second, "a void request", func() { c.Kill(1, 0) })
	within(t, 5*time.Second, "a cast", func() { c.MarkOutput("x") })
	time.Sleep(50 * time.Millisecond) // anything written would have been read by now
	assert.NotContains(t, rc.received(), `"method":"Open"`, "nothing reached the server")
	assert.NotContains(t, rc.received(), `"method":"Kill"`)
	assert.NotContains(t, rc.received(), `"method":"MarkOutput"`)
	assert.Zero(t, bk.kills.Load())
	assert.NoError(t, c.Err(), "a refusal is not a loss")
}

// rawFrames reads the frames nc carries into a channel, until it closes.
func rawFrames(nc net.Conn) <-chan Frame {
	out := make(chan Frame, 64)
	go func() {
		defer close(out)
		dec := json.NewDecoder(nc)
		for {
			var f Frame
			if err := dec.Decode(&f); err != nil {
				return
			}
			out <- f
		}
	}()
	return out
}

// next is the first frame from frames that ok accepts, skipping the rest.
func next(t *testing.T, frames <-chan Frame, what string, ok func(Frame) bool) Frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, open := <-frames:
			require.True(t, open, "the connection closed before %s", what)
			if ok(f) {
				return f
			}
		case <-deadline:
			t.Fatalf("no %s within 5s", what)
		}
	}
}

// TestBye_TheServerRefusesARequestThatCrossedIt: a request written before
// its client heard the bye reaches a stopping server, which answers it
// unavailable without calling the model.
func TestBye_TheServerRefusesARequestThatCrossedIt(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	bk := &killCounter{Loop: loop}
	srv := byeServer(t, bk)
	a, b := net.Pipe()
	t.Cleanup(func() { b.Close() })
	srv.Serve(a)
	enc := json.NewEncoder(b)
	self := Self()
	require.NoError(t, enc.Encode(Frame{Hello: &self}))
	frames := rawFrames(b)
	next(t, frames, "the hello", func(f Frame) bool { return f.Hello != nil })

	srv.Bye()
	next(t, frames, "the bye", func(f Frame) bool { return f.Bye != "" })
	params, err := json.Marshal(KillParams{ID: 1, Req: 3})
	require.NoError(t, err)
	go func() { _ = enc.Encode(Frame{ID: 7, Method: "Kill", Params: params}) }()
	reply := next(t, frames, "the reply", func(f Frame) bool { return f.ID == 7 })
	require.NotNil(t, reply.Error)
	assert.Equal(t, core.CodeUnavailable, reply.Error.Code)
	assert.ErrorIs(t, reply.Error, core.ErrUnavailable)
	assert.Zero(t, bk.kills.Load(), "the model was never called")

	// So is the barrier: a stopping server takes nothing new.
	go func() { _ = enc.Encode(Frame{ID: 8, Method: pingMethod, Params: json.RawMessage("{}")}) }()
	ping := next(t, frames, "the ping's reply", func(f Frame) bool { return f.ID == 8 })
	require.NotNil(t, ping.Error)
	assert.Equal(t, core.CodeUnavailable, ping.Error.Code)
}

// noGH is a machine whose gh fails at once, so a FetchIssue job runs no
// real gh.
func noGH() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return errors.New("no gh here") },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, errors.New("no gh here") },
	}
}

// TestBye_InFlightReplyArrivesBeforeClose: a request made before the bye
// still gets its Reply: the job finishes and its result is delivered while
// the server stops, and Close publishes it and flushes it to the client
// before the connection ends. Only then does the client report the loss.
func TestBye_InFlightReplyArrivesBeforeClose(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{GHExec: noGH()}))
	t.Cleanup(loop.Stop)
	srv := byeServer(t, loop)
	c, _ := dialServer(t, srv)

	c.FetchIssue(t.TempDir(), 5, 7)
	jobs := loop.JobsForTest()
	require.Len(t, jobs, 1, "the request's job, held")
	srv.Bye()
	waitStopping(t, c)

	loop.DeliverForTest(jobs[0]()) // wakes no one: only Close publishes it
	within(t, 10*time.Second, "Close", srv.Close)
	waitLost(t, c)
	replies := repliesOf(c.Sync())
	require.Len(t, replies, 1, "the in-flight request's Reply arrived before the connection closed")
	assert.Equal(t, core.ReqID(7), replies[0].Req)
	assert.Error(t, replies[0].Err, "gh failed")
	assert.ErrorIs(t, c.Err(), core.ErrUnavailable, "and the loss is graceful")
}

// TestBye_LossAfterItIsGraceful: a connection that closes after the bye is
// the daemon stopping, which the client's loss says (ErrUnavailable).
func TestBye_LossAfterItIsGraceful(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := byeServer(t, loop)
	c, _ := dialServer(t, srv)

	srv.Bye()
	waitStopping(t, c)
	srv.Close()
	waitLost(t, c)
	assert.ErrorIs(t, c.Err(), core.ErrUnavailable)
	var w *core.WireError
	require.ErrorAs(t, c.Err(), &w)
	assert.Equal(t, core.CodeUnavailable, w.Code)
	assert.Contains(t, w.Message, "stopped")

	var err error
	within(t, 5*time.Second, "a request after the loss", func() { _, err = c.Open(1) })
	assert.ErrorIs(t, err, core.ErrUnavailable)
}

// TestLoss_WithoutByeIsNot: a connection that closes with no bye is a
// crash, not a stop: the loss is no ErrUnavailable, though a request after
// it is refused as unavailable.
func TestLoss_WithoutByeIsNot(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := byeServer(t, loop)
	c, _ := dialServer(t, srv)

	srv.Close()
	waitLost(t, c)
	assert.False(t, errors.Is(c.Err(), core.ErrUnavailable), "a crash: %v", c.Err())
	assert.False(t, c.Stopping())
	var w *core.WireError
	require.ErrorAs(t, c.Err(), &w)
	assert.Equal(t, core.CodeError, w.Code)

	var err error
	within(t, 5*time.Second, "a request after the loss", func() { _, err = c.Open(1) })
	assert.ErrorIs(t, err, core.ErrUnavailable)
	assert.Contains(t, err.Error(), "connection to the model lost", "naming the loss")
}

// TestBye_AJoinerWhileStoppingFailsToDial: a stopping daemon can't be
// joined: a client that connects meanwhile hears the bye after its
// snapshot, and Dial fails as unavailable.
func TestBye_AJoinerWhileStoppingFailsToDial(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := byeServer(t, loop)
	srv.Bye()

	a, b := net.Pipe()
	srv.Serve(a)
	var err error
	within(t, 5*time.Second, "Dial", func() { _, err = Dial(b) })
	assert.ErrorIs(t, err, core.ErrUnavailable)
}

// TestClient_IgnoresAnUnknownFrameField: a frame carrying a field this
// build does not know decodes past it. A frame of nothing but unknown
// fields falls through the reader, which is what a 3B client does with a
// bye, and the events after it still apply.
func TestClient_IgnoresAnUnknownFrameField(t *testing.T) {
	c, s := dialScripted(t)
	c.Sync()

	raw := func(line string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		require.NoError(t, s.enc.Encode(json.RawMessage(line)))
	}
	raw(`{"zzz":1}`)
	require.NoError(t, c.FlushForTest(), "the client read past a frame it has no case for")
	assert.False(t, c.Stopping())

	raw(`{"bye":"stopping","zzz":1}`)
	f, err := encodeEvent(core.Notice{Info: "after the bye"})
	require.NoError(t, err)
	s.send(f)
	var got []core.Event
	require.Eventually(t, func() bool {
		got = append(got, c.Sync()...)
		return len(noticesOf(got)) == 1
	}, 5*time.Second, 5*time.Millisecond, "the event after the bye applied")
	assert.Equal(t, "after the bye", noticesOf(got)[0].Info)
	assert.True(t, c.Stopping())
	assert.NoError(t, c.Err())
}

var _ io.ReadWriteCloser = (*recordedConn)(nil)

// TestClient_RecordsTheRequestsRefusedAsUnavailable: a request a Reply
// answers that is refused as unavailable, by a stopping server (it crossed
// the bye) or by the client itself (after the bye), never reaches the model,
// so no Reply comes: the client records its request ID for the TUI to fail
// (TakeRefused). A request wanting no Reply, or refused otherwise, is not
// recorded.
func TestClient_RecordsTheRequestsRefusedAsUnavailable(t *testing.T) {
	c, s := dialScripted(t)
	s.answerWith(func(f Frame) Frame {
		if f.Method == "Pause" {
			return Frame{ID: f.ID, Error: &core.WireError{Code: core.CodeRefused, Message: "pause x: refused"}}
		}
		return Frame{ID: f.ID, Error: &core.WireError{Code: core.CodeUnavailable, Message: "the loom daemon is stopping"}}
	})
	c.Kill(1, 3)
	c.Kill(1, 0)
	c.Pause(1, 4)
	assert.Equal(t, []core.ReqID{3}, c.TakeRefused(), "the one the server refused as unavailable")
	assert.Empty(t, c.TakeRefused(), "taken")

	s.send(Frame{Bye: "stopping"})
	waitStopping(t, c)
	c.Resume(1, 5)
	c.FetchIssue("repo", 1, 6)
	assert.Equal(t, []core.ReqID{5, 6}, c.TakeRefused(), "the ones the client refused itself")
}
