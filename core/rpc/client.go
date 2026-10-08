package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// errClosed is a call's error once the connection is gone.
var errClosed = errors.New("rpc: connection closed")

// Client is core.Core over a connection to a Server. It keeps a replica
// of the model's published state (sent whole when it connects, then every
// change), answers every rpc:local query from it, sends casts one way,
// and waits for each request's reply, which follows the events the
// request produced: a read right after a request sees its effect. Sync
// returns the events received since the last Sync, the state events
// coalesced to the newest state. Wakes signals when events arrive.
//
// A panic the model raised reaches the caller that met it, and every
// call after it panics too, so the TUI's own recovery restores the
// terminal.
type Client struct {
	nc  io.ReadWriteCloser
	wmu sync.Mutex
	enc *json.Encoder

	// synchronous makes every rpc:local read (Sync included) a ping first,
	// so it sees everything the model published by then, and every cast a
	// ping after, so it has reached the model when it returns: a test seam
	// (InProcessForTest), for tests that change or read the model directly.
	synchronous bool

	mu      sync.Mutex
	rep     replica
	changed state
	queue   []core.Event
	pending map[uint64]chan Frame
	nextID  uint64
	fatal   *core.WireError
	closed  bool
	// closing is set by Close before it closes the connection, so the
	// reader can tell a connection the client ended from one it lost.
	closing bool

	wake       chan struct{}
	done       chan struct{}
	readerDone chan struct{}
	closeOnce  sync.Once
	wakeOnce   sync.Once
}

// Dial says hello on nc, starts reading, and returns once the replica
// holds the server's snapshot.
func Dial(nc io.ReadWriteCloser) (*Client, error) { return dial(nc, false) }

