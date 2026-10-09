package rpc

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scripted is a server end driven by hand, for a test that needs frames a
// real Server never sends: it answers the hello, answers each request with
// answer (an empty reply by default), and writes whatever frames the test
// hands to send.
type scripted struct {
	mu     sync.Mutex
	enc    *json.Encoder
	answer func(Frame) Frame
}

func (s *scripted) send(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(f)
}

// answerWith sets how requests are answered; the barrier (rpc.Ping) always
// gets an empty reply.
func (s *scripted) answerWith(f func(Frame) Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = f
}

func (s *scripted) replyTo(f Frame) Frame {
	s.mu.Lock()
	answer := s.answer
	s.mu.Unlock()
	if f.Method == pingMethod || answer == nil {
		return Frame{ID: f.ID, Result: json.RawMessage("{}")}
	}
	return answer(f)
}

// dialScripted connects a client to a scripted server.
func dialScripted(t *testing.T) (*Client, *scripted) {
	t.Helper()
	a, b := net.Pipe()
	s := &scripted{enc: json.NewEncoder(a)}
	t.Cleanup(func() { a.Close() })
	go func() {
		dec := json.NewDecoder(a)
		var hello Frame
		if dec.Decode(&hello) != nil {
			return
		}
		s.send(Frame{Hello: &Hello{Protocol: Protocol}})
		for {
			var f Frame
			if dec.Decode(&f) != nil {
				return
			}
			if f.ID != 0 {
				s.send(s.replyTo(f))
			}
		}
	}()
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c, s
}

// TestEvents_AnUnknownOneIsDroppedAKnownOneThatWillNotDecodeIsFatal: a newer
// peer may send an event this build has never heard of, which is skipped.
// An event it knows but cannot decode may be a Reply, whose requester would
// wait for it for good, or state the replica would keep stale: the model is
// as good as gone, as when the server cannot encode one.
func TestEvents_AnUnknownOneIsDroppedAKnownOneThatWillNotDecodeIsFatal(t *testing.T) {
	c, s := dialScripted(t)
	c.Sync()
	select {
	case <-c.Wakes():
	default:
	}

	s.send(Frame{Event: "FromTheFuture", Data: json.RawMessage(`{"Anything":1}`)})
	require.NoError(t, c.FlushForTest(), "the barrier follows the event: it has been read")
	assert.Zero(t, c.RCAuth(), "the client still works")
	assert.Empty(t, c.Sync(), "and queued nothing")

	s.send(Frame{Event: "ModelChanged", Data: json.RawMessage(`"not an object"`)})
	select {
	case <-c.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("the bad event did not wake the client")
	}
	w := assertLost(t, c, core.CodeProtocol)
	assert.Contains(t, w.Message, "ModelChanged", "it names the event")
}

// TestReply_AProtocolErrorIsFatal: a reply carrying a protocol error means
// the two sides disagree about the wire, which is not any method's own
// error: the model is lost, as with a panic reply, and Wakes says so.
func TestReply_AProtocolErrorIsFatal(t *testing.T) {
	c, s := dialScripted(t)
	s.answerWith(func(f Frame) Frame {
		return Frame{ID: f.ID, Error: &core.WireError{Code: core.CodeProtocol, Message: "rpc: decode params: bad"}}
	})
	select {
	case <-c.Wakes():
	default:
	}

	assert.Nil(t, catch(func() { c.Kill(1, 0) }), "the caller does not panic")
	select {
	case <-c.Wakes():
	default:
		t.Fatal("the loss did not wake the client")
	}
	assertLost(t, c, core.CodeProtocol)
}

// TestReply_AnUnknownMethodIsAnOrdinaryError: a method the server does not
// know comes back as an unsupported error, which a newer client can probe
// for: it is not fatal, and the client goes on.
func TestReply_AnUnknownMethodIsAnOrdinaryError(t *testing.T) {
	model := core.NewForTest(core.Options{})
	model.SetRCAuth(session.RemoteControlAuth{Reason: "a"})
	loop := core.StartForTest(model)
	c := pair(t, loop)

	var err error
	p := catch(func() { err = c.request("NoSuchMethod", struct{}{}, nil) })
	require.Nil(t, p, "an unknown method does not panic the client")
	var w *core.WireError
	require.ErrorAs(t, err, &w)
	assert.Equal(t, core.CodeUnsupported, w.Code)
	assert.Contains(t, w.Message, "NoSuchMethod")
	assert.Equal(t, "a", c.RCAuth().Reason)
	assert.NoError(t, c.FlushForTest(), "and goes on")
}

// TestReply_AnErrorIsTheMethodsOwn: any other error is the method's, and
// the client goes on.
func TestReply_AnErrorIsTheMethodsOwn(t *testing.T) {
	c, s := dialScripted(t)
	s.answerWith(func(f Frame) Frame {
		return Frame{ID: f.ID, Error: &core.WireError{Code: core.CodeError, Message: "save failed"}}
	})

	var err error
	assert.Nil(t, catch(func() { _, err = c.Open(1) }))
	assert.EqualError(t, err, "save failed")
	assert.Nil(t, catch(func() { c.RCAuth() }), "the client goes on")
}
