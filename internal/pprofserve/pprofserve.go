// Package pprofserve serves a loom process's runtime/pprof profiles over
// HTTP on a loopback address, when LOOM_PPROF asks for it, so a heap, CPU or
// goroutine profile of a running daemon or TUI can be read with
// `go tool pprof http://<addr>/debug/pprof/<name>`.
//
// It builds its handlers on runtime/pprof rather than importing
// net/http/pprof, whose init registers the profiler on http.DefaultServeMux:
// a server added later with a nil handler would then serve it too, and the
// web layer is reachable from outside the host.
package pprofserve

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// EnvVar names the environment variable that turns the profiler on: unset,
// empty, 0, off or false serve nothing; 1, on or true serve on 127.0.0.1 at
// a port the kernel picks; a loopback host:port serves there.
const EnvVar = "LOOM_PPROF"

// The CPU profile's length when the request names none, and the longest one
// it may ask for.
const (
	defaultCPUSeconds = 30
	maxCPUSeconds     = 300
)

// Server serves runtime/pprof profiles on a loopback listener.
type Server struct {
	ln  net.Listener
	srv *http.Server
}

// Parse reads an EnvVar value: the address to listen on and whether to
// serve at all. An address whose host is not loopback, or that names no
// host (which would bind every interface), is an error rather than served
// somewhere the user didn't mean.
func Parse(spec string) (addr string, on bool, err error) {
	spec = strings.TrimSpace(spec)
	switch strings.ToLower(spec) {
	case "", "0", "off", "false":
		return "", false, nil
	case "1", "on", "true":
		return "127.0.0.1:0", true, nil
	}
	host, _, err := net.SplitHostPort(spec)
	if err != nil {
		return "", false, fmt.Errorf("%s=%q: want 1 or a loopback host:port: %w", EnvVar, spec, err)
	}
	if !loopbackHost(host) {
		return "", false, fmt.Errorf("%s=%q: %q is not a loopback host", EnvVar, spec, host)
	}
	return spec, true, nil
}

// loopbackHost reports whether host, with no port or brackets, names this
// machine's loopback interface.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Start serves profiles at the address spec names (see Parse). It returns a
// nil Server and no error when spec turns the profiler off.
func Start(spec string) (*Server, error) {
	addr, on, err := Parse(spec)
	if err != nil || !on {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pprof: listen on %s: %w", addr, err)
	}
	// localhost resolves through the system's resolver: serve only what
	// actually bound to loopback.
	if tcp, ok := ln.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return nil, fmt.Errorf("pprof: %s bound to %s, not loopback", addr, ln.Addr())
	}
	s := &Server{ln: ln, srv: &http.Server{Handler: loopbackOnly(mux()), ReadHeaderTimeout: 10 * time.Second}}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.For("pprof").Error("pprof.serve_failed", "err", err.Error())
		}
	}()
	return s, nil
}

// FromEnv starts a server per EnvVar for the process named by role (daemon,
// tui) and logs its address, or why it refused one. It returns nil when the
// profiler is off or refused.
func FromEnv(role string) *Server {
	spec := os.Getenv(EnvVar)
	s, err := Start(spec)
	if err != nil {
		log.For("pprof").Warn("pprof.refused", "role", role, "spec", spec, "err", err.Error())
		return nil
	}
	if s == nil {
		return nil
	}
	log.For("pprof").Info("pprof.listening", "role", role, "addr", s.Addr(), "url", "http://"+s.Addr()+"/debug/pprof/")
	return s
}

// Addr is the address the server listens on, its port resolved.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops the server and drops its connections. A nil Server closes
// nothing.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	return s.srv.Close()
}

// loopbackOnly refuses a request whose Host is not loopback. A page loaded
// from elsewhere can reach a loopback port through DNS rebinding, but its
// requests carry its own host name.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if !loopbackHost(host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mux routes the index, the CPU profile and every named runtime/pprof
// profile.
func mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /debug/pprof/{$}", index)
	m.HandleFunc("GET /debug/pprof/profile", cpuProfile)
	m.HandleFunc("GET /debug/pprof/{name}", namedProfile)
	return m
}

// index lists what the server serves, as plain text.
func index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var b strings.Builder
	b.WriteString("go tool pprof http://<this address>/debug/pprof/<name>\n\n")
	fmt.Fprintf(&b, "%-14s CPU, ?seconds=N (default %d, at most %d)\n", "profile", defaultCPUSeconds, maxCPUSeconds)
	profiles := pprof.Profiles()
	slices.SortFunc(profiles, func(a, b *pprof.Profile) int { return strings.Compare(a.Name(), b.Name()) })
	for _, p := range profiles {
		fmt.Fprintf(&b, "%-14s %d now; ?debug=1 for text", p.Name(), p.Count())
		switch p.Name() {
		case "heap":
			b.WriteString(", ?gc=1 to collect first")
		case "goroutine":
			b.WriteString(", ?debug=2 for every stack")
		}
		b.WriteString("\n")
	}
	_, _ = w.Write([]byte(b.String()))
}

// namedProfile writes one runtime/pprof profile: proto, which
// `go tool pprof` reads, or text with ?debug=N.
func namedProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := pprof.Lookup(name)
	if p == nil {
		http.Error(w, "no profile "+strconv.Quote(name), http.StatusNotFound)
		return
	}
	debug, _ := strconv.Atoi(r.FormValue("debug"))
	if name == "heap" && r.FormValue("gc") != "" {
		runtime.GC()
	}
	if debug != 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	if err := p.WriteTo(w, debug); err != nil {
		log.For("pprof").Warn("pprof.write_failed", "profile", name, "err", err.Error())
	}
}

// cpuProfile records the CPU for ?seconds=N (default defaultCPUSeconds),
// ending early if the client goes away. Only one CPU profile can run in a
// process at a time.
func cpuProfile(w http.ResponseWriter, r *http.Request) {
	seconds := defaultCPUSeconds
	if v := r.FormValue("seconds"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCPUSeconds {
			http.Error(w, fmt.Sprintf("seconds must be 1 to %d", maxCPUSeconds), http.StatusBadRequest)
			return
		}
		seconds = n
	}
	if err := pprof.StartCPUProfile(w); err != nil {
		http.Error(w, "CPU profile not started: "+err.Error(), http.StatusConflict)
		return
	}
	t := time.NewTimer(time.Duration(seconds) * time.Second)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
	}
	pprof.StopCPUProfile()
}
