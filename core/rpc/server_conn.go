package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
)

// closeFlushTimeout bounds how long close waits for the peer to take what
// is queued: a peer that never reads must not hold its connection's
// goroutine, and so a Server's Close, for good. A var so a test can shrink
// it.
var closeFlushTimeout = 5 * time.Second

// serverConn is one connection's outbound side: a queue a writer
// goroutine drains, so a server never blocks on a slow client. A state
// event replaces an older one of the same key still queued, in its place,
// so the queue holds at most one of each; replies and other events keep
// their order.
type serverConn struct {
	nc io.ReadWriteCloser
	// n is the connection's number, which the request IDs it sends the
	// model carry (tagReq).
	n uint32
	// web and cli are set from the hello (RoleWeb, RoleCLI) before the
	// connection joins Server.conns, so every read of them under Server.mu
	// follows the write: neither the daemon's web layer nor a CLI command is
	// a client for the notice backlog (Server.clientsLocked), a web
	// connection's own request's Notice reaches no client, and a CLI
	// command is sent no other connection's request's Notice.
	web, cli bool

	mu     sync.Mutex
	queue  []queued
	closed bool
	signal chan struct{}
	done   chan struct{}
}

type queued struct {
	key   string
	frame Frame
}

func newServerConn(nc io.ReadWriteCloser) *serverConn {
	c := &serverConn{nc: nc, signal: make(chan struct{}, 1), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

// enqueue queues f, replacing a queued frame with the same non-empty key.
func (c *serverConn) enqueue(f Frame, key string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	replaced := false
	if key != "" {
		for i := range c.queue {
			if c.queue[i].key == key {
				c.queue[i].frame, replaced = f, true
				break
			}
		}
	}
	if !replaced {
		c.queue = append(c.queue, queued{key: key, frame: f})
	}
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// sendFrame queues f, never coalesced.
func (c *serverConn) sendFrame(f Frame) { c.enqueue(f, "") }

// sendReply queues the reply to request id.
func (c *serverConn) sendReply(id uint64, result any, err error) {
	f := Frame{ID: id, Error: core.ToWire(err)}
	if result != nil {
		data, merr := json.Marshal(result)
		if merr != nil {
			f.Result, f.Error = nil, &core.WireError{Code: core.CodeProtocol, Message: "rpc: encode result: " + merr.Error()}
		} else {
			f.Result = data
		}
	}
	c.sendFrame(f)
}

// writeLoop writes queued frames until the connection closes, each as
// appendFrame lays it out, in one Write.
func (c *serverConn) writeLoop() {
	defer close(c.done)
	var buf []byte
	for {
		c.mu.Lock()
		batch := c.queue
		c.queue = nil
		closed := c.closed
		c.mu.Unlock()
		for _, q := range batch {
			var err error
			if buf, err = appendFrame(buf[:0], q.frame); err == nil {
				_, err = c.nc.Write(buf)
			}
			if err != nil {
				c.nc.Close()
				return
			}
		}
		if cap(buf) > maxKeptFrameBuffer {
			buf = nil
		}
		if closed && len(batch) == 0 {
			return
		}
		if len(batch) == 0 {
			<-c.signal
		}
	}
}

// flush stops queuing and lets the writer write what is queued, for up to
// closeFlushTimeout; then the writer has stopped. A peer not reading by
// then has its connection closed under the blocked write.
func (c *serverConn) flush() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
	select {
	case <-c.done:
	case <-time.After(closeFlushTimeout):
		c.nc.Close() // the peer is not reading: closing fails the blocked write
		<-c.done
	}
}

// close flushes what is queued (flush) and closes the connection.
func (c *serverConn) close() {
	c.flush()
	c.nc.Close()
}

// maxKeptFrameBuffer is the largest write buffer a connection keeps between
// batches; a larger one, grown for a large frame, is let go.
const maxKeptFrameBuffer = 256 << 10

// appendFrame appends f as one line of the wire: the bytes json.Encoder
// writes for it (Frame's fields in order, each omitted when empty, then a
// newline), with Params, Result and Data copied as they are. encoding/json
// validates and compacts every json.RawMessage it encodes, byte by byte,
// and the server's are json.Marshal output already (compact, HTML-escaped):
// an event's data, a workspace's views with their diffs among them, went
// through that once per connection, most of a busy daemon's CPU.
func appendFrame(buf []byte, f Frame) ([]byte, error) {
	buf = append(buf, '{')
	first := true
	key := func(name string) {
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = append(buf, '"')
		buf = append(buf, name...)
		buf = append(buf, '"', ':')
	}
	value := func(name string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("rpc: encode frame field %s: %w", name, err)
		}
		key(name)
		buf = append(buf, b...)
		return nil
	}
	raw := func(name string, r json.RawMessage) {
		if len(r) > 0 {
			key(name)
			buf = append(buf, r...)
		}
	}
	if f.Hello != nil {
		if err := value("hello", f.Hello); err != nil {
			return nil, err
		}
	}
	if f.ID != 0 {
		key("id")
		buf = strconv.AppendUint(buf, f.ID, 10)
	}
	if f.Method != "" {
		if err := value("method", f.Method); err != nil {
			return nil, err
		}
	}
	raw("params", f.Params)
	raw("result", f.Result)
	if f.Error != nil {
		if err := value("error", f.Error); err != nil {
			return nil, err
		}
	}
	if f.Event != "" {
		if err := value("event", f.Event); err != nil {
			return nil, err
		}
	}
	raw("data", f.Data)
	if f.Fatal != nil {
		if err := value("fatal", f.Fatal); err != nil {
			return nil, err
		}
	}
	if f.Bye != "" {
		if err := value("bye", f.Bye); err != nil {
			return nil, err
		}
	}
	return append(buf, '}', '\n'), nil
}
