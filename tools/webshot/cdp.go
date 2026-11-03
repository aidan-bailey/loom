package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// cdpClient speaks the Chrome DevTools Protocol over Chromium's
// --remote-debugging-pipe: JSON messages, each ended by a NUL byte, written
// to the browser's fd 3 and read from its fd 4. A pipe needs no WebSocket and
// no port, so the tool stays on the standard library.
type cdpClient struct {
	w io.Writer

	mu      sync.Mutex
	next    int64
	pending map[int64]chan cdpMessage
	onEvent func(method, session string, params json.RawMessage)
	err     error // set once the read loop ends; every later call fails with it

	done chan struct{}
}

// cdpMessage is one message from the browser: a reply (ID set) or an event
// (Method set).
type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// newCDPClient reads replies and events from r until it fails, and writes
// calls to w. onEvent, which may be nil, runs on the read loop for every
// event, so it must not call back into the client.
func newCDPClient(r io.Reader, w io.Writer, onEvent func(method, session string, params json.RawMessage)) *cdpClient {
	c := &cdpClient{w: w, pending: make(map[int64]chan cdpMessage), onEvent: onEvent, done: make(chan struct{})}
	go c.read(bufio.NewReader(r))
	return c
}

func (c *cdpClient) read(r *bufio.Reader) {
	defer close(c.done)
	for {
		frame, err := r.ReadBytes(0)
		if err != nil {
			c.fail(fmt.Errorf("read from the browser: %w", err))
			return
		}
		var m cdpMessage
		if err := json.Unmarshal(frame[:len(frame)-1], &m); err != nil {
			c.fail(fmt.Errorf("decode a message from the browser: %w", err))
			return
		}
		if m.ID == 0 {
			if c.onEvent != nil && m.Method != "" {
				c.onEvent(m.Method, m.SessionID, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ok {
			ch <- m
		}
	}
}

// fail ends every waiting call with err.
func (c *cdpClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
	for id, ch := range c.pending {
		ch <- cdpMessage{ID: id, Error: &cdpError{Code: -1, Message: c.err.Error()}}
		delete(c.pending, id)
	}
}

// call sends method with params to the browser (session "") or to an
// attached target, waits for its reply, and decodes the result into out
// unless out is nil.
func (c *cdpClient) call(ctx context.Context, session, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("%s: encode: %w", method, err)
	}
	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, c.err)
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	msg, err := json.Marshal(cdpMessage{ID: id, Method: method, SessionID: session, Params: body})
	if err == nil {
		_, err = c.w.Write(append(msg, 0))
	}
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: send: %w", method, err)
	}
	c.mu.Unlock()

	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %w", method, m.Error)
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(m.Result, out); err != nil {
			return fmt.Errorf("%s: decode the result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}
