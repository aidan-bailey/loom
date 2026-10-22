package web

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/web/webpush"
	"github.com/aidan-bailey/loom/log"
)

// The push sender's limits (spec, "Push" and "Limits").
const (
	// pushTimeout bounds one exchange with a push service.
	pushTimeout = 10 * time.Second
	// pushQueueMax is how many pushes may wait for the sender; a push that
	// finds the queue full is dropped and logged.
	pushQueueMax = 100
	// pushPerMinute is the most pushes one device takes in a minute.
	pushPerMinute = 30
	// pushRetries is how many times a 429, a 5xx or a transport error is
	// retried, after the first attempt.
	pushRetries = 3
	// pushBackoff is the first retry's wait when the service gave no
	// Retry-After; it doubles each time.
	pushBackoff = time.Second
	// pushRetryAfterMax is the longest Retry-After the sender sits through.
	// The sender is one goroutine for every device, and a push is stale
	// within the hour anyway, so a service that asks for longer is given up
	// on, recorded as failing.
	pushRetryAfterMax = 30 * time.Second
	// pushTTL is how long a push service keeps a push for an offline phone.
	pushTTL = time.Hour
)

// pushEnv is what a pusher needs of its layer, so a test can build one
// without a layer.
type pushEnv struct {
	host core.WebHost
	now  func() time.Time
	// origin is the configured origin, which signs the VAPID token as its
	// subject.
	origin func() string
	// recoverPanic is the layer's: the sender defers it, so a panic in the
	// sender stops the whole layer (Layer.fail), as one in any of the layer's
	// goroutines does.
	recoverPanic func(where string)
}

// pusher is the layer's push sender and the sink that feeds it. apply runs on
// the drain goroutine: it works out the pushes a projection implies and
// queues them without blocking. One sender goroutine takes them off the queue,
// addresses each to the devices whose settings allow it, and posts it, so the
// drain never waits on a push service.
type pusher struct {
	env   pushEnv
	httpc *http.Client
	wait  func(ctx context.Context, d time.Duration) bool
	log   *slog.Logger

	watch *pushWatch
	queue chan pushJob

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// sent holds each device's push times of the last minute; the sender
	// goroutine alone touches it.
	sent map[string][]time.Time
}

var _ sink = (*pusher)(nil)

// newPusher builds the layer's pusher and starts its sender.
func newPusher(l *Layer) *pusher {
	p := newPusherWith(pushEnv{
		host:         l.host,
		now:          func() time.Time { return l.now() },
		origin:       func() string { return l.client.Web().Origin },
		recoverPanic: l.recoverPanic,
	})
	p.start()
	return p
}

// newPusherWith builds a pusher that has not started its sender. Its client
// never follows a redirect: a push service answers where it means to, and a
// redirect would carry the daemon's request to a host the subscription check
// never admitted.
func newPusherWith(env pushEnv) *pusher {
	ctx, cancel := context.WithCancel(context.Background())
	return &pusher{
		env: env,
		httpc: &http.Client{
			Timeout:       pushTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		wait:   pushSleep,
		log:    log.For("web", "part", "push"),
		watch:  newPushWatch(),
		queue:  make(chan pushJob, pushQueueMax),
		ctx:    ctx,
		cancel: cancel,
		sent:   map[string][]time.Time{},
	}
}

// start runs the sender.
func (p *pusher) start() {
	p.wg.Add(1)
	go p.run()
}

// close stops the sender: a push in flight is cancelled, a wait for a retry
// ends, and close returns once the sender has. It may be called again, and it
// may be called by the stop that Layer.fail starts after the sender panicked:
// a sender that panicked has already marked itself done (see run).
func (p *pusher) close() {
	p.cancel()
	p.wg.Wait()
}

// apply is the sink: the drain hands it every Sync's events.
func (p *pusher) apply(evs []core.Event) {
	for _, ev := range evs {
		switch ev := ev.(type) {
		case core.WorkspacesChanged:
			p.watch.forget(ev.Views)
		case core.ViewsChanged:
			for _, job := range p.watch.observe(ev.WS, ev.Views) {
				p.enqueue(job)
			}
		}
	}
}

// enqueue queues a push for the sender without waiting. It reports false,
// after logging, for one it dropped because the queue is full, and false for
// any push once the pusher is closed.
func (p *pusher) enqueue(job pushJob) bool {
	if p.ctx.Err() != nil {
		return false
	}
	select {
	case p.queue <- job:
		return true
	default:
		p.log.Warn("push dropped: the queue is full", "kind", job.Kind, "session", job.Session, "limit", pushQueueMax)
		return false
	}
}

// run is the sender goroutine. A panic in it is the layer's to handle: the
// layer's recoverPanic logs the stack and calls Layer.fail, which stops the
// whole layer on a goroutine of its own and so closes this pusher, and which
// raises the notice. The two defers are in this order on purpose: wg.Done
// runs first, before the panic is recovered, so a close that waits for the
// sender (the stop fail starts, or any stop that ran on this goroutine)
// never waits on a sender that is itself inside recoverPanic.
func (p *pusher) run() {
	defer p.env.recoverPanic("push")
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.queue:
			p.deliver(job)
		}
	}
}

