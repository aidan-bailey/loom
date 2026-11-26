package web

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/coder/websocket"
)

// The terminals' part of the web layer: the slots a terminal connection
// counts as from before the model is asked for its shell, the caps, the
// switch's copy and the device list its sink keeps, and what ends a slot.
// http.Server.Close does not track a hijacked connection, so the
// connections of a listener that closes or of a layer that stops are ended
// here and nowhere else.

const (
	// maxTerminalsPerDevice and maxTerminals cap the open connections of a
	// device and of the whole layer. They count connections, never shells.
	maxTerminalsPerDevice = 4
	maxTerminals          = 16

	// termCloseGrace bounds how long close waits for the connections to
	// write their closing frames.
	termCloseGrace = 2 * time.Second
)

// The refusals reserve gives, as the route answers them.
var (
	errTermsStopping = &statusError{http.StatusServiceUnavailable, "loom is stopping"}
	errTermOff       = &statusError{http.StatusForbidden, "the terminal is off on this host"}
	errTermsFull     = &statusError{http.StatusTooManyRequests, "too many open terminals"}
)

var (
	_ sink      = (*terminals)(nil)
	_ termShell = (*tmux.ShellAttach)(nil)
)

// termSlot is one connection's place, from the reservation to the handler's
// return. An ending the part gives it before its connection exists is kept,
// and handed to the connection when it attaches.
type termSlot struct {
	device string
	paired time.Time

	mu     sync.Mutex
	conn   *termConn
	ended  bool
	ending termEnd
}

// end gives the slot its ending, the first one winning, and passes it on to
// the connection when there is one. It never blocks.
func (s *termSlot) end(e termEnd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended, s.ending = true, e
	if s.conn != nil {
		s.conn.end(e)
	}
}

// attach gives the slot its connection, before the connection runs. An
// ending the slot already has ends the connection at once: termConn.end
// before run makes run send it and open no attach.
func (s *termSlot) attach(c *termConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = c
	if s.ended {
		c.end(s.ending)
	}
}

// terminals is the part: a sink following the switch and the devices, and
// the owner of every slot. Its mutex guards what follows it; end never
// blocks, so the sink, endAll and close hold it safely.
type terminals struct {
	l  *Layer
	lg *slog.Logger

	// ensure asks the model for a session's shell, newOpen makes the termOpen
	// of a shell by its tmux name, and exec and ptys are what tmuxOpen runs
	// tmux through. Tests replace them before any request.
	ensure  func(id core.InstanceID, req core.ReqID)
	newOpen func(name string) termOpen
	exec    internalexec.Executor
	ptys    tmux.PtyFactory

	// grace is how long close waits for the handlers.
	grace time.Duration

	// devices holds each paired device's pairing time as last published, and
	// handlers one count per slot.
	mu       sync.Mutex
	closed   bool
	on       bool
	devices  map[string]time.Time
	slots    map[*termSlot]struct{}
	handlers sync.WaitGroup
}

// newTerminals builds the part. It starts from the replica's switch and
// device list, so a request that comes before the drain's first batch is
// judged by what the model already published.
func newTerminals(l *Layer) *terminals {
	t := &terminals{
		l:       l,
		lg:      log.For("web", "part", "terminal"),
		exec:    internalexec.Default{},
		ptys:    tmux.MakePtyFactory(),
		grace:   termCloseGrace,
		devices: map[string]time.Time{},
		slots:   map[*termSlot]struct{}{},
	}
	t.newOpen = t.tmuxOpen
	if l.client != nil {
		t.ensure = l.client.EnsureTerminal
		w := l.client.Web()
		t.on = w.Terminal
		t.setDevicesLocked(w.Devices)
	}
	return t
}

// reserve counts a connection of device before the model is asked for its
// shell. It refuses (as a *statusError) while the part is closed or no
// listener runs (503), while the switch is off (403), for a device the part
// has not read (503, which the app retries) and at the caps (429). The
// caller must release the slot it gets.
func (t *terminals) reserve(device string) (*termSlot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.closed || t.l.rule.Load() == nil:
		return nil, errTermsStopping
	case !t.on:
		return nil, errTermOff
	}
	paired, ok := t.devices[device]
	if !ok {
		return nil, &statusError{http.StatusServiceUnavailable, errDeviceUnknown.Error()}
	}
	mine := 0
	for s := range t.slots {
		if s.device == device {
			mine++
		}
	}
	if mine >= maxTerminalsPerDevice || len(t.slots) >= maxTerminals {
		return nil, errTermsFull
	}
	s := &termSlot{device: device, paired: paired}
	t.slots[s] = struct{}{}
	t.handlers.Add(1)
	return s, nil
}

