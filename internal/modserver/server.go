// Package modserver serves the loom mod's HTTP API (session/claudemod) on the
// daemon's mod socket: POST /v<n>/hello, GET /v<n>/inbox (a long poll) and
// POST /v<n>/events, n being claudemod.Version, each naming the launch the
// mod runs in. It holds no state: every request goes to the model through
// core.ModHost.
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
	"github.com/aidan-bailey/loom/session/claudemod"
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
	http     *http.Server
	stopping chan struct{}
	once     sync.Once

	// mu guards the socket it listens on now: Watch moves it.
	mu sync.Mutex
	// path is the socket's path, ln its listener, and file the socket file
	// as listened on, to tell it from one put in its place (os.SameFile).
	path   string
	ln     net.Listener
	file   os.FileInfo
	closed bool
}

// route is method's pattern for path under the API version the module
// speaks: the module builds its URLs from the same Version, so a bump moves
// both.
func route(method, path string) string {
	return fmt.Sprintf("%s /v%d%s", method, claudemod.Version, path)
}

// Listen serves the mod's API on a unix socket at path (mode 0600; a stale
// file there is replaced), with host behind it and empty polls held for hold.
// Once it listens it tells host (ModsServing): no mod could reach the daemon
// before, so every launch's grace runs from then at the earliest.
func Listen(path string, host core.ModHost, hold time.Duration) (*Server, error) {
	ln, file, err := listenPrivate(path)
	if err != nil {
		return nil, err
	}
	s := &Server{host: host, hold: hold, path: path, ln: ln, file: file, stopping: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc(route("POST", "/hello"), s.hello)
	mux.HandleFunc(route("GET", "/inbox"), s.inbox)
	mux.HandleFunc(route("POST", "/events"), s.events)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go s.serve(ln)
	host.ModsServing()
	return s, nil
}

// listenPrivate listens on a unix socket at path, replacing a stale file
// there (the daemon's lock proves no live daemon uses it), mode 0600, and
// returns the socket file as listened on. Closing the listener leaves the
// path alone: only the server removes it, and only while it is still its own
// (removeOwn).
func listenPrivate(path string) (net.Listener, os.FileInfo, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("modserver: listen on %s: %w", path, err)
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, nil, fmt.Errorf("modserver: make %s private: %w", path, err)
	}
	return ln, stat(path), nil
}

// serve serves ln until it closes: a listener Watch replaced closes, which is
// no failure, and so does every listener at Close.
func (s *Server) serve(ln net.Listener) {
	err := s.http.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.For("modserver").Error("modserver.serve_failed", "err", err)
	}
}

// Watch keeps the socket at path: it listens again when the socket file it
// listens on is gone or replaced (a tmp cleaner, a runtime dir removed at the
// last logout), or when path is not where it listens (the daemon's own socket
// moved, and the mod socket goes beside it). An intact socket at path is left
// alone. A new listener serves through the same http.Server, the old one
// closes (connections it accepted stay open), and the model hears the mod is
// served again (ModsServing), since no mod could reach it meanwhile. It
// returns the listen's error, keeping the old listener; it does nothing after
// Close.
func (s *Server) Watch(path string) error {
	s.mu.Lock()
	if s.closed || path == s.path && sameFile(s.file, s.path) {
		s.mu.Unlock()
		return nil
	}
	ln, file, err := listenPrivate(path)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	old, oldPath, oldFile := s.ln, s.path, s.file
	s.ln, s.path, s.file = ln, path, file
	_ = old.Close()
	if oldPath != path {
		removeOwn(oldFile, oldPath)
	}
	go s.serve(ln)
	s.mu.Unlock()
	s.host.ModsServing()
	return nil
}

// Close ends every held poll with 503, stops serving and removes the socket,
// if it is still its own. It is idempotent, and a no-op on nil.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		file, path := s.file, s.path
		s.mu.Unlock()
		close(s.stopping)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		removeOwn(file, path)
	})
}

// stat is path's file info, nil when it can't be read.
func stat(path string) os.FileInfo {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	return info
}

// sameFile reports whether path is still the socket file listened on, not
// gone or another put in its place.
func sameFile(file os.FileInfo, path string) bool {
	now := stat(path)
	return file != nil && now != nil && os.SameFile(file, now)
}

// removeOwn removes path when it is still the socket file listened on.
func removeOwn(file os.FileInfo, path string) {
	if sameFile(file, path) {
		_ = os.Remove(path)
	}
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
