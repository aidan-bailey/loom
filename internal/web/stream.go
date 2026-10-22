package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// The stream's bounds (spec, "Limits" and "The stream").
const (
	maxStreamsPerDevice = 4
	maxStreams          = 32
	pingEvery           = 20 * time.Second
	publishEvery        = 250 * time.Millisecond
	recentNotices       = 20
)

const (
	// streamQueue is how many events a stream may hold unwritten. A stream
	// that falls further behind is ended: its app reconnects, and the snapshot
	// it gets rebuilds everything the queue held.
	streamQueue = 64
	// byeGrace bounds how long close waits for the flusher to return and for
	// the streams to write their bye.
	byeGrace = 2 * time.Second
	// streamWriteTimeout bounds one frame's write and flush: a peer that stops
	// reading without closing would otherwise hold its handler, and its slot,
	// forever. The stream as a whole has no timeout.
	streamWriteTimeout = 30 * time.Second
)

var (
	errStreamsFull   = errors.New("too many open streams")
	errHubClosed     = errors.New("loom is stopping")
	errDeviceUnknown = errors.New("loom has not read this device's record yet; try again")

	pingFrame = sseFrame{event: "ping", data: []byte("{}")}
	byeFrame  = sseFrame{event: "bye", data: []byte("{}")}
)

var _ sink = (*hub)(nil)

// sseFrame is one server-sent event with its data already encoded. The data
// is compact JSON, which escapes every newline, so no text a session carries
// can end the frame or start another.
type sseFrame struct {
	event string
	data  []byte
}

func frameOf(event string, data any) (sseFrame, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return sseFrame{}, fmt.Errorf("encode %s event: %w", event, err)
	}
	return sseFrame{event: event, data: b}, nil
}

// streamSub is one open stream. The hub's mutex guards paired and dev; the
// rest is set before the stream is shared, or is a channel.
type streamSub struct {
	device string
	seq    uint64 // connection order: the lowest is the device's oldest
	ping   time.Duration
	out    chan sseFrame
	done   chan struct{} // closed to end the stream
	once   sync.Once
	// bye is set before done closes: the stream says goodbye as it ends
	// (the layer is closing) instead of just stopping (a revocation).
	bye bool
	// paired is the pairing time of the device record the stream began with,
	// and dev the record last sent.
	paired time.Time
	dev    deviceJSON
}

func (s *streamSub) end() { s.once.Do(func() { close(s.done) }) }

// offer queues a frame without blocking, and reports whether it fit.
func (s *streamSub) offer(f sseFrame) bool {
	select {
	case s.out <- f:
		return true
	default:
		return false
	}
}

// hubDevice is a device record as the hub last saw it published.
type hubDevice struct {
	rec    deviceJSON
	paired time.Time
}

// hub is the web layer's stream part and its sink: it keeps the fleet
// projection, the device records and the recent notices from the events the
// drain hands it, and fans them out to the open streams. apply, publish,
// sendTo and closeDevice never block and never do network I/O; each stream's
// own handler goroutine writes to its connection.
type hub struct {
	l  *Layer
	lg *slog.Logger

	mu      sync.Mutex
	closed  bool
	subs    map[*streamSub]struct{}
	counter uint64
	fleet   *projection
	// devices are the records the last published device list held.
	devices map[string]hubDevice
	// notices are the last recentNotices the hub sent to a stream, oldest
	// first: the model's own, and what publish and sendTo were given.
	notices []noticeJSON
	// pubWorkspaces and pubSessions are what the streams were last told, so
	// a flush sends only a list that differs from it.
	pubWorkspaces []workspaceJSON
	pubSessions   map[core.WorkspaceID][]sessionJSON
	// ping, every and flushFn are the knobs the tests turn, read under mu.
	ping    time.Duration
	every   time.Duration
	flushFn func()

	wake     chan struct{}
	stop     chan struct{}
	exited   chan struct{}
	handlers sync.WaitGroup
}