// release gives a slot back, once: the handler defers it.
func (t *terminals) release(s *termSlot) {
	t.mu.Lock()
	_, held := t.slots[s]
	delete(t.slots, s)
	t.mu.Unlock()
	if held {
		t.handlers.Done()
	}
}

// apply is the sink: a published model view carries the switch and the
// device list. A device that is gone, or there with another pairing time
// (revoked, then paired again under the same name), loses its slots to
// termEndRevoked; a switch that is off ends every slot with termEndOff.
func (t *terminals) apply(evs []core.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	for _, ev := range evs {
		m, ok := ev.(core.ModelChanged)
		if !ok {
			continue
		}
		w := m.View.Web
		t.on = w.Terminal
		t.setDevicesLocked(w.Devices)
		if !w.Terminal {
			t.endLocked(termEndOff)
		}
	}
}

// setDevicesLocked takes the published device list and ends the slots of a
// device that is no longer the one they began with. t.mu is held, or t is
// not shared yet.
func (t *terminals) setDevicesLocked(devices []core.WebDevice) {
	known := make(map[string]time.Time, len(devices))
	for _, d := range devices {
		known[d.Name] = d.Paired
	}
	t.devices = known
	for s := range t.slots {
		if paired, ok := known[s.device]; !ok || !paired.Equal(s.paired) {
			s.end(termEndRevoked)
		}
	}
}

// endAll ends every slot with e and leaves the part open: the layer calls it
// when the listener rebinds, since the connections it holds are the old
// listener's. It is safe on a nil part.
func (t *terminals) endAll(e termEnd) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endLocked(e)
}

// endLocked ends every slot with e. t.mu is held.
func (t *terminals) endLocked(e termEnd) {
	for s := range t.slots {
		s.end(e)
	}
}

// close refuses new slots, ends every slot as the layer stopping, and waits
// up to the grace for the handlers to return, so each connection has written
// its closing frame before the listener closes. It is safe on a nil part and
// called twice.
func (t *terminals) close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.endLocked(termEndStopping)
	t.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		defer t.l.recoverPanic("terminals")
		defer close(finished)
		t.handlers.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(t.grace):
		t.lg.Warn("web.terminals_close_grace")
	}
}

// tmuxOpen is the termOpen for the tmux session called name: the history
// above the visible screen, then the attach at the browser's size. A history
// that cannot be read leaves the scrollback empty and is logged; an attach
// that cannot start is logged with its cause, which the connection does not
// tell the app, and returned.
func (t *terminals) tmuxOpen(name string) termOpen {
	return func(cols int, rows int) (termShell, []byte, error) {
		history, err := tmux.ShellHistory(name, t.exec)
		if err != nil {
			t.lg.Warn("web.terminal_history_failed", "shell", name, "err", err)
			history = nil
		}
		a, err := tmux.StartShellAttach(name, cols, rows, t.ptys)
		if err != nil {
			t.lg.Warn("web.terminal_attach_failed", "shell", name, "err", err)
			return nil, nil, err
		}
		return a, history, nil
	}
}

