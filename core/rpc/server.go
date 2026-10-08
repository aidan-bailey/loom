package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// Backend is what a Server serves: the model's Core, a Sync and the whole
// state in one call for a client that connects (SyncAndSnapshot), and its
// wakes (core.Loop has all three).
type Backend interface {
	core.Core
	SyncAndSnapshot() (published, snapshot []core.Event)
	Wakes() <-chan struct{}
}

// Server serves a Backend to any number of connections. After every call
// and every wake it publishes (Backend.Sync) to every connection, and a
// request's reply follows the events its call produced. A connection is
// sent the whole state (Backend.SyncAndSnapshot) when it connects.
type Server struct {
	b Backend

	// mu orders publishing: a Sync and the frames it sends, the
	// connections, the fatal error, and each reply after the publish that
	// follows its call.
	mu sync.Mutex
	// conns are the connections that have said hello and been sent the
	// snapshot, which publishing reaches. open is every connection Serve
	// was given and not yet finished with, hello or not, which Close ends.
	conns  map[*serverConn]bool
	open   map[uint64]io.ReadWriteCloser
	nextNC uint64
	fatal  *core.WireError

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewServer serves b; it starts publishing on b's wakes.
func NewServer(b Backend) *Server {
	s := &Server{b: b, conns: map[*serverConn]bool{}, open: map[uint64]io.ReadWriteCloser{}, done: make(chan struct{})}
	s.wg.Add(1)
	go s.wakeLoop()
	return s
}

// wakeLoop publishes on every wake, until the backend stops or the server
// closes.
func (s *Server) wakeLoop() {
	defer s.wg.Done()
	for {
		select {
		case _, ok := <-s.b.Wakes():
			if !ok {
				return
			}
			s.mu.Lock()
			s.publishLocked()
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

// Close ends every connection and stops publishing.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		close(s.done)
		for _, nc := range s.open {
			nc.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
}

// Serve serves nc on a goroutine of its own until it closes (serveConn).
// After Close it closes nc at once and serves nothing.
func (s *Server) Serve(nc io.ReadWriteCloser) {
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		nc.Close()
		return
	default:
	}
	s.nextNC++
	n := s.nextNC
	s.open[n] = nc
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.open, n)
			s.mu.Unlock()
		}()
		s.serveConn(nc)
	}()
}

