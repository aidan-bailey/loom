package rpc

import (
	"encoding/json"
	"io"
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

// writeLoop writes queued frames until the connection closes.
func (c *serverConn) writeLoop() {
	defer close(c.done)
	enc := json.NewEncoder(c.nc)
	for {
		c.mu.Lock()
		batch := c.queue
		c.queue = nil
		closed := c.closed
		c.mu.Unlock()
		for _, q := range batch {
			if err := enc.Encode(q.frame); err != nil {
				c.nc.Close()
				return
			}
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