func dial(nc io.ReadWriteCloser, synchronous bool) (*Client, error) {
	c := &Client{
		nc:          nc,
		enc:         json.NewEncoder(nc),
		synchronous: synchronous,
		pending:     map[uint64]chan Frame{},
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		readerDone:  make(chan struct{}),
	}
	if err := c.write(Frame{Hello: &Hello{Protocol: Protocol, Build: build()}}); err != nil {
		nc.Close()
		return nil, err
	}
	dec := json.NewDecoder(nc)
	var hello Frame
	if err := dec.Decode(&hello); err != nil {
		nc.Close()
		return nil, fmt.Errorf("rpc: read hello: %w", err)
	}
	if hello.Error != nil {
		nc.Close()
		return nil, hello.Error
	}
	if hello.Hello == nil || hello.Hello.Protocol != Protocol {
		nc.Close()
		return nil, &core.WireError{Code: core.CodeMismatch, Message: "rpc: the server speaks another protocol"}
	}
	go c.read(dec)
	if err := c.handshake(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// handshake is the barrier that completes Dial. A fatal error that arrives
// meanwhile (the model panicked, or the connection was lost) is Dial's
// error rather than a panic in its caller. It is best-effort: a Fatal frame
// that lands after the ping has checked for one gives a Dial that succeeds,
// whose first call panics with it.
func (c *Client) handshake() (err error) {
	defer func() {
		if r := recover(); r != nil {
			w, ok := r.(*core.WireError)
			if !ok {
				panic(r)
			}
			err = w
		}
	}()
	return c.ping()
}

// read is the client's reader: it applies each event to the replica (or
// queues it), records a fatal error, and hands each reply to its caller,
// in the order the server wrote them.
func (c *Client) read(dec *json.Decoder) {
	defer close(c.readerDone)
	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			c.fail(err)
			return
		}
		switch {
		case f.Event != "":
			ev, err := decodeEvent(f)
			if errors.Is(err, errUnknownEvent) {
				// A newer peer may send an event this build has not heard of.
				log.For("rpc").Warn("client.unknown_event", "event", f.Event)
				continue
			}
			if err != nil {
				// A known event that will not decode may be a Reply, whose
				// requester would wait for it for good, or state the replica
				// would hold stale: the model is as good as gone, as when the
				// server cannot encode one.
				log.For("rpc").Error("client.bad_event", "event", f.Event, "err", err)
				c.mu.Lock()
				if c.fatal == nil {
					c.fatal = &core.WireError{Code: core.CodeProtocol, Message: "rpc: " + err.Error()}
				}
				c.mu.Unlock()
				c.signal()
				continue
			}
			c.mu.Lock()
			if !c.rep.apply(ev, &c.changed) {
				c.queue = append(c.queue, ev)
			}
			c.mu.Unlock()
			c.signal()
		case f.Fatal != nil:
			c.mu.Lock()
			c.fatal = f.Fatal
			c.mu.Unlock()
			c.signal()
		case f.ID != 0:
			c.mu.Lock()
			ch := c.pending[f.ID]
			delete(c.pending, f.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

// fail ends the client after its connection failed or was closed: every
// waiting call returns errClosed. A connection the client did not close
// itself is lost, and the model with it: that is fatal, so the next call
// panics (and the TUI's own recovery restores the terminal) rather than
// the TUI running on against a model it cannot reach.
func (c *Client) fail(err error) {
	c.mu.Lock()
	c.closed = true
	lost := !c.closing && c.fatal == nil
	if lost {
		c.fatal = &core.WireError{Code: core.CodeError, Message: "rpc: connection to the model lost: " + err.Error()}
	}
	c.mu.Unlock()
	c.nc.Close()
	if lost {
		log.For("rpc").Warn("client.connection_lost", "err", err)
		c.signal()
	}
	c.closeOnce.Do(func() { close(c.done) })
}

// signal wakes the client's user: at most one wake waits.
func (c *Client) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Wakes signals that events arrived for Sync. Wakes coalesce; Close
// closes it.
func (c *Client) Wakes() <-chan struct{} { return c.wake }

// Close ends the connection and waits for the reader, the only sender on
// Wakes, then closes Wakes.
func (c *Client) Close() {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.nc.Close()
	<-c.readerDone
	c.wakeOnce.Do(func() { close(c.wake) })
}

// write sends one frame.
func (c *Client) write(f Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(f)
}

// checkFatalLocked re-raises the model's panic. c.mu is held, and released
// by the caller's defer: a caller that unlocks by hand copies c.fatal and
// panics after unlocking instead.
func (c *Client) checkFatalLocked() {
	if c.fatal != nil {
		panic(c.fatal)
	}
}

// request sends method with params, waits for its reply and decodes its
// result into result; it returns the method's error. A panic the model
// raised is re-raised here.
func (c *Client) request(method string, params, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("rpc: encode %s: %w", method, err)
	}
	c.mu.Lock()
	fatal, closed := c.fatal, c.closed
	var id uint64
	var ch chan Frame
	if fatal == nil && !closed {
		c.nextID++
		id = c.nextID
		ch = make(chan Frame, 1)
		c.pending[id] = ch
	}
	c.mu.Unlock()
	if fatal != nil {
		panic(fatal)
	}
	if closed {
		log.For("rpc").Warn("client.call_after_close", "method", method)
		return errClosed
	}
	if err := c.write(Frame{ID: id, Method: method, Params: data}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errClosed
	}
	var reply Frame
	select {
	case reply = <-ch:
	case <-c.done:
		return errClosed
	}
	if reply.Error != nil && (reply.Error.Code == core.CodePanic || reply.Error.Code == core.CodeProtocol) {
		// A panic ends the model; a protocol error means the two sides
		// disagree about the wire. Neither can be told to the caller as the
		// method's own error.
		c.mu.Lock()
		c.fatal = reply.Error
		c.mu.Unlock()
		panic(reply.Error)
	}
	if result != nil && len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("rpc: decode %s result: %w", method, err)
		}
	}
	return core.FromWire(reply.Error)
}

// requestNoErr is request for a method that has no error to return: a
// failure (the connection gone, a reply that will not decode, an error the
// server answered with) is logged, not discarded.
func (c *Client) requestNoErr(method string, params, result any) {
	if err := c.request(method, params, result); err != nil {
		log.For("rpc").Warn("client.request_failed", "method", method, "err", err)
	}
}

// cast sends method with params one way.
func (c *Client) cast(method string, params any) {
	data, err := json.Marshal(params)
	if err != nil {
		log.For("rpc").Error("client.encode_failed", "method", method, "err", err)
		return
	}
	c.mu.Lock()
	fatal, closed := c.fatal, c.closed
	c.mu.Unlock()
	if fatal != nil {
		panic(fatal)
	}
	if closed {
		return
	}
	_ = c.write(Frame{Method: method, Params: data})
	if c.synchronous {
		_ = c.ping()
	}
}

// ping is the barrier: when it returns, everything the server published
// before answering it is in the replica.
func (c *Client) ping() error { return c.request(pingMethod, struct{}{}, nil) }

// local runs f on the replica, after a ping when synchronous.
func (c *Client) local(f func(*replica)) {
	if c.synchronous {
		_ = c.ping()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkFatalLocked()
	f(&c.rep)
}

// Sync returns the events received since the last Sync (rpc:client):
// first the state events, coalesced to the newest state, in the model's
// order, then the others in the order they arrived.
func (c *Client) Sync() []core.Event {
	var out []core.Event
	c.local(func(r *replica) {
		out = append(r.events(c.changed), c.queue...)
		c.changed, c.queue = state{}, nil
	})
	return out
}

var _ core.Core = (*Client)(nil)
