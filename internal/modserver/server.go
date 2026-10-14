// Package modserver serves the loom mod's HTTP API (session/claudemod) on the
// daemon's mod socket: POST /v1/hello, GET /v1/inbox (a long poll) and POST
// /v1/events, each naming the launch the mod runs in. It holds no state: every
// request goes to the model through core.ModHost.
package modserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// DefaultHold is how long an empty inbox poll is held before it answers 204.
// Claude's host aborts a fetch at 30 s (plan package A measured a 29 s hold
// succeed and every longer one abort at 30 s), so the hold stays 5 s under
// that, leaving room for a slow loop and the mod's own round trip.
const DefaultHold = 24 * time.Second

// maxBody caps a request's body.
const maxBody = 1 << 20

// Server is the mod socket's HTTP server.
type Server struct {
	host     core.ModHost
	hold     time.Duration
	path     string
	http     *http.Server
	stopping chan struct{}
	once     sync.Once
}

// Listen serves the mod's API on a unix socket at path (mode 0600; a stale
// file there is replaced), with host behind it and empty polls held for hold.
func Listen(path string, host core.ModHost, hold time.Duration) (*Server, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("modserver: listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("modserver: make %s private: %w", path, err)
	}
	s := &Server{host: host, hold: hold, path: path, stopping: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/hello", s.hello)
	mux.HandleFunc("GET /v1/inbox", s.inbox)
	mux.HandleFunc("POST /v1/events", s.events)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.For("modserver").Error("modserver.serve_failed", "err", err)
		}
	}()
	return s, nil
}

// Close ends every held poll with 503, stops serving and removes the socket.
// It is idempotent, and a no-op on nil.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.stopping)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = os.Remove(s.path)
	})
}

type helloBody struct {
	Launch string `json:"launch"`
	core.ModHello
}

type eventsBody struct {
	Launch string          `json:"launch"`
	Events []core.ModEvent `json:"events"`
}

type inboxBody struct {
	Messages []core.ModMessage `json:"messages"`
}

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	var b helloBody
	if decode(w, r, &b) {
		answer(w, s.host.ModHello(b.Launch, b.ModHello))
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	var b eventsBody
	if decode(w, r, &b) {
		answer(w, s.host.ModEvents(b.Launch, b.Events))
	}
}

// inbox answers the messages waiting for the launch at once, or holds the
// poll until something may have changed (204), the hold ends (204) or the
// daemon stops (503).
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	msgs, wake, err := s.host.ModInbox(r.URL.Query().Get("launch"))
	switch {
	case err != nil:
		answer(w, err)
		return
	case wake == nil:
		http.Error(w, "the loom daemon is stopping", http.StatusServiceUnavailable)
		return
	case len(msgs) > 0:
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(inboxBody{Messages: msgs})
		return
	}
	hold := time.NewTimer(s.hold)
	defer hold.Stop()
	select {
	case <-wake:
		w.WriteHeader(http.StatusNoContent)
	case <-hold.C:
		w.WriteHeader(http.StatusNoContent)
	case <-s.stopping:
		http.Error(w, "the loom daemon is stopping", http.StatusServiceUnavailable)
	case <-r.Context().Done():
	}
}

// decode reads r's JSON body, at most maxBody bytes, into v, and answers 400
// when it can't.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// answer answers a call the model took: 204, 404 for a launch no session
// holds, 500 for anything else.
func answer(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, core.ErrModUnknownLaunch):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		log.For("modserver").Error("modserver.request_failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