// serveConn serves one connection until it closes: the hello, the
// snapshot, then each request and cast in the order they arrive.
func (s *Server) serveConn(nc io.ReadWriteCloser) {
	c := newServerConn(nc)
	defer c.close()
	dec := json.NewDecoder(nc)
	var hello Frame
	if err := dec.Decode(&hello); err != nil {
		return
	}
	if hello.Hello == nil || hello.Hello.Protocol != Protocol {
		c.sendFrame(Frame{Error: mismatchError()})
		return
	}
	c.sendFrame(Frame{Hello: &Hello{Protocol: Protocol, Build: build()}})

	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
	}
	// The snapshot is the model as it is now, but the next Sync diffs
	// against the model as of the last one: a change made in between and
	// undone before the next publish shows in no diff, and would stay in
	// this replica for good. So the backend runs the two as one call, the
	// Sync's events go to the connections already here, and the snapshot to
	// this one, which then starts from the state the next Sync diffs against.
	if s.fatal == nil {
		published, snap, p := s.syncAndSnapshot()
		if p != nil {
			s.setFatalLocked(p)
		} else {
			s.sendLocked(published)
		}
		if s.fatal == nil {
			for _, ev := range snap {
				f, err := encodeEvent(ev)
				if err != nil {
					s.setFatalLocked(encodeFatal(ev, err))
					break
				}
				c.enqueue(f, coalesceKey(ev))
			}
		}
	}
	if s.fatal != nil {
		c.sendFrame(Frame{Fatal: s.fatal})
	}
	s.conns[c] = true
	s.mu.Unlock()

	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			break
		}
		s.handle(c, f)
	}
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// handle runs one request, then publishes, then replies; or runs one
// cast, which publishes nothing: a cast changes no published state (it
// marks output, names the selection, or starts jobs, whose results
// publish when they land, on the loop's wake), and pane events cast up to
// ~60 times a second per session.
func (s *Server) handle(c *serverConn, f Frame) {
	var result any = struct{}{}
	var err error
	if f.Method != pingMethod {
		var found bool
		result, err, found = s.call(f.Method, f.Params)
		if !found {
			err = &core.WireError{Code: core.CodeUnsupported, Message: fmt.Sprintf("rpc: unknown method %q", f.Method)}
		}
	}
	if f.ID == 0 {
		if err != nil {
			log.For("rpc").Warn("server.cast_failed", "method", f.Method, "err", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked()
	c.sendReply(f.ID, result, err)
}

// call dispatches method, turning a panic into a fatal error that is also
// the call's error.
func (s *Server) call(method string, params json.RawMessage) (result any, err error, found bool) {
	defer func() {
		if r := recover(); r != nil {
			p := panicWire(r)
			s.mu.Lock()
			s.setFatalLocked(p)
			s.mu.Unlock()
			result, err, found = nil, p, true
		}
	}()
	return dispatch(s.b, method, params)
}

// syncAndSnapshot is the backend's SyncAndSnapshot, or the panic it raised.
func (s *Server) syncAndSnapshot() (published, snapshot []core.Event, p *core.WireError) {
	defer func() {
		if r := recover(); r != nil {
			published, snapshot, p = nil, nil, panicWire(r)
		}
	}()
	published, snapshot = s.b.SyncAndSnapshot()
	return published, snapshot, nil
}

// publishLocked sends what the backend produced since the last publish to
// every connection. s.mu is held.
func (s *Server) publishLocked() {
	if s.fatal != nil {
		return
	}
	events, p := s.sync()
	if p != nil {
		s.setFatalLocked(p)
		return
	}
	s.sendLocked(events)
}

// sendLocked sends events to every connection; one that will not encode is
// fatal. s.mu is held.
func (s *Server) sendLocked(events []core.Event) {
	for _, ev := range events {
		f, err := encodeEvent(ev)
		if err != nil {
			s.setFatalLocked(encodeFatal(ev, err))
			return
		}
		for c := range s.conns {
			c.enqueue(f, coalesceKey(ev))
		}
	}
}

// sync is the backend's Sync, or the panic it raised.
func (s *Server) sync() (events []core.Event, p *core.WireError) {
	defer func() {
		if r := recover(); r != nil {
			events, p = nil, panicWire(r)
		}
	}()
	return s.b.Sync(), nil
}

// setFatalLocked records the backend's panic and tells every connection.
// s.mu is held.
func (s *Server) setFatalLocked(p *core.WireError) {
	if s.fatal != nil {
		return
	}
	s.fatal = p
	for c := range s.conns {
		c.sendFrame(Frame{Fatal: p})
	}
}

// encodeFatal is the fatal error for an event that will not encode. Dropping
// it would leave a client waiting for it (a Reply) or holding stale state,
// so the model is as good as gone to every client.
func encodeFatal(ev core.Event, err error) *core.WireError {
	log.For("rpc").Error("server.encode_event_failed", "event", fmt.Sprintf("%T", ev), "err", err)
	return &core.WireError{Code: core.CodeProtocol, Message: fmt.Sprintf("rpc: encode %T: %v", ev, err)}
}

// panicWire is a recovered panic as a wire error: a core.LoopPanic keeps
// its value and stack in its message.
func panicWire(r any) *core.WireError {
	msg := fmt.Sprint(r)
	if err, ok := r.(error); ok {
		msg = err.Error()
	}
	return &core.WireError{Code: core.CodePanic, Message: msg}
}

// coalesceKey names a state event's slot in a connection's queue: a newer
// one replaces an older one still queued, which a client would only
// overwrite. Other events keep their place ("").
func coalesceKey(ev core.Event) string {
	switch ev := ev.(type) {
	case core.WorkspacesChanged:
		return "workspaces"
	case core.ModelChanged:
		return "model"
	case core.AccountsChanged:
		return "accounts"
	case core.GitHubChanged:
		return "github"
	case core.ViewsChanged:
		return fmt.Sprintf("views:%d", ev.WS)
	}
	return ""
}
