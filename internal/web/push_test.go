package web

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
)

// pushTestStart is the fake clock's first reading, on a whole second so an
// HTTP date built from it reads back exactly.
var pushTestStart = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

const (
	// pushTestOrigin is the origin the rig's model is configured with: an
	// https one, which can be a VAPID subject.
	pushTestOrigin = "https://loom.example.com"
	// pushTestBaseline is the status every rig device starts out failing
	// with, so that a push that records nothing is told from one that
	// records ok.
	pushTestBaseline = 599
)

func pushPtr[T any](v T) *T { return &v }

// pushReply is how the fake push service answers one request: a status, with
// a Retry-After header when retryAfter is set, or (drop) the connection cut
// before any answer, which the sender sees as a transport error.
type pushReply struct {
	status     int
	retryAfter string
	drop       bool
}

// pushRequest is what the fake push service saw of one request.
type pushRequest struct {
	path   string
	header http.Header
	body   []byte
}

// pushService stands in for Apple's push service: an httptest TLS server
// that records every request and answers from a script, the last reply for
// every request after the script's end. transport sends every request to it,
// whatever host the endpoint names.
type pushService struct {
	srv *httptest.Server

	mu      sync.Mutex
	replies []pushReply
	reqs    []pushRequest
	served  int
}

// newPushService starts the service; it answers 201 until a test scripts it.
func newPushService(t *testing.T) *pushService {
	t.Helper()
	svc := &pushService{replies: []pushReply{{status: http.StatusCreated}}}
	svc.srv = httptest.NewTLSServer(http.HandlerFunc(svc.serve))
	t.Cleanup(svc.srv.Close)
	return svc
}

func (svc *pushService) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	svc.mu.Lock()
	reply := svc.replies[min(svc.served, len(svc.replies)-1)]
	svc.served++
	svc.reqs = append(svc.reqs, pushRequest{path: r.URL.Path, header: r.Header.Clone(), body: body})
	svc.mu.Unlock()
	if reply.drop {
		panic(http.ErrAbortHandler)
	}
	if reply.retryAfter != "" {
		w.Header().Set("Retry-After", reply.retryAfter)
	}
	w.WriteHeader(reply.status)
}

// transport dials the service for any address, and trusts its certificate,
// which httptest makes for example.com and 127.0.0.1. Keep-alives are off, so
// every request is a connection of its own and a cut one is never replayed.
func (svc *pushService) transport() *http.Transport {
	pool := x509.NewCertPool()
	pool.AddCert(svc.srv.Certificate())
	return &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.com", MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, svc.srv.Listener.Addr().String())
		},
		DisableKeepAlives: true,
	}
}

// script replaces the replies and starts them over.
func (svc *pushService) script(replies ...pushReply) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.replies, svc.served = replies, 0
}

func (svc *pushService) seen() []pushRequest {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return append([]pushRequest(nil), svc.reqs...)
}

// requested is the device each request was for (the endpoints end in the
// device's name), in order.
func (svc *pushService) requested() []string {
	var out []string
	for _, req := range svc.seen() {
		out = append(out, strings.TrimPrefix(req.path, "/"))
	}
	return out
}

// forget clears the record of requests, not the script's place.
func (svc *pushService) forget() {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.reqs = nil
}