// deliver posts one push to every device whose settings allow its kind.
func (p *pusher) deliver(job pushJob) {
	key, err := p.env.host.VAPIDKey()
	if err != nil {
		p.log.Warn("push dropped: no VAPID key", "kind", job.Kind, "session", job.Session, "err", err)
		return
	}
	payload, err := job.payload()
	if err != nil {
		p.log.Error("push dropped: payload", "kind", job.Kind, "session", job.Session, "err", err)
		return
	}
	subject := p.env.origin()
	for _, target := range p.env.host.PushTargets() {
		if p.ctx.Err() != nil {
			return
		}
		if !pushAllows(target.Notify, job.Kind, p.env.now()) {
			continue
		}
		if p.atRate(target.Device) {
			p.log.Warn("push dropped: the device is at its rate limit", "device", target.Device, "kind", job.Kind, "session", job.Session, "limit", pushPerMinute)
			continue
		}
		p.send(target, key, subject, job, payload)
	}
}

// pushAllows reports whether a device's settings take a push of this kind now.
func pushAllows(n core.NotifySettings, kind string, now time.Time) bool {
	if n.PausedUntil.After(now) {
		return false
	}
	switch kind {
	case pushWaiting:
		return n.Waiting
	case pushFinished:
		return n.Finished
	}
	return false
}

// atRate reports whether the device has taken its limit of pushes in the last
// minute, after forgetting the older ones. It counts nothing: a push is
// counted (see countSent) only once its request goes out, so one skipped for
// an unusable subscription does not use up the device's minute.
func (p *pusher) atRate(device string) bool {
	cutoff := p.env.now().Add(-time.Minute)
	for name, times := range p.sent {
		keep := times[:0]
		for _, at := range times {
			if at.After(cutoff) {
				keep = append(keep, at)
			}
		}
		if len(keep) == 0 {
			delete(p.sent, name)
		} else {
			p.sent[name] = keep
		}
	}
	return len(p.sent[device]) >= pushPerMinute
}

// countSent counts a push against the device's minute.
func (p *pusher) countSent(device string) {
	p.sent[device] = append(p.sent[device], p.env.now())
}

// send posts the payload to one device and records the outcome through
// PushResult: a 2xx is ok, a 404 or 410 is an expired subscription, and any
// other status is failing with it. A 429, a 5xx or a transport error is
// retried up to pushRetries times, after the service's Retry-After or else a
// doubling backoff. A transport error that outlasts its retries is the
// network's trouble, not the subscription's: it is logged and the recorded
// state stays.
func (p *pusher) send(target core.PushTarget, key core.VAPIDKey, subject string, job pushJob, payload []byte) {
	sub, err := webpush.ParseSubscription(target.Subscription)
	if err != nil {
		p.log.Warn("push skipped: unusable subscription", "device", target.Device, "err", err)
		return
	}
	msg := webpush.Message{Payload: payload, TTL: pushTTL, Urgency: job.urgency(), Topic: job.topic()}
	for attempt := 0; ; attempt++ {
		req, err := webpush.NewRequest(p.ctx, sub, key, subject, msg, p.env.now())
		if err != nil {
			p.log.Warn("push skipped: the request could not be built", "device", target.Device, "err", err)
			return
		}
		if attempt == 0 {
			p.countSent(target.Device)
		}
		status, retryAfter, err := p.post(req)
		// A close during the exchange cancelled it: its outcome (a transport
		// error from the cancel) says nothing about the subscription, so
		// record nothing.
		if p.ctx.Err() != nil {
			return
		}
		if err == nil {
			switch {
			case status >= 200 && status < 300:
				p.env.host.PushResult(target.Device, core.PushOK)
				return
			case status == http.StatusNotFound || status == http.StatusGone:
				p.env.host.PushResult(target.Device, core.PushExpired)
				return
			case status != http.StatusTooManyRequests && status < 500:
				p.env.host.PushResult(target.Device, core.PushFailing(status))
				return
			}
		}
		pause := pushBackoff << attempt
		if retryAfter > 0 {
			pause = retryAfter
		}
		if attempt == pushRetries || pause > pushRetryAfterMax {
			p.giveUp(target.Device, status, err)
			return
		}
		if !p.wait(p.ctx, pause) {
			return
		}
	}
}

// giveUp records a push that failed for good: an HTTP status is recorded on
// the device, and a transport error is only logged.
func (p *pusher) giveUp(device string, status int, err error) {
	if err != nil {
		p.log.Warn("push not delivered", "device", device, "err", err)
		return
	}
	p.log.Warn("push not delivered", "device", device, "status", status)
	p.env.host.PushResult(device, core.PushFailing(status))
}

// post makes one exchange and reads the status and any Retry-After, leaving
// the body unread beyond what lets the connection be reused.
func (p *pusher) post(req *http.Request) (status int, retryAfter time.Duration, err error) {
	resp, err := p.httpc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After"), p.env.now()), nil
}

// parseRetryAfter reads a Retry-After header, whole seconds or an HTTP date,
// as a wait; 0 when there is none or it can't be read. A day is the most it
// returns, which is already over what the sender sits through.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(min(max(secs, 0), 24*60*60)) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		return min(max(at.Sub(now), 0), 24*time.Hour)
	}
	return 0
}

// pushSleep waits d, and reports false if ctx ended first.
func pushSleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// routePush adds the push key route to the layer's mux.
func (l *Layer) routePush(m *http.ServeMux) {
	m.Handle("GET /api/v1/push-key", l.authed(l.pushKey))
}

// pushKey answers the VAPID public key the app subscribes with
// (pushManager.subscribe's applicationServerKey), as unpadded base64url.
func (l *Layer) pushKey(w http.ResponseWriter, _ *http.Request, _ string) {
	key, err := l.host.VAPIDKey()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "push is not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": base64.RawURLEncoding.EncodeToString(key.Public)})
}
