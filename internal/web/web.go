// Package web is the daemon's web layer: a client of the daemon's own
// rpc.Server (joined over net.Pipe as the web role) plus core.WebHost for
// device records, serving the loom PWA, a JSON API and a live stream on a
// loopback port that a Cloudflare tunnel reaches, and sending Web Push. It
// holds no session state: the model owns every session and every device. It
// logs under subsystem=web.
package web

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/log"
)

// sink is a part of the layer that follows the daemon's events. The drain
// hands every batch Sync returns to each sink in turn, on its own
// goroutine: a sink never blocks and never does network I/O.
type sink interface{ apply(evs []core.Event) }

// Layer is the web layer inside a running daemon: Start builds it and Close
// stops it. Its parts (the stream hub, the request tracker, the push sender)
// each follow the daemon's events as a sink and register their routes in mux.
type Layer struct {
	client   *rpc.Client
	host     core.WebHost
	hostname string
	build    string
	now      func() time.Time

	// rule is the origin rule of the listener now running, which guard
	// enforces; nil while none runs, and then guard refuses every request.
	rule atomic.Pointer[originRule]

	// bindMu guards the listener (srv) and closed, which bind, shutListener
	// and the unbinding take it for.
	bindMu sync.Mutex
	srv    *http.Server
	closed bool

	// applied, rev and reported are the drain's own: whether it has acted on
	// a published WebView yet, the Rev of the last one it acted on, and
	// whether it has told the host anything that turning the web off must
	// undo. Only the drain's goroutine touches them.
	applied  bool
	rev      uint64
	reported bool

	// done ends the drain, and drained is closed when the drain returns.
	// stopOnce runs the layer's stop once, whether Close or fail comes first.
	done     chan struct{}
	drained  chan struct{}
	stopOnce sync.Once

	// sinkMu guards sinks.
	sinkMu sync.Mutex

	sinks []sink

	hub *hub

	reqs *reqs

	push *pusher

	// onAction, when set, takes the place of the log for the result of each
	// action (record): a test seam.
	onAction func(a actionLog, result string)
}

// addSink adds s to the sinks the drain feeds. The layer's parts add theirs
// in startParts, before the drain begins; a sink added later sees the
// batches from then on.
func (l *Layer) addSink(s sink) {
	l.sinkMu.Lock()
	defer l.sinkMu.Unlock()
	l.sinks = append(l.sinks, s)
}

// mux is the layer's routes, one registrar per part.
func (l *Layer) mux() *http.ServeMux {
	m := http.NewServeMux()
	l.routePair(m)
	l.routeStream(m)
	l.routeAPI(m)
	l.routeStatic(m)
	l.routePush(m)
	return m
}

// startParts builds the layer's parts and adds their sinks, before the drain starts.
func (l *Layer) startParts() {
	l.hub = newHub(l)
	l.addSink(l.hub)
	l.reqs = newReqs(l)
	l.addSink(l.reqs)
	l.push = newPusher(l)
	l.addSink(l.push)
}

// stopParts stops the layer's parts, first in Close.
func (l *Layer) stopParts() {
	// Deferred, so a panic in an earlier part's close cannot leave the sender
	// pushing after the layer stopped.
	defer l.push.close()
	l.hub.close()
}

// Options configure Start.
type Options struct {
	Server   *rpc.Server  // the daemon's server, dialled over net.Pipe with rpc.DialAs(…, rpc.RoleWeb)
	Host     core.WebHost // the loop
	Hostname string       // shown in the app's header
	Build    string       // the daemon's build ID: in the snapshot and in sw.js
}

// drainGrace is how long Close waits for the drain to return before it
// closes the client under it.
const drainGrace = 5 * time.Second

// Start joins the daemon's server as the web layer's client, builds the
// layer's parts and starts draining the client. The drain applies the
// published settings, so the layer listens once web.json enables the web,
// and binds afresh whenever a ConfigureWeb has changed the view's Rev. A
// server that cannot be joined (it is stopping, say) is an error, and the
// daemon runs on without the web.
func Start(o Options) (*Layer, error) {
	if o.Server == nil || o.Host == nil {
		return nil, errors.New("web: Start needs the daemon's server and its host")
	}
	a, b := net.Pipe()
	o.Server.Serve(a)
	client, err := rpc.DialAs(b, rpc.RoleWeb)
	if err != nil {
		return nil, fmt.Errorf("web: join the daemon's server: %w", err)
	}
	l := &Layer{
		client:   client,
		host:     o.Host,
		hostname: o.Hostname,
		build:    o.Build,
		now:      time.Now,
		done:     make(chan struct{}),
		drained:  make(chan struct{}),
	}
	l.startParts()
	go l.drain()
	log.For("web").Info("web.started", "host", l.hostname, "build", l.build)
	return l, nil
}

// Close stops the layer: its parts first (stopParts: the streams hear their
// bye), then the listener, then the drain, then the layer's client. It is
// safe to call twice, concurrently, on a layer that failed or is failing
// (then it returns once that stop is complete), and on nil.
func (l *Layer) Close() {
	if l == nil {
		return
	}
	l.stop(nil)
}

