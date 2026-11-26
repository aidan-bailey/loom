package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termShellFake is an attach the tests drive: feed is the output it produces
// (closing it ends the attach by itself, as a shell's exit does), and what the
// connection does to it is kept.
type termShellFake struct {
	feed   chan []byte
	closed chan struct{}
	once   sync.Once
	closes atomic.Int32

	// panicOnRead makes Read panic: set before the fake is shared.
	panicOnRead bool

	mu      sync.Mutex
	rest    []byte
	in      bytes.Buffer
	resizes [][2]int
}

func newTermShellFake() *termShellFake {
	return &termShellFake{feed: make(chan []byte, 1024), closed: make(chan struct{})}
}

func (f *termShellFake) Read(p []byte) (int, error) {
	if f.panicOnRead {
		panic("the attach's read panicked")
	}
	f.mu.Lock()
	if len(f.rest) > 0 {
		n := copy(p, f.rest)
		f.rest = f.rest[n:]
		f.mu.Unlock()
		return n, nil
	}
	f.mu.Unlock()
	select {
	case b, ok := <-f.feed:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, b)
		if n < len(b) {
			f.mu.Lock()
			f.rest = b[n:]
			f.mu.Unlock()
		}
		return n, nil
	case <-f.closed:
		return 0, io.ErrClosedPipe
	}
}

func (f *termShellFake) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.Write(p)
}

func (f *termShellFake) Resize(cols int, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *termShellFake) Close() error {
	f.closes.Add(1)
	f.once.Do(func() { close(f.closed) })
	return nil
}

// input is what the connection has written to the attach so far.
func (f *termShellFake) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.String()
}

// sizes are the resizes the connection has applied to the attach so far.
func (f *termShellFake) sizes() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
}

// termOpenFake is a termOpen: it records each call and answers with its
// shell, its history, or its error. When gate is set, open waits for it to
// close after recording the call.
type termOpenFake struct {
	shell *termShellFake
	hist  []byte
	err   error
	gate  chan struct{}

	mu   sync.Mutex
	seen [][2]int
}

func newTermOpenFake() *termOpenFake { return &termOpenFake{shell: newTermShellFake()} }

func (o *termOpenFake) open(cols int, rows int) (termShell, []byte, error) {
	o.mu.Lock()
	o.seen = append(o.seen, [2]int{cols, rows})
	o.mu.Unlock()
	if o.gate != nil {
		<-o.gate
	}
	if o.err != nil {
		return nil, nil, o.err
	}
	return o.shell, o.hist, nil
}

// calls are the sizes open was called with.
func (o *termOpenFake) calls() [][2]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([][2]int(nil), o.seen...)
}

// termRigOpts shape startTermConn: the open and ended a connection is given
// (defaults: a fresh termOpenFake, and termEndExited), the recoverPanic
// (default: none, so a panic fails the test binary), tune (runs on the
// handler's goroutine before run, to set the connection's private fields) and
// before (runs right before run).
type termRigOpts struct {
	open         termOpen
	ended        func() termEnd
	recoverPanic func(where string)
	tune         func(c *termConn)
	before       func(c *termConn)
	dial         *websocket.DialOptions
}

// termRig is a termConn serving the server side of a real WebSocket pair; the
// test is the browser, on client.
type termRig struct {
	client *websocket.Conn
	conn   *termConn
	cancel context.CancelFunc
	result chan termEnd
}

// termStarted is what a test server's handler hands the test once it has built
// its connection: the connection, and the cancel of the context it runs under.
type termStarted struct {
	conn   *termConn
	cancel context.CancelFunc
}

