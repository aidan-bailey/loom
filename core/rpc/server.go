package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// Backend is what a Server serves: the model's Core, a Sync and the whole
// state in one call for a client that connects (SyncAndSnapshot), every
// client's selected row at once (SetSelection), and its wakes (core.Loop
// has them all).
type Backend interface {
	core.Core
	SyncAndSnapshot() (published, snapshot []core.Event)
	SetSelection(ids []core.InstanceID)
	Wakes() <-chan struct{}
}

// helloTimeout bounds how long a connection may take to say hello.
var helloTimeout = 10 * time.Second

// maxBacklog bounds the notices kept for the next connection.
const maxBacklog = 50

// Server serves a Backend to any number of connections. After every call
// and every wake it publishes (Backend.Sync) to every connection, but an
// event naming a request as forConn routes it: a Reply, or a request's
// Notice, only to the connection that made the request, and Started or
// Recovered to every connection, naming the request to that one alone. A
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
	// fatalCh is closed when fatal is set, so whatever runs the server (a
	// daemon) can stop: the model is as good as gone.
	fatalCh chan struct{}
	// backlog holds the notices that reached no client: raised while none
	// was connected, or the Notice of a request whose client has gone. The
	// next connection is sent them after its snapshot (keep).
	backlog []core.Notice
	// tmux is the tmux server the model's sessions run on, for the hello
	// (SetTmux).
	tmux string

	// selMu orders the selection: each connection's selected row (its
	// SetSelected), merged for the model (setSelected).
	selMu    sync.Mutex
	selected map[*serverConn]core.InstanceID

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewServer serves b; it starts publishing on b's wakes.
func NewServer(b Backend) *Server {
	s := &Server{b: b, conns: map[*serverConn]bool{}, open: map[uint64]io.ReadWriteCloser{},
		selected: map[*serverConn]core.InstanceID{}, fatalCh: make(chan struct{}), done: make(chan struct{})}
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

// SetTmux names, in the hello every later connection is sent, the tmux
// server the model's sessions run on (its socket's path): the daemon's,
// which its clients must use too, whatever their own environment says.
func (s *Server) SetTmux(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tmux = path
}

// hello is the hello the server sends: its build and tmux server.
func (s *Server) hello() *Hello {
	h := Self()
	s.mu.Lock()
	h.Tmux = s.tmux
	s.mu.Unlock()
	return &h
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
		s.serveConn(nc, uint32(n))
	}()
}

// serveConn serves connection n until it closes: the hello, the snapshot,
// then each request and cast in the order they arrive. n names the
// connection in the request IDs it hands the model (tagReq). The server's
// hello goes even to a peer of another protocol, ahead of the mismatch
// error, so the peer can tell which build is newer.
func (s *Server) serveConn(nc io.ReadWriteCloser, n uint32) {
	c := newServerConn(nc)
	c.n = n
	defer c.close()
	dec := json.NewDecoder(nc)
	// A peer that never says hello must not hold a server goroutine (and
	// its connection) for good.
	deadline, _ := nc.(interface{ SetReadDeadline(time.Time) error })
	if deadline != nil {
		_ = deadline.SetReadDeadline(time.Now().Add(helloTimeout))
	}
	var hello Frame
	if err := dec.Decode(&hello); err != nil {
		return
	}
	if deadline != nil {
		_ = deadline.SetReadDeadline(time.Time{})
	}
	c.sendFrame(Frame{Hello: s.hello()})
	if hello.Hello == nil || hello.Hello.Protocol != Protocol {
		c.sendFrame(Frame{Error: mismatchError()})
		return
	}

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
	} else {
		s.flushBacklogLocked(c)
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
	s.dropSelection(c)
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
		result, err, found = s.call(c, f.Method, f.Params)
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

// call dispatches connection c's call of method, turning a panic into a
// fatal error that is also the call's error.
func (s *Server) call(c *serverConn, method string, params json.RawMessage) (result any, err error, found bool) {
	defer func() {
		if r := recover(); r != nil {
			p := panicWire(r)
			s.mu.Lock()
			s.setFatalLocked(p)
			s.mu.Unlock()
			result, err, found = nil, p, true
		}
	}()
	return dispatch(connBackend{Backend: s.b, s: s, c: c}, method, params, tagReq(c.n))
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

// sendLocked sends events to every connection, an event naming a request
// as forConn says; one that will not encode is fatal. A Notice that reaches
// no connection is kept for the next (keepLocked). s.mu is held.
func (s *Server) sendLocked(events []core.Event) {
	for _, ev := range events {
		notice, isNotice := ev.(core.Notice)
		if routed(ev) {
			sent := false
			for c := range s.conns {
				e, ok := forConn(ev, c.n)
				if !ok {
					continue
				}
				f, err := encodeEvent(e)
				if err != nil {
					s.setFatalLocked(encodeFatal(e, err))
					return
				}
				c.enqueue(f, "")
				sent = true
			}
			if isNotice && !sent {
				// Its client has gone: whoever comes next is told.
				notice.Req = 0
				s.keepLocked(notice)
			}
			continue
		}
		if isNotice && len(s.conns) == 0 {
			s.keepLocked(notice)
			continue
		}
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

// Keep holds notices for the next connection, as if raised while none was
// connected: a daemon's boot raises them before any client can be
// (core.Model.Boot).
func (s *Server) Keep(notices ...core.Notice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range notices {
		s.keepLocked(n)
	}
}

// keepLocked adds n to the backlog, which holds the newest maxBacklog
// notices, and logs it, so even a notice no client ever reads is on
// record. s.mu is held.
func (s *Server) keepLocked(n core.Notice) {
	log.For("rpc").Info("server.notice_kept", "info", n.Info, "err", n.Err)
	s.backlog = append(s.backlog, n)
	if over := len(s.backlog) - maxBacklog; over > 0 {
		log.For("rpc").Warn("server.notice_backlog_full", "dropped", over)
		s.backlog = s.backlog[over:]
	}
}

// flushBacklogLocked sends c the notices no connection was there for, and
// forgets them. s.mu is held.
func (s *Server) flushBacklogLocked(c *serverConn) {
	for _, n := range s.backlog {
		f, err := encodeEvent(n)
		if err != nil {
			s.setFatalLocked(encodeFatal(n, err))
			return
		}
		c.enqueue(f, "")
	}
	s.backlog = nil
}

// Fatal is closed once the model is as good as gone (it panicked, or an
// event would not encode): whatever runs the server should stop.
// FatalError says why.
func (s *Server) Fatal() <-chan struct{} { return s.fatalCh }

// FatalError is the error that made the server fatal, nil while it is not.
func (s *Server) FatalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fatal == nil {
		return nil
	}
	return s.fatal
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
	close(s.fatalCh)
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