// stop is the layer's one stop, shared by Close and fail: whichever comes
// first runs it, and any other caller waits until it is complete (sync.Once),
// so a fail racing a Close neither hangs nor closes anything twice. A non-nil
// cause is told to the host last, once the drain has returned: a binding the
// drain was making cannot report "serving" over it.
func (l *Layer) stop(cause error) {
	l.stopOnce.Do(func() {
		l.stopPartsSafely()
		close(l.done)
		l.shutListener()
		select {
		case <-l.drained:
		case <-time.After(drainGrace):
			log.For("web").Warn("web.drain_wedged")
		}
		l.client.Close()
		// Only now, once the drain has returned: a binding it was making cannot
		// report "serving" over the failure.
		if cause != nil {
			l.report("", cause)
		}
	})
}

// stopPartsSafely is stopParts that logs a panic instead of raising it into
// the daemon's stop.
func (l *Layer) stopPartsSafely() {
	defer func() {
		if r := recover(); r != nil {
			log.For("web").Error("web.stop_parts_panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	l.stopParts()
}

// drain follows the daemon as the TUI does: a batch of events on each wake
// (Sync). Every batch goes to each sink, and each published WebView is
// applied. A lost client ends the layer, since the model it follows is gone.
func (l *Layer) drain() {
	defer l.recoverPanic("drain")
	defer close(l.drained)
	l.applyWeb(l.client.Web())
	for {
		l.deliver(l.client.Sync())
		if err := l.client.Err(); err != nil {
			l.fail(fmt.Errorf("lost the daemon's model: %w", err))
			return
		}
		select {
		case _, ok := <-l.client.Wakes():
			if !ok {
				return
			}
		case <-l.done:
			return
		}
	}
}

// deliver hands a batch to every sink in turn, then applies the settings a
// ModelChanged in it carries.
func (l *Layer) deliver(evs []core.Event) {
	if len(evs) == 0 {
		return
	}
	l.sinkMu.Lock()
	sinks := slices.Clone(l.sinks)
	l.sinkMu.Unlock()
	for _, s := range sinks {
		s.apply(evs)
	}
	for _, ev := range evs {
		if m, ok := ev.(core.ModelChanged); ok {
			l.applyWeb(m.View.Web)
		}
	}
}

// applyWeb makes the listener match the published settings whenever v.Rev
// differs from the Rev it last acted on (the first view it sees always
// counts): it closes the listener, which ends every open stream, and binds
// v.Listen when that is not empty. Every ConfigureWeb bumps Rev and nothing
// else does, so a view that carries only a report (WebServing), a device or
// a pairing leaves the listener alone, and a disable and an enable that one
// Sync coalesced into a single ModelChanged still rebind, though the settings
// they end on equal the ones already bound. A listen that fails leaves the
// web off, and the host is told why (a notice for the next TUI); it is tried
// again only by the next ConfigureWeb, whatever values that carries. The host
// hears only what the layer did: an error the model set itself (a web.json it
// left alone) is not the layer's to clear.
func (l *Layer) applyWeb(v core.WebView) {
	if l.applied && l.rev == v.Rev {
		return
	}
	l.applied, l.rev = true, v.Rev
	key := webKey{listen: v.Listen, origin: v.Origin}
	addr, err := l.bind(key)
	switch {
	case errors.Is(err, errLayerClosed):
		return
	case err != nil:
		log.For("web").Warn("web.listen_failed", "listen", key.listen, "origin", key.origin, "err", err)
		l.reported = true
		l.host.WebServing("", fmt.Errorf("the web layer cannot listen: %w", err))
	case addr != "":
		log.For("web").Info("web.serving", "addr", addr, "origin", key.origin)
		l.reported = true
		l.host.WebServing(addr, nil)
	case l.reported:
		log.For("web").Info("web.off")
		l.reported = false
		l.host.WebServing("", nil)
	}
}

// recoverPanic ends a goroutine of the layer that panicked, and is deferred
// first in each one: it recovers the panic, logs it with its stack under
// subsystem=web and stops the layer (fail) with an error naming where, so the
// daemon goes on without the web. It must be deferred directly (defer
// l.recoverPanic("drain")), since recover only works in the deferred call.
func (l *Layer) recoverPanic(where string) {
	r := recover()
	if r == nil {
		return
	}
	log.For("web").Error("web.panic", "where", where, "panic", r, "stack", string(debug.Stack()))
	l.fail(fmt.Errorf("the web layer stopped: %s panicked: %v", where, r))
}

// fail stops the layer because of err, and returns at once: the stop (stop)
// runs on a goroutine of its own, so a part may call fail from the goroutine
// that panicked, which the stop may be waiting for (the drain). The stop ends
// every stream, closes the listener and the client, and tells the host err
// through WebServing("", err), whose notice waits for the next TUI. Close
// runs the same stop: whichever comes first runs it, once.
func (l *Layer) fail(err error) {
	log.For("web").Error("web.stopped", "err", err)
	go func() {
		defer l.recoverPanic("stop")
		l.stop(err)
	}()
}

// report tells the host the listener's state, and logs a panic from the host
// (a failed model re-raises its own) rather than raising it.
func (l *Layer) report(addr string, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.For("web").Error("web.report_failed", "panic", r)
		}
	}()
	l.host.WebServing(addr, err)
}