// newTermServer serves a termConn built from o on each upgrade of an httptest
// server: the handler hands the connection on startedCh, runs it, and reports
// run's answer on result. startTermConn dials it with the library's client;
// a test that must put bytes on the wire the client never sends dials it by
// hand. The connection is built as the route builds it, with the hijacked
// connection recordHijack gives.
func newTermServer(t *testing.T, o termRigOpts) (*httptest.Server, chan termStarted, chan termEnd) {
	t.Helper()
	if o.open == nil {
		o.open = newTermOpenFake().open
	}
	if o.ended == nil {
		o.ended = func() termEnd { return termEndExited }
	}
	if o.recoverPanic == nil {
		o.recoverPanic = func(string) {}
	}
	startedCh := make(chan termStarted, 1)
	result := make(chan termEnd, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw, hijacked := recordHijack(w)
		ws, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		c := newTermConn(ws, hijacked(), o.open, o.ended, o.recoverPanic)
		if o.tune != nil {
			o.tune(c)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		startedCh <- termStarted{c, cancel}
		if o.before != nil {
			o.before(c)
		}
		result <- c.run(ctx)
	}))
	t.Cleanup(srv.Close)
	return srv, startedCh, result
}

func startTermConn(t *testing.T, o termRigOpts) *termRig {
	t.Helper()
	srv, startedCh, result := newTermServer(t, o)
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	client, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(srv.URL, "http"), o.dial)
	require.NoError(t, err)
	client.SetReadLimit(4 << 20)
	t.Cleanup(func() { _ = client.CloseNow() })
	select {
	case st := <-startedCh:
		t.Cleanup(st.cancel)
		return &termRig{client: client, conn: st.conn, cancel: st.cancel, result: result}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never built its connection")
		return nil
	}
}

// termFrame is one message the browser received.
type termFrame struct {
	typ  websocket.MessageType
	data []byte
}

// readUntilClose reads what the browser is sent until the connection ends,
// answering a close frame as a browser does (the library answers only while
// reading). It returns the messages, and the close frame when the end had one.
func readUntilClose(c *websocket.Conn) ([]termFrame, *websocket.CloseError, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var frames []termFrame
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				return frames, &ce, nil
			}
			return frames, nil, err
		}
		frames = append(frames, termFrame{typ, b})
	}
}

// finish drains the browser's side until the close, then waits for run's
// answer. The close must be a close frame.
func (r *termRig) finish(t *testing.T) (termEnd, []termFrame, websocket.CloseError) {
	t.Helper()
	frames, ce, err := readUntilClose(r.client)
	require.NoError(t, err, "the connection ended without a close frame")
	require.NotNil(t, ce)
	return waitTermEnd(t, r.result), frames, *ce
}

func waitTermEnd(t *testing.T, ch <-chan termEnd) termEnd {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return")
		return termEnd{}
	}
}

// waitTermEndWithin is waitTermEnd with the bound a test asserts: run must have
// returned within d.
func waitTermEndWithin(t *testing.T, ch <-chan termEnd, d time.Duration) termEnd {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(d):
		t.Fatalf("run did not return within %v", d)
		return termEnd{}
	}
}

func readTerm(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	require.NoError(t, err)
	return typ, b
}

func sendTerm(t *testing.T, c *websocket.Conn, typ websocket.MessageType, p []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Write(ctx, typ, p))
}

func resizeJSON(cols int, rows int) string {
	return fmt.Sprintf(`{"resize":{"cols":%d,"rows":%d}}`, cols, rows)
}

func sendResize(t *testing.T, c *websocket.Conn, cols int, rows int) {
	t.Helper()
	sendTerm(t, c, websocket.MessageText, []byte(resizeJSON(cols, rows)))
}

func expectLive(t *testing.T, c *websocket.Conn) {
	t.Helper()
	typ, b := readTerm(t, c)
	require.Equal(t, websocket.MessageText, typ)
	require.Equal(t, `{"state":"live"}`, string(b))
}

func TestTermEnds_AreTheContractsCodesAndReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		got    termEnd
		code   websocket.StatusCode
		reason string
	}{
		{"off", termEndOff, 4001, "the terminal is off on this host"},
		{"revoked", termEndRevoked, 4002, "this device is no longer paired"},
		{"session", termEndSession, 4003, "the session was paused or killed"},
		{"exited", termEndExited, 4004, "the shell exited"},
		{"slow", termEndSlow, 4005, "the browser fell behind"},
		{"bad frame", termEndBadFrame, 4006, "a frame loom does not understand"},
		{"no attach", termEndNoAttach, 4007, "loom could not attach to the shell"},
		{"stopping", termEndStopping, 1001, "loom is stopping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, tc.got.code)
			assert.Equal(t, tc.reason, tc.got.reason)
			// A close frame's reason may hold 123 bytes.
			assert.LessOrEqual(t, len(tc.got.reason), 123)
		})
	}
}