// pushClock is the clock a test moves by hand.
type pushClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *pushClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *pushClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// pushTestSub is a subscription with a real P-256 key and a 16-byte secret,
// as a browser makes one, at an endpoint on Apple's host.
func pushTestSub(t *testing.T, device string) core.PushSubscription {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return core.PushSubscription{
		Endpoint: "https://web.push.apple.com/" + device,
		P256DH:   base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// The wrappers below are the model with one thing it would never do itself,
// so the sender's handling of it can be tested; every other WebHost method
// is the model's.

// keylessHost cannot give a VAPID key, as the model cannot before its first
// enable.
type keylessHost struct{ core.WebHost }

func (keylessHost) VAPIDKey() (core.VAPIDKey, error) { return core.VAPIDKey{}, core.ErrWebOff }

// extraTargetHost has one more push target than the model holds, ahead of
// the model's.
type extraTargetHost struct {
	core.WebHost
	extra core.PushTarget
}

func (h extraTargetHost) PushTargets() []core.PushTarget {
	return append([]core.PushTarget{h.extra}, h.WebHost.PushTargets()...)
}

// panicHost's PushTargets panics, as a defect on the sender's path would.
type panicHost struct{ core.WebHost }

func (panicHost) PushTargets() []core.PushTarget { panic("boom") }

// pushRig is a pusher over the model of a webStack, a fake push service and
// a fake clock. Its sender is not started and its waits are recorded, not
// slept. No layer runs: the tests that need one start it in Task 12.3's
// newPushStack.
type pushRig struct {
	s     *webStack
	p     *pusher
	svc   *pushService
	clock *pushClock

	mu    sync.Mutex
	waits []time.Duration
}

// newPushRig boots the model with the web enabled (so it has a VAPID key and
// pairs devices) and pairs a device per name, each subscribed at Apple's
// host, taking both kinds of push, and failing with pushTestBaseline. The
// push service answers 201 until a test scripts it.
func newPushRig(t *testing.T, devices ...string) *pushRig {
	t.Helper()
	s := newWebStack(t)
	require.NoError(t, s.connect(t).ConfigureWeb("127.0.0.1:7420", pushTestOrigin))
	rig := &pushRig{s: s, svc: newPushService(t), clock: &pushClock{now: pushTestStart}}
	// A sender that panics in a test should fail the test, so nothing here
	// recovers.
	rig.p = newPusherWith(pushEnv{
		host:         s.loop,
		now:          rig.clock.Now,
		origin:       func() string { return pushTestOrigin },
		recoverPanic: func(string) {},
	})
	rig.p.httpc.Transport = rig.svc.transport()
	rig.p.wait = func(_ context.Context, d time.Duration) bool {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		rig.waits = append(rig.waits, d)
		return true
	}
	t.Cleanup(rig.p.close)
	for _, name := range devices {
		rig.addDevice(t, name, core.NotifyChange{})
	}
	return rig
}

// addDevice pairs a device on the model as the pairing route does, gives it a
// push subscription at Apple's host and the settings notify sets, and marks
// it failing with pushTestBaseline.
func (r *pushRig) addDevice(t *testing.T, name string, notify core.NotifyChange) {
	t.Helper()
	pairing, err := r.s.connect(t).PairDevice(name)
	require.NoError(t, err)
	_, _, err = r.s.loop.RedeemPairing(pairing.Code)
	require.NoError(t, err)
	_, err = r.s.loop.SetPushSubscription(name, pushTestSub(t, name))
	require.NoError(t, err)
	_, err = r.s.loop.SetNotify(name, notify)
	require.NoError(t, err)
	r.s.loop.PushResult(name, core.PushFailing(pushTestBaseline))
	require.Equal(t, core.PushFailing(pushTestBaseline), r.state(t, name))
}

// stateOf is the device's push state in the model. A SetNotify that changes
// nothing answers with the device as stored, from the model's own loop, so
// the answer is never behind a PushResult that has returned.
func (r *pushRig) stateOf(device string) (core.PushState, error) {
	d, err := r.s.loop.SetNotify(device, core.NotifyChange{})
	return d.Push, err
}

func (r *pushRig) state(t *testing.T, device string) core.PushState {
	t.Helper()
	state, err := r.stateOf(device)
	require.NoError(t, err)
	return state
}

// waited is the retry waits asked for so far; nil when there are none.
func (r *pushRig) waited() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.waits...)
}

var (
	pushTestWaiting  = pushJob{Kind: pushWaiting, Session: 42, Title: "fix the build", Body: "permission: Bash", Badge: 1}
	pushTestFinished = pushJob{Kind: pushFinished, Session: 42, Title: "fix the build", Body: pushFinishedBody, Badge: 0}
)