// newHub builds the hub and starts its flusher.
func newHub(l *Layer) *hub {
	h := &hub{
		l:             l,
		lg:            log.For("web", "part", "stream"),
		subs:          map[*streamSub]struct{}{},
		fleet:         newProjection(),
		devices:       map[string]hubDevice{},
		notices:       []noticeJSON{},
		pubWorkspaces: []workspaceJSON{},
		pubSessions:   map[core.WorkspaceID][]sessionJSON{},
		ping:          pingEvery,
		every:         publishEvery,
		wake:          make(chan struct{}, 1),
		stop:          make(chan struct{}),
		exited:        make(chan struct{}),
	}
	h.flushFn = h.flush
	go h.run()
	return h
}

// apply takes the events one Sync returned. A workspace or its sessions
// changing wakes the flusher, which publishes at most every publishEvery;
// a notice not tied to a request, and a device record changing, go out at once.
func (h *hub) apply(evs []core.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	moved := false
	for _, ev := range evs {
		switch ev := ev.(type) {
		case core.WorkspacesChanged:
			h.fleet.setWorkspaces(ev.Views)
			moved = true
		case core.ViewsChanged:
			if h.fleet.setViews(ev.WS, ev.Views) {
				moved = true
			}
		case core.ModelChanged:
			h.applyDevicesLocked(ev.View.Web.Devices)
		case core.Notice:
			// A notice tied to a request of the web client is its reply's.
			if ev.Req == 0 {
				h.noticeLocked(ev)
			}
		}
	}
	if moved {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
}

// applyDevicesLocked takes the published device list. A stream whose device
// is gone, or is there with another pairing time (revoked, then paired again
// under the same name), is ended with no bye; one whose device record changed
// is sent the new record.
func (h *hub) applyDevicesLocked(devices []core.WebDevice) {
	known := make(map[string]hubDevice, len(devices))
	for _, d := range devices {
		known[d.Name] = hubDevice{rec: deviceOf(d), paired: d.Paired}
	}
	h.devices = known
	for sub := range h.subs {
		d, ok := known[sub.device]
		if !ok || !sub.paired.Equal(d.paired) {
			h.dropLocked(sub)
			continue
		}
		if d.rec == sub.dev {
			continue
		}
		sub.dev = d.rec
		f, err := frameOf("device", d.rec)
		if err != nil {
			h.lg.Error("web.stream_device_encode_failed", "device", sub.device, "err", err)
			continue
		}
		h.offerLocked(sub, f)
	}
}

// noticeLocked sends a model notice to every stream and keeps it. A notice
// with no text says nothing and is dropped.
func (h *hub) noticeLocked(n core.Notice) {
	nj := noticeOf(n, h.l.now())
	if nj.Text == "" {
		return
	}
	h.publishLocked("notice", nj)
}

// publish sends an event to every open stream. A notice is also kept among
// the recent ones.
func (h *hub) publish(event string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(event, data)
}

func (h *hub) publishLocked(event string, data any) {
	h.keepLocked(event, data)
	h.broadcastLocked(event, data)
}

// sendTo sends an event to the open streams of one device. A notice is kept
// among the recent ones even when the device has no stream open, so a phone
// that missed it finds it in its next snapshot.
func (h *hub) sendTo(device, event string, data any) {
	f, err := frameOf(event, data)
	if err != nil {
		h.lg.Error("web.stream_encode_failed", "event", event, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keepLocked(event, data)
	for sub := range h.subs {
		if sub.device == device {
			h.offerLocked(sub, f)
		}
	}
}

// keepLocked keeps a notice among the last recentNotices, which every
// snapshot carries. An event that is not a notice, and a notice with no text,
// is not kept.
func (h *hub) keepLocked(event string, data any) {
	n, ok := data.(noticeJSON)
	if event != "notice" || !ok || n.Text == "" {
		return
	}
	h.notices = append(h.notices, n)
	if len(h.notices) > recentNotices {
		h.notices = slices.Clone(h.notices[len(h.notices)-recentNotices:])
	}
}

// closeDevice ends the open streams of one device, with no bye, so the app
// reconnects, is refused and shows pairing.
func (h *hub) closeDevice(device string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.device == device {
			h.dropLocked(sub)
		}
	}
}

func (h *hub) broadcastLocked(event string, data any) {
	f, err := frameOf(event, data)
	if err != nil {
		h.lg.Error("web.stream_encode_failed", "event", event, "err", err)
		return
	}
	for sub := range h.subs {
		h.offerLocked(sub, f)
	}
}

// offerLocked queues a frame for a stream, and ends the stream if it is full.
func (h *hub) offerLocked(sub *streamSub, f sseFrame) {
	if !sub.offer(f) {
		h.lg.Warn("web.stream_lagging", "device", sub.device)
		h.dropLocked(sub)
	}
}

// dropLocked takes a stream out of the set and ends it. Its handler stops
// writing at once, whatever is still queued.
func (h *hub) dropLocked(sub *streamSub) {
	delete(h.subs, sub)
	sub.end()
}

// flush publishes the workspace list and each workspace's session list that
// differs from what the streams were last told, each with the fleet's badge
// count as it stands now.
func (h *hub) flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	badge := h.fleet.badge()
	if !reflect.DeepEqual(h.fleet.workspaces, h.pubWorkspaces) {
		h.pubWorkspaces = h.fleet.workspaces
		h.broadcastLocked("workspaces", workspacesJSON{Workspaces: h.fleet.workspaces, Badge: badge})
	}
	for _, w := range h.fleet.workspaces {
		cur := h.fleet.sessions[w.ID]
		if reflect.DeepEqual(cur, h.pubSessions[w.ID]) {
			continue
		}
		h.pubSessions[w.ID] = cur
		h.broadcastLocked("sessions", sessionsJSON{Workspace: w.ID, Sessions: cur, Badge: badge})
	}
	for id := range h.pubSessions {
		if !h.fleet.served(id) {
			delete(h.pubSessions, id)
		}
	}
}

// run is the flusher: each wake waits one publishEvery, then flushes, so a
// workspace's list goes out at most four times a second however fast its
// views change. A panic stops the whole layer: recoverPanic, deferred first,
// calls the layer's fail. It runs last, after exited is closed, so the stop
// that fail starts (which ends in close, waiting on exited) never waits on a
// flusher that is still unwinding.
func (h *hub) run() {
	defer h.l.recoverPanic("hub")
	defer close(h.exited)
	for {
		select {
		case <-h.wake:
		case <-h.stop:
			return
		}
		h.mu.Lock()
		every, flush := h.every, h.flushFn
		h.mu.Unlock()
		t := time.NewTimer(every)
		select {
		case <-t.C:
		case <-h.stop:
			t.Stop()
			return
		}
		flush()
	}
}

// end marks the hub closed and ends every stream, with a bye or without, and
// stops the flusher. It reports whether this call was the one that closed it.
func (h *hub) end(bye bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.closed = true
	for sub := range h.subs {
		sub.bye = bye
		h.dropLocked(sub)
	}
	close(h.stop)
	return true
}

// close sends bye on every stream and ends them, refuses new ones, and waits
// up to byeGrace for the flusher to return and the handlers to write the bye,
// so the listener can close behind it. A nil hub, and a hub already closed,
// do nothing. The layer's stop calls it from Close, and from the goroutine
// fail starts when a part panicked, the hub's flusher among them.
func (h *hub) close() {
	if h == nil || !h.end(true) {
		return
	}
	finished := make(chan struct{})
	go func() {
		defer h.l.recoverPanic("hub")
		defer close(finished)
		<-h.exited
		h.handlers.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(byeGrace):
		h.lg.Warn("web.hub_close_grace")
	}
}

// subscribe adds a stream for device, ending the device's oldest first when
// it already has maxStreamsPerDevice, and queues the snapshot as its first
// event. It refuses with errHubClosed once the hub is closed, with
// errDeviceUnknown when the device list the hub has read does not name the
// device (the host answered for it an instant before: a connection that raced
// the hub reading a pairing or a revocation, which the app retries), and with
// errStreamsFull when maxStreams streams are open. On success the caller must
// call release.
func (h *hub) subscribe(device string) (*streamSub, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errHubClosed
	}
	dev, ok := h.devices[device]
	if !ok {
		return nil, errDeviceUnknown
	}
	mine := 0
	var oldest *streamSub
	for s := range h.subs {
		if s.device != device {
			continue
		}
		mine++
		if oldest == nil || s.seq < oldest.seq {
			oldest = s
		}
	}
	if mine >= maxStreamsPerDevice {
		h.dropLocked(oldest)
	}
	if len(h.subs) >= maxStreams {
		return nil, errStreamsFull
	}
	snap, err := frameOf("snapshot", h.snapshotLocked(dev))
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	h.counter++
	sub := &streamSub{
		device: device,
		seq:    h.counter,
		ping:   h.ping,
		out:    make(chan sseFrame, streamQueue),
		done:   make(chan struct{}),
		paired: dev.paired,
		dev:    dev.rec,
	}
	sub.out <- snap
	h.subs[sub] = struct{}{}
	h.handlers.Add(1)
	return sub, nil
}

