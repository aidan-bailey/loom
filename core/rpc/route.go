package rpc

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/core"
)

// A server serves several clients, each numbering its requests itself from
// 1, so request IDs collide across clients. The server keeps them apart by
// putting its connection's number in the high 32 bits of every request ID it
// hands the model (tagReq), and takes it out again on the way back
// (forConn): a request's Reply, and the events it caused, reach the client
// that made it with that client's own ID. Nothing is remembered per
// request: a Create's Reply comes at once and its Started much later, so a
// table would never know when to forget an entry.
const connShift = 32

// tagReq is the tag dispatch applies to connection n's requests: a client's
// request ID, which must be below 2^32, becomes the server's. 0, a request
// that wants no Reply, stays 0.
func tagReq(n uint32) func(*core.ReqID) error {
	return func(req *core.ReqID) error {
		if *req == 0 {
			return nil
		}
		if *req >= 1<<connShift {
			return &core.WireError{Code: core.CodeProtocol, Message: fmt.Sprintf("rpc: request id %d is not below 2^32", *req)}
		}
		*req |= core.ReqID(n) << connShift
		return nil
	}
}

// local is the server's request ID req as its connection numbered it, and
// whether connection n made the request. An ID no server tagged names no
// connection, and is everyone's.
func local(req core.ReqID, n uint32) (core.ReqID, bool) {
	owner := uint32(req >> connShift)
	if owner == 0 {
		return req, true
	}
	return req & (1<<connShift - 1), owner == n
}

// routed reports whether ev names a request, so that each connection is
// sent it as forConn says rather than as it is.
func routed(ev core.Event) bool {
	switch e := ev.(type) {
	case core.Reply:
		return e.Req != 0
	case core.Notice:
		return e.Req != 0
	case core.Started:
		return e.Req != 0
	case core.Recovered:
		return e.Req != 0
	}
	return false
}

// forConn is ev as connection n is sent it, and whether it is sent at all. A
// Reply, and the Notice of a request's job, go to the client that made the
// request alone, with its own request ID. Started and Recovered go to every
// client, since each attaches the session's pane, but name the request only
// to the one that made it, which alone selects the row and attaches inline.
// Every event type with a request ID is here (TestForConn_CoversEveryEventNamingARequest).
func forConn(ev core.Event, n uint32) (core.Event, bool) {
	switch e := ev.(type) {
	case core.Reply:
		req, mine := local(e.Req, n)
		e.Req = req
		return e, mine
	case core.Notice:
		req, mine := local(e.Req, n)
		e.Req = req
		return e, mine
	case core.Started:
		e.Req = reqFor(e.Req, n)
		return e, true
	case core.Recovered:
		e.Req = reqFor(e.Req, n)
		return e, true
	}
	return ev, true
}

// reqFor is req as connection n is told it: its own request's ID, 0 for
// another connection's.
func reqFor(req core.ReqID, n uint32) core.ReqID {
	if r, mine := local(req, n); mine {
		return r
	}
	return 0
}

// connBackend is the backend as connection c's calls reach it: its
// SetSelected names c's own selected row, which the server merges with
// every other connection's (Server.setSelected).
type connBackend struct {
	Backend
	s *Server
	c *serverConn
}

func (b connBackend) SetSelected(id core.InstanceID) { b.s.setSelected(b.c, id) }

// setSelected records c's selected row (0 for none) and tells the model
// every connection's (Backend.SetSelection), so its probe refreshes the
// full diff of each client's selection, not only the last one named.
func (s *Server) setSelected(c *serverConn, id core.InstanceID) {
	s.selMu.Lock()
	defer s.selMu.Unlock()
	if id == 0 {
		delete(s.selected, c)
	} else {
		s.selected[c] = id
	}
	s.b.SetSelection(s.selectionLocked())
}

// dropSelection forgets a closed connection's selected row. A panic the
// backend raises is fatal, as in a call: nothing else recovers here.
func (s *Server) dropSelection(c *serverConn) {
	s.selMu.Lock()
	defer s.selMu.Unlock()
	if _, ok := s.selected[c]; !ok {
		return
	}
	delete(s.selected, c)
	defer func() {
		if r := recover(); r != nil {
			s.mu.Lock()
			s.setFatalLocked(panicWire(r))
			s.mu.Unlock()
		}
	}()
	s.b.SetSelection(s.selectionLocked())
}

// selectionLocked is every connection's selected row, in the order the
// connections were made, without repeats. s.selMu is held.
func (s *Server) selectionLocked() []core.InstanceID {
	conns := make([]*serverConn, 0, len(s.selected))
	for c := range s.selected {
		conns = append(conns, c)
	}
	slices.SortFunc(conns, func(a, b *serverConn) int { return cmp.Compare(a.n, b.n) })
	var ids []core.InstanceID
	for _, c := range conns {
		if id := s.selected[c]; !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}