func TestPusher_PostsAnEncryptedPushWithItsHeaders(t *testing.T) {
	rig := newPushRig(t, "iPhone")

	rig.p.deliver(pushTestWaiting)

	reqs := rig.svc.seen()
	require.Len(t, reqs, 1)
	req := reqs[0]
	assert.Equal(t, "/iPhone", req.path)
	assert.Equal(t, "aes128gcm", req.header.Get("Content-Encoding"))
	assert.Equal(t, "3600", req.header.Get("TTL"))
	assert.Equal(t, "high", req.header.Get("Urgency"))
	assert.Equal(t, "AAAAAAAAACo", req.header.Get("Topic"))
	assert.True(t, strings.HasPrefix(req.header.Get("Authorization"), "vapid t="), req.header.Get("Authorization"))
	assert.NotEmpty(t, req.body)
	assert.NotContains(t, string(req.body), "permission", "the push service sees ciphertext only")
	assert.Equal(t, core.PushOK, rig.state(t, "iPhone"), "the model heard the result")

	rig.svc.forget()
	rig.p.deliver(pushTestFinished)
	require.Len(t, rig.svc.seen(), 1)
	assert.Equal(t, "normal", rig.svc.seen()[0].header.Get("Urgency"))
}

func TestPusher_RecordsEachOutcome(t *testing.T) {
	date := pushTestStart.Add(10 * time.Second).UTC().Format(http.TimeFormat)
	created := pushReply{status: http.StatusCreated}
	baseline := core.PushFailing(pushTestBaseline)
	cases := []struct {
		name     string
		replies  []pushReply
		want     core.PushState
		requests int
		waits    []time.Duration
	}{
		{"created", []pushReply{created}, core.PushOK, 1, nil},
		{"accepted", []pushReply{{status: http.StatusAccepted}}, core.PushOK, 1, nil},
		{"not found", []pushReply{{status: http.StatusNotFound}}, core.PushExpired, 1, nil},
		{"gone", []pushReply{{status: http.StatusGone}}, core.PushExpired, 1, nil},
		{"bad request", []pushReply{{status: http.StatusBadRequest}}, core.PushFailing(400), 1, nil},
		{"forbidden: the VAPID token was refused", []pushReply{{status: http.StatusForbidden}}, core.PushFailing(403), 1, nil},
		{"payload too large", []pushReply{{status: http.StatusRequestEntityTooLarge}}, core.PushFailing(413), 1, nil},
		{"redirect", []pushReply{{status: http.StatusTemporaryRedirect}}, core.PushFailing(307), 1, nil},
		{
			"429 with Retry-After, then created",
			[]pushReply{{status: http.StatusTooManyRequests, retryAfter: "7"}, created},
			core.PushOK, 2, []time.Duration{7 * time.Second},
		},
		{
			"429 with a Retry-After date, then created",
			[]pushReply{{status: http.StatusTooManyRequests, retryAfter: date}, created},
			core.PushOK, 2, []time.Duration{10 * time.Second},
		},
		{
			"Retry-After at the longest the sender waits",
			[]pushReply{{status: http.StatusTooManyRequests, retryAfter: "30"}, created},
			core.PushOK, 2, []time.Duration{30 * time.Second},
		},
		{
			"Retry-After past it is given up on",
			[]pushReply{{status: http.StatusTooManyRequests, retryAfter: "31"}},
			core.PushFailing(429), 1, nil,
		},
		{
			"an hour's Retry-After is given up on",
			[]pushReply{{status: http.StatusServiceUnavailable, retryAfter: "3600"}},
			core.PushFailing(503), 1, nil,
		},
		{
			"429 for good, three retries at its Retry-After",
			[]pushReply{{status: http.StatusTooManyRequests, retryAfter: "2"}},
			core.PushFailing(429), 4, []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second},
		},
		{
			"503 twice, then created, backing off",
			[]pushReply{{status: http.StatusServiceUnavailable}, {status: http.StatusServiceUnavailable}, created},
			core.PushOK, 3, []time.Duration{time.Second, 2 * time.Second},
		},
		{
			"503 for good, three retries backing off",
			[]pushReply{{status: http.StatusServiceUnavailable}},
			core.PushFailing(503), 4, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		},
		{
			"429, then gone",
			[]pushReply{{status: http.StatusTooManyRequests}, {status: http.StatusGone}},
			core.PushExpired, 2, []time.Duration{time.Second},
		},
		{
			"a transport error, then created",
			[]pushReply{{drop: true}, created},
			core.PushOK, 2, []time.Duration{time.Second},
		},
		{
			"transport errors for good record nothing",
			[]pushReply{{drop: true}},
			baseline, 4, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newPushRig(t, "iPhone")
			rig.svc.script(c.replies...)

			rig.p.deliver(pushTestWaiting)

			assert.Equal(t, c.want, rig.state(t, "iPhone"))
			assert.Len(t, rig.svc.seen(), c.requests)
			assert.Equal(t, c.waits, rig.waited())
			if c.want == core.PushExpired {
				assert.Empty(t, rig.s.loop.PushTargets(), "the model dropped the subscription")
				rig.svc.forget()
				rig.p.deliver(pushTestWaiting)
				assert.Empty(t, rig.svc.seen(), "and the next push goes nowhere")
			}
		})
	}
}