// release takes a stream out of the set when its handler returns.
func (h *hub) release(sub *streamSub) {
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
	sub.end()
	h.handlers.Done()
}

// snapshotLocked is everything the app shows, for a device.
func (h *hub) snapshotLocked(dev hubDevice) snapshotJSON {
	return snapshotJSON{
		Build:      h.l.build,
		Host:       h.l.hostname,
		Workspaces: h.fleet.workspaces,
		Sessions:   h.fleet.sessions,
		Device:     dev.rec,
		Badge:      h.fleet.badge(),
		Notices:    h.notices,
	}
}

// routeStream serves GET /api/v1/stream to a paired device.
func (l *Layer) routeStream(m *http.ServeMux) {
	m.Handle("GET /api/v1/stream", l.authed(func(w http.ResponseWriter, r *http.Request, device string) {
		l.hub.serveStream(w, r, device)
	}))
}

// serveStream writes device's stream until the client leaves, the hub ends
// it, or a write fails. It sets the device cookie again first, so a device in
// use never expires, and answers 503 when the limits refuse it.
func (h *hub) serveStream(w http.ResponseWriter, r *http.Request, device string) {
	// The mux routes HEAD to a GET pattern; a HEAD must not hold a stream slot
	// and ping into a body nobody reads.
	if r.Method == http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sub, err := h.subscribe(device)
	if err != nil {
		if !errors.Is(err, errStreamsFull) && !errors.Is(err, errHubClosed) && !errors.Is(err, errDeviceUnknown) {
			h.lg.Error("web.stream_start_failed", "device", device, "err", err)
			err = errors.New("could not start the stream")
		}
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer h.release(sub)

	if c, err := r.Cookie(cookieName); err == nil {
		setDeviceCookie(w, c.Value)
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}
	send := func(f sseFrame) bool {
		// A writer without deadlines (a test recorder) is written with none.
		if err := rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.event, f.data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	tick := time.NewTicker(sub.ping)
	defer tick.Stop()
	for {
		// An ended stream gets nothing more, whatever is queued: a revoked
		// device must not read another list.
		select {
		case <-sub.done:
			if sub.bye {
				send(byeFrame)
			}
			return
		default:
		}
		select {
		case f := <-sub.out:
			// Both channels may have been ready: an ended stream gets nothing
			// more, so look again; the top of the loop says bye if it is due.
			select {
			case <-sub.done:
				continue
			default:
			}
			if !send(f) {
				return
			}
		case <-tick.C:
			if !send(pingFrame) {
				return
			}
		case <-sub.done:
		case <-r.Context().Done():
			return
		}
	}
}