func TestTermConn_AnEndingIsACloseFrameWithItsCodeAndReason(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.conn.end(termEndOff)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Equal(t, "the terminal is off on this host", ce.Reason)
}

func TestTermConn_TheFirstEndingWinsFromAnyGoroutine(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.conn.end(termEndRevoked)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rig.conn.end(termEndOff)
			rig.conn.end(termEndSlow)
		}()
	}
	wg.Wait()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndRevoked, end)
	assert.Equal(t, websocket.StatusCode(4002), ce.Code)
}

func TestTermConn_ACancelledContextEndsItAsStopping(t *testing.T) {
	rig := startTermConn(t, termRigOpts{})
	rig.cancel()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	assert.Equal(t, "loom is stopping", ce.Reason)
}

func TestTermConn_AContextCancelledBeforeRunStartsStillSendsTheStoppingFrame(t *testing.T) {
	release := make(chan struct{})
	rig := startTermConn(t, termRigOpts{before: func(*termConn) { <-release }})
	rig.cancel() // run is handed a context that is already cancelled
	close(release)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	assert.Equal(t, "loom is stopping", ce.Reason)
}

func TestTermConn_TheBrowserLeavingIsTheZeroEnding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(c *websocket.Conn)
	}{
		{"with a close frame", func(c *websocket.Conn) { _ = c.Close(websocket.StatusNormalClosure, "") }},
		{"without one", func(c *websocket.Conn) { _ = c.CloseNow() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := startTermConn(t, termRigOpts{})
			tc.leave(rig.client)
			assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
		})
	}
}

func TestTermConn_RunClosesTheAttachItHolds(t *testing.T) {
	sh := newTermShellFake()
	rig := startTermConn(t, termRigOpts{tune: func(c *termConn) { c.shell = sh }})
	rig.conn.end(termEndOff)
	rig.finish(t)
	assert.GreaterOrEqual(t, sh.closes.Load(), int32(1))
}

func TestTermConn_ABrowserThatNeverAnswersTheCloseFrameIsClosedOutrightAfterTheBound(t *testing.T) {
	rig := startTermConn(t, termRigOpts{tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond }})
	// The browser reads nothing, so the library never answers loom's close
	// frame: on its own, the library's close handshake gives up only after 5 s.
	rig.conn.end(termEndOff)
	assert.Equal(t, termEndOff, waitTermEndWithin(t, rig.result, 2*time.Second),
		"run returns within the bound, with the ending it sent")
}

func TestRecordHijack_AcceptThroughItUpgradesAndGivesTheHijackedConnection(t *testing.T) {
	type seen struct{ before, after bool }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw, hijacked := recordHijack(w)
		before := hijacked() != nil
		ws, err := websocket.Accept(rw, r, nil)
		if err != nil {
			got <- seen{before, false}
			return
		}
		defer func() { _ = ws.CloseNow() }()
		raw := hijacked()
		got <- seen{before, raw != nil}
		if raw != nil {
			_ = raw.Close() // the connection under the WebSocket, closed outright
		}
	}))
	t.Cleanup(srv.Close)

	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	client, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err, "Accept given the wrapped writer upgrades")
	t.Cleanup(func() { _ = client.CloseNow() })
	assert.Equal(t, seen{before: false, after: true}, <-got,
		"nil before the hijack, the hijacked connection after it")

	// Closing the recorded connection drops the client with no close frame: it
	// is the one under the WebSocket, not some other.
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	_, _, err = client.Read(rctx)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusCode(-1), websocket.CloseStatus(err), "a dropped connection, not a close frame")
}

func TestRecordHijack_AWriterThatCannotHijackFailsTheUpgradeAndGivesNoConnection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder() // a ResponseRecorder cannot be hijacked
	rw, hijacked := recordHijack(rec)
	ws, err := websocket.Accept(rw, r, nil)
	require.Error(t, err)
	assert.Nil(t, ws)
	assert.Nil(t, hijacked())
	// The library has written 101 before it hijacks, and a recorder keeps the first
	// status, so the 500 it then tries to send is not observable here.
}