func TestPusher_HonoursEachDevicesSettings(t *testing.T) {
	rig := newPushRig(t)
	at := rig.clock.Now()
	rig.addDevice(t, "both", core.NotifyChange{})
	rig.addDevice(t, "waitingOnly", core.NotifyChange{Finished: pushPtr(false)})
	rig.addDevice(t, "finishedOnly", core.NotifyChange{Waiting: pushPtr(false)})
	rig.addDevice(t, "off", core.NotifyChange{Waiting: pushPtr(false), Finished: pushPtr(false)})
	rig.addDevice(t, "paused", core.NotifyChange{PausedUntil: pushPtr(at.Add(time.Hour))})
	rig.addDevice(t, "pauseEndsNow", core.NotifyChange{PausedUntil: pushPtr(at)})
	rig.addDevice(t, "pauseOver", core.NotifyChange{PausedUntil: pushPtr(at.Add(-time.Minute))})

	rig.p.deliver(pushTestWaiting)
	assert.ElementsMatch(t, []string{"both", "waitingOnly", "pauseEndsNow", "pauseOver"}, rig.svc.requested())

	rig.svc.forget()
	rig.p.deliver(pushTestFinished)
	assert.ElementsMatch(t, []string{"both", "finishedOnly", "pauseEndsNow", "pauseOver"}, rig.svc.requested())
}

func TestPusher_AtMostThirtyPushesAMinutePerDevice(t *testing.T) {
	rig := newPushRig(t, "iPhone")
	for i := 0; i < pushPerMinute; i++ {
		rig.p.deliver(pushTestWaiting)
	}
	require.Len(t, rig.svc.seen(), pushPerMinute)

	rig.p.deliver(pushTestWaiting)
	assert.Len(t, rig.svc.seen(), pushPerMinute, "the 31st push of the minute is dropped")

	// The model orders its devices by name, so iPad comes before iPhone.
	rig.addDevice(t, "iPad", core.NotifyChange{})
	rig.svc.forget()
	rig.p.deliver(pushTestWaiting)
	assert.Equal(t, []string{"iPad"}, rig.svc.requested(), "the limit is the device's own")

	rig.svc.forget()
	rig.clock.Advance(59 * time.Second)
	rig.p.deliver(pushTestWaiting)
	assert.Equal(t, []string{"iPad"}, rig.svc.requested(), "59 seconds on, the first thirty still count")

	rig.svc.forget()
	rig.clock.Advance(2 * time.Second)
	rig.p.deliver(pushTestWaiting)
	assert.Equal(t, []string{"iPad", "iPhone"}, rig.svc.requested(), "a minute on, they have left the window")
}

