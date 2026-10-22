// Package web is the daemon's web layer: a client of the daemon's own
// rpc.Server (joined over net.Pipe as the web role) plus core.WebHost for
// device records, serving the loom PWA, a JSON API and a live stream on a
// loopback port that a Cloudflare tunnel reaches, and sending Web Push. It
// holds no session state: the model owns every session and every device. It
// logs under subsystem=web.
package web

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/aidan-bailey/loom/core"
)

// sink is a part of the layer that follows the daemon's events. The drain
// hands every batch Sync returns to each sink in turn, on its own
// goroutine: a sink never blocks and never does network I/O.
type sink interface{ apply(evs []core.Event) }

// Layer is the web layer inside a running daemon: Start builds it and Close
// stops it. Its parts (the stream hub, the request tracker, the push sender)
// each follow the daemon's events as a sink and register their routes in mux.
type Layer struct {
	host core.WebHost

	// rule is the origin rule of the listener now running, which guard
	// enforces; nil while none runs, and then guard refuses every request.
	rule atomic.Pointer[originRule]

	// sinkMu guards sinks.
	sinkMu sync.Mutex

	sinks []sink
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
	return m
}

// startParts builds the layer's parts and adds their sinks, before the drain starts.
func (l *Layer) startParts() {
}

// stopParts stops the layer's parts, first in Close.
func (l *Layer) stopParts() {
}