// terminal answers GET /api/v1/sessions/{id}/terminal: a WebSocket to the
// session's worktree shell. It runs behind authed, and guard does not check
// the Origin of a GET, so the route checks it itself: exactly one Origin
// header that the layer's origin rule accepts, which is why the library's own
// origin check is off. Everything that can be refused is refused before the
// upgrade, as JSON, and first of all a request that is not a WebSocket upgrade
// (426: not a GET, below HTTP/1.1, or without the Connection and Upgrade
// headers) or whose handshake Accept would refuse (400: not version 13, or not
// exactly one Sec-WebSocket-Key of 16 bytes), each before the origin, the slot
// and the model: then the origin (403), the session (404), the slot (the
// switch off 403, the caps 429, a stopping layer or an unread device 503) and
// the model (409 with its words, 504 when it did not answer, 500 when its
// reply named no shell). The slot is reserved before the model is asked for the
// shell, and released when this handler returns. After Accept the connection
// is the library's: its endings are close frames (termEnd), and the bytes are
// never logged.
func (l *Layer) terminal(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "terminal"}
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Upgrade", "websocket")
		l.refuse(w, a, http.StatusUpgradeRequired, "this route is a WebSocket upgrade")
		return
	}
	if !validHandshake(r) {
		w.Header().Set("Sec-WebSocket-Version", "13")
		l.refuse(w, a, http.StatusBadRequest, "this route needs WebSocket version 13 and one Sec-WebSocket-Key of 16 bytes")
		return
	}
	rule := l.rule.Load()
	if origins := r.Header.Values("Origin"); rule == nil || len(origins) != 1 || !rule.allowsOrigin(origins[0]) {
		l.refuse(w, a, http.StatusForbidden, "the request does not come from loom's origin")
		return
	}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	t := l.terms
	slot, err := t.reserve(device)
	if err != nil {
		status, msg := errStatus(err)
		l.refuse(w, a, status, msg)
		return
	}
	defer t.release(slot)
	reply, err := l.readReply(r.Context(), device, func(req core.ReqID) { t.ensure(id, req) })
	if err != nil {
		status, msg := errStatus(err)
		l.refuse(w, a, status, msg)
		return
	}
	if reply.Terminal == "" {
		l.refuse(w, a, http.StatusInternalServerError, "loom's reply named no shell")
		return
	}
	// Accept is handed the writer recordHijack gives, which keeps the
	// connection Accept hijacks: the connection closes it outright when its
	// close handshake outlasts termCloseWait, since the library's close waits
	// with no deadline on a frame the browser may never finish and
	// http.Server.Close does not track a hijacked connection.
	rw, hijacked := recordHijack(w)
	ws, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		// The handshake was checked above and the origin check is off, so the
		// one failure left to Accept is a connection it cannot hijack, which
		// it has answered 500 itself.
		l.record(a, http.StatusInternalServerError, "the connection could not be hijacked: "+err.Error())
		return
	}
	conn := newTermConn(ws, hijacked(), t.newOpen(reply.Terminal), l.shellEnded(id), l.recoverPanic)
	slot.attach(conn)
	l.record(a, http.StatusSwitchingProtocols, "ok")
	t.lg.Info("web.terminal_open", "device", device, "session", uint64(id))
	// The request's context is not used after Accept: the part ends the
	// connection, and run returns when it ends.
	end := conn.run(context.Background())
	t.lg.Info("web.terminal_close", "device", device, "session", uint64(id), "reason", closeReason(end))
}

// isWebSocketUpgrade reports whether r asks for a WebSocket upgrade: a GET
// (the mux's GET pattern also serves HEAD) at HTTP/1.1 or above, whose
// Connection header lists upgrade and whose Upgrade header lists websocket,
// each as a token and in any case.
func isWebSocketUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet && r.ProtoAtLeast(1, 1) &&
		headerHasToken(r.Header, "Connection", "upgrade") && headerHasToken(r.Header, "Upgrade", "websocket")
}

// validHandshake reports whether r's handshake is one websocket.Accept takes:
// Sec-WebSocket-Version 13 and exactly one Sec-WebSocket-Key that decodes to
// 16 bytes. The route asks before the origin, the slot and the model, so
// Accept's own 400 never answers after the model has started a shell.
func validHandshake(r *http.Request) bool {
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	keys := r.Header.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	return err == nil && len(raw) == 16
}

// headerHasToken reports whether any value of the header key lists token, in
// a comma-separated list, ignoring case.
func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// closeReason is the reason a close is logged with: the ending's own, or that
// the browser left when loom sent no close frame (the zero termEnd).
func closeReason(e termEnd) string {
	if e == (termEnd{}) {
		return "the browser left"
	}
	return e.reason
}

// shellEnded is the connection's `ended` for session id, asked once when the
// attach's output ends by itself: a session that is still started and active
// (the replica's Active) lost only its shell, which exited; any other was
// paused or killed. It reads the replica and is cheap.
func (l *Layer) shellEnded(id core.InstanceID) func() termEnd {
	return func() termEnd {
		if v, ok := l.client.View(id); ok && v.Active() {
			return termEndExited
		}
		return termEndSession
	}
}