func TestPusher_SendsNothingItCannotSign(t *testing.T) {
	baseline := core.PushFailing(pushTestBaseline)

	t.Run("no VAPID key", func(t *testing.T) {
		rig := newPushRig(t, "iPhone")
		rig.p.env.host = keylessHost{rig.s.loop}
		rig.p.deliver(pushTestWaiting)
		assert.Empty(t, rig.svc.seen())
		assert.Equal(t, baseline, rig.state(t, "iPhone"), "a push the daemon could not sign says nothing about the subscription")
	})
	t.Run("a development origin cannot be a VAPID subject", func(t *testing.T) {
		rig := newPushRig(t, "iPhone")
		rig.p.env.origin = func() string { return "http://127.0.0.1:7420" }
		rig.p.deliver(pushTestWaiting)
		assert.Empty(t, rig.svc.seen())
		assert.Equal(t, baseline, rig.state(t, "iPhone"), "a push the daemon could not build says nothing about the subscription")
	})
	t.Run("one unusable subscription leaves the others", func(t *testing.T) {
		rig := newPushRig(t, "good")
		bad := core.PushTarget{
			Device:       "bad",
			Notify:       core.NotifySettings{Waiting: true, Finished: true},
			Subscription: pushTestSub(t, "bad"),
		}
		bad.Subscription.P256DH = "AAAA"
		rig.p.env.host = extraTargetHost{WebHost: rig.s.loop, extra: bad}
		rig.p.deliver(pushTestWaiting)
		assert.Equal(t, []string{"good"}, rig.svc.requested())
		assert.Equal(t, core.PushOK, rig.state(t, "good"))
	})
}

func TestPusher_QueueHoldsAtMostOneHundredPushes(t *testing.T) {
	p := newPusherWith(pushEnv{now: time.Now})
	t.Cleanup(p.close)
	for i := 0; i < pushQueueMax; i++ {
		require.True(t, p.enqueue(pushTestWaiting), "push %d", i)
	}

	assert.False(t, p.enqueue(pushTestWaiting), "the 101st is dropped")
	assert.Len(t, p.queue, pushQueueMax)
}

func TestPusher_TheSenderPostsWhatApplyQueues(t *testing.T) {
	rig := newPushRig(t, "iPhone")
	views := func(status session.Status) []core.Event {
		return []core.Event{core.ViewsChanged{WS: 10, Views: []core.InstanceView{pushTestView(7, status)}}}
	}

	rig.p.apply(views(session.Running))
	assert.Empty(t, rig.p.queue, "the first projection is a baseline")

	rig.p.apply(views(session.Prompting))
	require.Len(t, rig.p.queue, 1)
	rig.p.apply(views(session.Prompting))
	assert.Len(t, rig.p.queue, 1, "the same state published again queues nothing")

	rig.p.start()
	require.Eventually(t, func() bool {
		state, err := rig.stateOf("iPhone")
		return err == nil && state == core.PushOK
	}, 5*time.Second, 5*time.Millisecond, "the sender never recorded the push")
	reqs := rig.svc.seen()
	require.Len(t, reqs, 1)
	assert.Equal(t, "AAAAAAAAAAc", reqs[0].header.Get("Topic"))
	assert.Equal(t, "high", reqs[0].header.Get("Urgency"))
}

func TestPusher_CloseEndsARetryWait(t *testing.T) {
	rig := newPushRig(t, "iPhone")
	rig.svc.script(pushReply{status: http.StatusServiceUnavailable})
	rig.p.wait = pushSleep
	rig.p.start()
	require.True(t, rig.p.enqueue(pushTestWaiting))
	require.Eventually(t, func() bool { return len(rig.svc.seen()) == 1 }, 5*time.Second, 5*time.Millisecond)

	done := make(chan struct{})
	go func() {
		rig.p.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("close waited out the retries")
	}

	assert.False(t, rig.p.enqueue(pushTestWaiting), "a closed pusher takes nothing")
	assert.Equal(t, core.PushFailing(pushTestBaseline), rig.state(t, "iPhone"), "a push cut short records nothing")
}

// The layer's recoverPanic stops the layer (Layer.fail) on a goroutine of its
// own, and that stop closes the pusher, which waits for the sender. If a stop
// ever ran on the panicking goroutine itself, close would wait on a sender
// that is waiting on it: the sender marks itself done before its panic is
// recovered, so it never does.
func TestPusher_ASenderThatPanicsIsDoneBeforeItIsRecovered(t *testing.T) {
	rig := newPushRig(t, "iPhone")
	rig.p.env.host = panicHost{rig.s.loop}
	outcome := make(chan string, 1)
	rig.p.env.recoverPanic = func(where string) {
		if recover() == nil {
			return
		}
		closed := make(chan struct{})
		go func() {
			rig.p.close()
			close(closed)
		}()
		select {
		case <-closed:
			outcome <- where + ": closed"
		case <-time.After(2 * time.Second):
			outcome <- where + ": close waited on the panicking sender"
		}
	}
	rig.p.start()
	require.True(t, rig.p.enqueue(pushTestWaiting))

	select {
	case got := <-outcome:
		assert.Equal(t, "push: closed", got)
	case <-time.After(10 * time.Second):
		t.Fatal("the sender never panicked")
	}
}

func TestPusher_NeverFollowsARedirect(t *testing.T) {
	var elsewhereHits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		elsewhereHits.Add(1)
	}))
	t.Cleanup(elsewhere.Close)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(service.Close)

	p := newPusherWith(pushEnv{now: time.Now})
	t.Cleanup(p.close)
	resp, err := p.httpc.Post(service.URL, "application/octet-stream", strings.NewReader("x"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	assert.Zero(t, elsewhereHits.Load(), "the redirect's target was never contacted")
}

// pushStack is a webStack whose layer listens on a loopback port, with the
// device "phone" paired through the pairing route.
type pushStack struct {
	*webStack
	addr   string
	cookie *http.Cookie
}

// newPushStack boots the stack and starts the layer, then enables the web and
// pairs the phone. When wrap is set the layer's host is wrap(the model), which
// is how a test gives the layer a host that fails where the model would not.
func newPushStack(t *testing.T, wrap func(core.WebHost) core.WebHost) *pushStack {
	t.Helper()
	s := newWebStack(t)
	if wrap == nil {
		s.start(t)
	} else {
		layer, err := Start(Options{Server: s.srv, Host: wrap(s.loop), Hostname: "testhost", Build: "test build"})
		require.NoError(t, err)
		s.layer = layer
	}
	addr, _ := s.enable(t)
	return &pushStack{webStack: s, addr: addr, cookie: s.pair(t, addr, "phone")}
}

// asPhone adds the paired phone's cookie to a request.
func (ps *pushStack) asPhone(r *http.Request) { r.AddCookie(ps.cookie) }

func TestStart_BuildsThePusherAndAddsItAsASink(t *testing.T) {
	ps := newPushStack(t, nil)
	p := ps.layer.push

	require.NotNil(t, p, "startParts builds the pusher")
	assert.Contains(t, ps.layer.sinks, sink(p), "the drain hands the pusher every Sync's events")
	assert.Equal(t, pushTimeout, p.httpc.Timeout, "a push gets 10 seconds")
	assert.Same(t, ps.loop, p.env.host, "the pusher reads and records through the layer's host")
	configured := ps.connect(t).Web().Origin
	require.NotEmpty(t, configured, "the stack enabled the web with an origin")
	assert.Equal(t, configured, p.env.origin(), "the VAPID subject is the origin the model is configured with")
	assert.WithinDuration(t, time.Now(), p.env.now(), time.Minute, "the clock is the layer's")
}

func TestClose_StopsThePusher(t *testing.T) {
	ps := newPushStack(t, nil)
	p := ps.layer.push
	// No device has a subscription, so the sender finds nobody to post to.
	require.True(t, p.enqueue(pushTestWaiting), "a running layer's pusher takes a push")

	ps.layer.Close()

	assert.False(t, p.enqueue(pushTestWaiting), "closing the layer stops the pusher")
}

func TestRoutePush_ServesTheKeyToAPairedDevice(t *testing.T) {
	ps := newPushStack(t, nil)
	key, err := ps.loop.VAPIDKey()
	require.NoError(t, err)

	resp := httpDo(t, ps.addr, http.MethodGet, "/api/v1/push-key", "", ps.asPhone)
	body := bodyText(t, resp)

	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	var got struct {
		Key string `json:"key"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(key.Public), got.Key)
	raw, err := base64.RawURLEncoding.DecodeString(got.Key)
	require.NoError(t, err)
	assert.Len(t, raw, 65, "an uncompressed P-256 point")
	assert.Equal(t, byte(4), raw[0])
}

func TestRoutePush_RefusesWhoIsNotPaired(t *testing.T) {
	ps := newPushStack(t, nil)
	key, err := ps.loop.VAPIDKey()
	require.NoError(t, err)
	encoded := base64.RawURLEncoding.EncodeToString(key.Public)

	for _, c := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"no cookie", func(*http.Request) {}},
		{"a cookie no device holds", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: deviceToken(9)})
		}},
	} {
		resp := httpDo(t, ps.addr, http.MethodGet, "/api/v1/push-key", "", c.mutate)
		body := bodyText(t, resp)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, c.name)
		assert.NotContains(t, body, encoded, c.name)
	}

	require.NoError(t, ps.connect(t).RevokeDevice("phone"))
	resp := httpDo(t, ps.addr, http.MethodGet, "/api/v1/push-key", "", ps.asPhone)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the phone's own cookie, once its device is revoked")
	assert.NotContains(t, bodyText(t, resp), encoded)
}

func TestRoutePush_WithoutAKeyIs503(t *testing.T) {
	ps := newPushStack(t, func(h core.WebHost) core.WebHost { return keylessHost{h} })

	resp := httpDo(t, ps.addr, http.MethodGet, "/api/v1/push-key", "", ps.asPhone)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.JSONEq(t, `{"error":"push is not available"}`, bodyText(t, resp))
}

func TestRoutePush_IsGETOnly(t *testing.T) {
	ps := newPushStack(t, nil)

	resp := httpDo(t, ps.addr, http.MethodPost, "/api/v1/push-key", "{}", ps.asPhone)

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, http.StatusOK, httpDo(t, ps.addr, http.MethodGet, "/api/v1/push-key", "", ps.asPhone).StatusCode,
		"the same device and path, by GET")
}

// A panic in the sender stops the whole layer, not just the sender: the
// sender's recoverPanic is Layer.fail's way in, and fail's stop closes the
// pusher, ends the streams, closes the listener and raises a notice for the
// next TUI.
func TestPusher_APanicInTheSenderStopsTheWholeLayer(t *testing.T) {
	ps := newPushStack(t, func(h core.WebHost) core.WebHost { return panicHost{h} })
	p := ps.layer.push

	require.True(t, p.enqueue(pushTestWaiting))

	ps.waitNotice(t, "push")
	requireNoListener(t, ps.addr)
	require.Eventually(t, func() bool { return p.ctx.Err() != nil }, 5*time.Second, 5*time.Millisecond,
		"the layer's stop closed the pusher")

	closed := make(chan struct{})
	go func() {
		ps.layer.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung after the sender panicked")
	}
}

func TestPush_ServiceWorkerReadsEveryPayloadKeyAndHandlesBothEvents(t *testing.T) {
	raw, err := pushJob{Kind: pushWaiting, Session: 1}.payload()
	require.NoError(t, err)
	var keys map[string]any
	require.NoError(t, json.Unmarshal(raw, &keys))
	require.NotEmpty(t, keys)

	sw, err := os.ReadFile("static/sw.js")
	require.NoError(t, err)
	for key := range keys {
		assert.Contains(t, string(sw), "data."+key, "sw.js never reads the push payload's %q", key)
	}
	assert.Contains(t, string(sw), `addEventListener("push"`)
	assert.Contains(t, string(sw), `addEventListener("notificationclick"`)
}
