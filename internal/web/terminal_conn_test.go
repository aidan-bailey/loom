package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
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

// failingHijackWriter is a writer whose Hijack fails, and which keeps every status
// written to it, unlike a ResponseRecorder, which keeps only the first.
type failingHijackWriter struct {
	header   http.Header
	statuses []int
}

func (w *failingHijackWriter) Header() http.Header         { return w.header }
func (w *failingHijackWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *failingHijackWriter) WriteHeader(code int)        { w.statuses = append(w.statuses, code) }
func (w *failingHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("no hijack here")
}

func TestRecordHijack_AFailedHijackAnswers500AndGivesNoConnection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	w := &failingHijackWriter{header: http.Header{}}
	rw, hijacked := recordHijack(w)
	ws, err := websocket.Accept(rw, r, nil)
	require.Error(t, err)
	assert.Nil(t, ws)
	assert.Nil(t, hijacked())
	assert.Contains(t, w.statuses, http.StatusInternalServerError, "the failed upgrade is answered 500")
}

// dialRawTerm opens a TCP connection to srv and upgrades it by hand, so a test
// can put on the wire what the library's own client never sends, such as a
// frame header that announces more than follows. It returns the connection
// past the handshake.
func dialRawTerm(t *testing.T, srv *httptest.Server) net.Conn {
	t.Helper()
	nc, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	require.NoError(t, nc.SetDeadline(time.Now().Add(30*time.Second)))
	_, err = fmt.Fprintf(nc, "GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n", srv.Listener.Addr())
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(nc), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	return nc
}

// rawFrameHeader is the header of a masked client frame with FIN set and the
// opcode op (2 is binary), announcing n payload bytes in the 64-bit length
// form, with a mask key of zeros so the payload goes as it is.
func rawFrameHeader(op byte, n uint64) []byte {
	hdr := []byte{0x80 | op, 0x80 | 127}
	hdr = binary.BigEndian.AppendUint64(hdr, n)
	return append(hdr, 0, 0, 0, 0)
}

func TestTermConn_TheFirstResizeOpensTheAttachAtThatSizeAndLaterOnesResizeIt(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 120, 40)
	expectLive(t, rig.client)
	assert.Equal(t, [][2]int{{120, 40}}, fake.calls())

	sendResize(t, rig.client, 100, 30)
	sendTerm(t, rig.client, websocket.MessageBinary, []byte("ls"))
	sendTerm(t, rig.client, websocket.MessageBinary, []byte("\r"))
	waitUntil(t, "the attach to hear the resize and the keys", func() bool {
		return fake.shell.input() == "ls\r" && len(fake.shell.sizes()) == 1
	})
	assert.Equal(t, [][2]int{{100, 30}}, fake.shell.sizes())
	assert.Len(t, fake.calls(), 1, "a later resize resizes the attach; it never opens another")

	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ABadFirstFrameEndsItAndOpensNoAttach(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   websocket.MessageType
		frame string
	}{
		{"bytes before the first resize", websocket.MessageBinary, "ls\r"},
		{"columns below the minimum", websocket.MessageText, resizeJSON(1, 24)},
		{"columns above the maximum", websocket.MessageText, resizeJSON(501, 24)},
		{"rows below the minimum", websocket.MessageText, resizeJSON(80, 0)},
		{"rows above the maximum", websocket.MessageText, resizeJSON(80, 201)},
		{"a negative size", websocket.MessageText, resizeJSON(-5, 24)},
		{"text that is not JSON", websocket.MessageText, "not json"},
		{"JSON that is not a resize", websocket.MessageText, `{}`},
		{"a null resize", websocket.MessageText, `{"resize":null}`},
		{"a field the frame does not take", websocket.MessageText, `{"resize":{"cols":80,"rows":24},"extra":1}`},
		{"a field the size does not take", websocket.MessageText, `{"resize":{"cols":80,"rows":24,"x":1}}`},
		{"a size that is a string", websocket.MessageText, `{"resize":{"cols":"80","rows":24}}`},
		{"a size that is not whole", websocket.MessageText, `{"resize":{"cols":80.5,"rows":24}}`},
		{"a second value after the resize", websocket.MessageText, resizeJSON(80, 24) + ` {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendTerm(t, rig.client, tc.typ, []byte(tc.frame))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.Equal(t, "a frame loom does not understand", ce.Reason)
			assert.Empty(t, fake.calls(), "a bad first frame opens no attach")
		})
	}
}

func TestTermConn_EachBoundOfTheSizeIsInclusive(t *testing.T) {
	for _, size := range [][2]int{{2, 24}, {500, 24}, {80, 1}, {80, 200}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, size[0], size[1])
			expectLive(t, rig.client)
			assert.Equal(t, [][2]int{size}, fake.calls())
			rig.conn.end(termEndOff)
			rig.finish(t)
		})
	}
}

func TestTermConn_ABadLaterFrameEndsItAndClosesTheAttach(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
	}{
		{"a size out of bounds", resizeJSON(501, 24)},
		{"text that is not a resize", `{"state":"live"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			sendTerm(t, rig.client, websocket.MessageText, []byte(tc.frame))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
			assert.Empty(t, fake.shell.sizes(), "a refused size is never applied")
		})
	}
}

func TestTermConn_TheFrameCapIsTheConnectionsOwn(t *testing.T) {
	live := func(t *testing.T) (*termRig, *termOpenFake) {
		fake := newTermOpenFake()
		rig := startTermConn(t, termRigOpts{open: fake.open})
		sendResize(t, rig.client, 80, 24)
		expectLive(t, rig.client)
		return rig, fake
	}
	t.Run("a frame of exactly the cap is typed into the attach", func(t *testing.T) {
		rig, fake := live(t)
		sendTerm(t, rig.client, websocket.MessageBinary, bytes.Repeat([]byte("a"), termFrameMax))
		waitUntil(t, "the whole frame to reach the attach", func() bool {
			return len(fake.shell.input()) == termFrameMax
		})
		rig.conn.end(termEndOff)
		rig.finish(t)
	})
	for _, tc := range []struct {
		name string
		size int
	}{
		{"one byte over the cap", termFrameMax + 1},
		{"a frame far over it", 1 << 20},
	} {
		t.Run(tc.name+" is a bad frame, never the library's 1009", func(t *testing.T) {
			rig, fake := live(t)
			sendTerm(t, rig.client, websocket.MessageBinary, bytes.Repeat([]byte("a"), tc.size))
			end, _, ce := rig.finish(t)
			assert.Equal(t, termEndBadFrame, end)
			assert.Equal(t, websocket.StatusCode(4006), ce.Code)
			assert.Less(t, len(fake.shell.input()), tc.size, "an oversized frame is never typed in")
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_AnOversizedFrameThatStallsNeverLeavesRunBlocked(t *testing.T) {
	srv, _, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.drainWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)

	// A masked binary frame whose header announces 1 GiB, then a little more
	// than the cap of it, and then nothing: the sender stalls inside the frame
	// and keeps the TCP connection open.
	_, err := nc.Write(rawFrameHeader(2, 1<<30))
	require.NoError(t, err)
	_, err = nc.Write(bytes.Repeat([]byte("a"), termFrameMax+4096))
	require.NoError(t, err)

	start := time.Now()
	assert.Equal(t, termEnd{}, waitTermEnd(t, result),
		"the sender stalled, so no close frame could reach it")
	assert.Less(t, time.Since(start), 3*time.Second, "run returns within the drain bound, not when TCP gives up")
}

func TestTermConn_AFrameThatStallsAfterTheCloseFrameNeverLeavesRunBlocked(t *testing.T) {
	srv, _, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)

	// A complete binary frame one byte over the cap: the reader discards it
	// whole and ends the connection with termEndBadFrame, and loom's close
	// frame goes out. The browser never answers it. It starts another frame
	// instead, announcing 1 GiB, sends a few bytes of it and stalls: the
	// library's Close reads it byte by byte with no deadline.
	over := bytes.Repeat([]byte("a"), termFrameMax+1)
	_, err := nc.Write(append(rawFrameHeader(2, uint64(len(over))), over...))
	require.NoError(t, err)
	_, err = nc.Write(append(rawFrameHeader(2, 1<<30), "abcd"...))
	require.NoError(t, err)

	assert.Equal(t, termEndBadFrame, waitTermEndWithin(t, result, 2*time.Second),
		"run returns within the close bound, with the ending it sent")
}

func TestTermConn_AnEndingFromOutsideDuringAFrameThatStallsNeverLeavesRunBlocked(t *testing.T) {
	srv, started, result := newTermServer(t, termRigOpts{
		tune: func(c *termConn) { c.closeWait = 200 * time.Millisecond },
	})
	nc := dialRawTerm(t, srv)
	var st termStarted
	select {
	case st = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never built its connection")
	}

	// Part of a frame that announces 1000 bytes: the reader is inside it. The
	// route then ends the connection from outside (a revoked device, a rebind),
	// the browser sends one more byte and stalls. The pauses put the events in
	// that order; the assertion holds in any order.
	_, err := nc.Write(append(rawFrameHeader(2, 1000), bytes.Repeat([]byte("a"), 10)...))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	st.conn.end(termEndRevoked)
	time.Sleep(100 * time.Millisecond)
	_, err = nc.Write([]byte("a"))
	require.NoError(t, err)

	assert.Equal(t, termEndRevoked, waitTermEndWithin(t, result, 2*time.Second),
		"run returns within the close bound, with the ending it sent")
}

func TestTermConn_AFailedAttachEndsWithNoAttachAndTellsTheAppNoCause(t *testing.T) {
	fake := newTermOpenFake()
	fake.err = errors.New("tmux: no server running on /tmp/tmux-1000/default")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndNoAttach, end)
	assert.Equal(t, websocket.StatusCode(4007), ce.Code)
	assert.Equal(t, "loom could not attach to the shell", ce.Reason)
}

func TestTermConn_AnEndingBeforeRunOpensNoAttach(t *testing.T) {
	fake := newTermOpenFake()
	sent := make(chan struct{})
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		before: func(c *termConn) {
			<-sent // the browser's resize is already on the wire
			c.end(termEndOff)
		},
	})
	sendResize(t, rig.client, 80, 24)
	close(sent)
	end, frames, ce := rig.finish(t)
	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, fake.calls(), "an ending decided before run opens nothing")
	assert.Empty(t, frames)
}

func TestTermConn_AnAttachThatOpensAfterTheEndingIsClosedUnused(t *testing.T) {
	fake := newTermOpenFake()
	fake.gate = make(chan struct{})
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	waitUntil(t, "open to be called", func() bool { return len(fake.calls()) == 1 })
	rig.conn.end(termEndOff)

	// The close frame goes out after run has closed the attach it holds (none
	// yet), so the open that returns now must close its own.
	frames, ce, err := readUntilClose(rig.client)
	require.NoError(t, err)
	require.NotNil(t, ce)
	close(fake.gate)
	assert.Equal(t, termEndOff, waitTermEnd(t, rig.result))
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, frames, "an ended connection says nothing of a late attach")
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}

// readBinaryN reads binary frames until n bytes have arrived.
func readBinaryN(t *testing.T, c *websocket.Conn, n int) []byte {
	t.Helper()
	var got []byte
	for len(got) < n {
		typ, b := readTerm(t, c)
		require.Equal(t, websocket.MessageBinary, typ)
		got = append(got, b...)
	}
	return got
}

func TestTermConn_TheHistoryGoesFirstThenRowsLineFeedsThenTheAttachsOutput(t *testing.T) {
	fake := newTermOpenFake()
	fake.hist = []byte("one\r\ntwo\r\n")
	fake.shell.feed <- []byte("early")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)

	var before []byte
	for {
		typ, b := readTerm(t, rig.client)
		if typ == websocket.MessageText {
			assert.Equal(t, `{"state":"live"}`, string(b))
			break
		}
		before = append(before, b...)
	}
	assert.Equal(t, "one\r\ntwo\r\n"+strings.Repeat("\n", 23), string(before),
		"the history, then one line feed per row of the pane but one: the cursor's fresh row is the first")
	assert.Equal(t, "early", string(readBinaryN(t, rig.client, 5)),
		"the output the attach made before the history went out comes after it")

	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_NoHistoryIsNoLineFeeds(t *testing.T) {
	fake := newTermOpenFake()
	fake.shell.feed <- []byte("x")
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client) // the very first message: nothing is sent before it
	assert.Equal(t, "x", string(readBinaryN(t, rig.client, 1)))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ALongHistoryIsSentInFramesOfAtMostTheCap(t *testing.T) {
	fake := newTermOpenFake()
	fake.hist = bytes.Repeat([]byte("history line\r\n"), 12000) // about 164 KiB
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	var got []byte
	frames := 0
	for {
		typ, b := readTerm(t, rig.client)
		if typ == websocket.MessageText {
			break
		}
		frames++
		assert.LessOrEqual(t, len(b), termFrameMax)
		got = append(got, b...)
	}
	assert.Equal(t, string(fake.hist)+strings.Repeat("\n", 23), string(got))
	assert.GreaterOrEqual(t, frames, 3)
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_TheOutputIsFilteredOfAlternateScreenSwitches(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	// A switch split across two reads, as the pty can deliver it.
	fake.shell.feed <- []byte("\x1b[?1049hhello \x1b[?10")
	fake.shell.feed <- []byte("49lworld")
	got := readBinaryN(t, rig.client, len("hello world"))
	assert.Equal(t, "hello world", string(got))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_OutputArrivesInOrder(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: fake.open})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	var want strings.Builder
	for i := 0; i < 200; i++ {
		s := fmt.Sprintf("line %03d\r\n", i)
		want.WriteString(s)
		fake.shell.feed <- []byte(s)
	}
	assert.Equal(t, want.String(), string(readBinaryN(t, rig.client, want.Len())))
	rig.conn.end(termEndOff)
	rig.finish(t)
}

func TestTermConn_ABrowserThatFallsBehindIsEndedAndNeverBlocksTheAttach(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) { c.outBuffer = 64 << 10 },
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)

	// An attach that never waits for the browser...
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	go func() {
		for {
			select {
			case fake.shell.feed <- chunk:
			case <-fake.shell.closed:
				return
			}
		}
	}()
	// ...and a browser that reads, but slower than the attach writes.
	type outcome struct {
		ce  *websocket.CloseError
		err error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for {
			if _, _, err := rig.client.Read(ctx); err != nil {
				var ce websocket.CloseError
				if errors.As(err, &ce) {
					outcomes <- outcome{ce: &ce}
				} else {
					outcomes <- outcome{err: err}
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	assert.Equal(t, termEndSlow, waitTermEnd(t, rig.result))
	select {
	case o := <-outcomes:
		require.NoError(t, o.err)
		assert.Equal(t, websocket.StatusCode(4005), o.ce.Code)
		assert.Equal(t, "the browser fell behind", o.ce.Reason)
	case <-time.After(20 * time.Second):
		t.Fatal("the browser never saw the ending")
	}
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}

func TestOutQueue_HoldsAtMostItsLimitAndKeepsWhatItHolds(t *testing.T) {
	ctx := context.Background()
	q := newOutQueue(10)
	assert.True(t, q.push([]byte("12345")))
	assert.True(t, q.push([]byte("67890")), "exactly the limit fits")
	assert.False(t, q.push([]byte("x")), "one byte past the limit does not")

	got, err := q.pop(ctx)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(got), "what the queue holds stays, in order")
	assert.True(t, q.push([]byte("abcde")), "a pop makes room")

	q.finish()
	var rest []string
	for {
		b, err := q.pop(ctx)
		if err != nil {
			assert.ErrorIs(t, err, io.EOF, "a finished queue ends only once it is empty")
			break
		}
		rest = append(rest, string(b))
	}
	assert.Equal(t, []string{"67890", "abcde"}, rest)
}

func TestOutQueue_AnEmptyQueueTakesAChunkLargerThanItsLimit(t *testing.T) {
	q := newOutQueue(4)
	assert.True(t, q.push([]byte("12345678")), "one read larger than the limit is no overflow")
	assert.False(t, q.push([]byte("x")))
}

func TestOutQueue_PushCopiesAndPopStopsOnItsContext(t *testing.T) {
	q := newOutQueue(100)
	src := []byte("abc")
	require.True(t, q.push(src))
	src[0] = 'X'
	got, err := q.pop(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "abc", string(got), "the caller may reuse its buffer")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = q.pop(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestTermConn_ABrowserThatAnswersItsPingsStaysConnected(t *testing.T) {
	var pings atomic.Int32
	rig := startTermConn(t, termRigOpts{
		tune: func(c *termConn) {
			c.pingEvery = 20 * time.Millisecond
			c.pongWait = 500 * time.Millisecond
		},
		dial: &websocket.DialOptions{OnPingReceived: func(context.Context, []byte) bool {
			pings.Add(1)
			return true // the library answers it, while the browser reads
		}},
	})
	type closed struct {
		ce  *websocket.CloseError
		err error
	}
	done := make(chan closed, 1)
	go func() {
		_, ce, err := readUntilClose(rig.client)
		done <- closed{ce, err}
	}()

	select {
	case e := <-rig.result:
		t.Fatalf("a browser that answers its pings was dropped: %+v", e)
	case <-time.After(400 * time.Millisecond):
	}
	assert.GreaterOrEqual(t, pings.Load(), int32(3), "the connection pings every termPingEvery")

	rig.conn.end(termEndOff)
	assert.Equal(t, termEndOff, waitTermEnd(t, rig.result))
	d := <-done
	require.NoError(t, d.err)
	assert.Equal(t, websocket.StatusCode(4001), d.ce.Code)
}

func TestTermConn_APingTheBrowserNeverAnswersDropsItWithNoCloseFrame(t *testing.T) {
	fake := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) {
			c.pingEvery = 20 * time.Millisecond
			c.pongWait = 50 * time.Millisecond
		},
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	// From here the browser reads nothing, so the library never answers a ping.
	start := time.Now()
	assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}

func TestTermConn_NoPingFailsWhileTheAttachIsStillOpening(t *testing.T) {
	fake := newTermOpenFake()
	fake.gate = make(chan struct{})
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		tune: func(c *termConn) {
			c.pingEvery = 50 * time.Millisecond
			c.pongWait = 300 * time.Millisecond
		},
	})
	// A ping is outstanding when the attach starts to open: the first one goes
	// out at 50 ms and would fail at 350 ms, and the browser reads nothing.
	time.Sleep(100 * time.Millisecond)
	sendResize(t, rig.client, 80, 24)
	waitUntil(t, "open to be called", func() bool { return len(fake.calls()) == 1 })

	// The reader is inside open, so it reads no pong, and the browser is not
	// reading either, which answers none: far past pingEvery and pongWait.
	select {
	case e := <-rig.result:
		t.Fatalf("a ping failed while the attach was still opening: %+v", e)
	case <-time.After(900 * time.Millisecond):
	}

	close(fake.gate)
	expectLive(t, rig.client)
	rig.conn.end(termEndOff)
	end, _, _ := rig.finish(t)
	assert.Equal(t, termEndOff, end)
}

func TestTermConn_AnAttachThatEndsByItselfAsksEndedOnceAndSendsItsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		then termEnd
		code websocket.StatusCode
	}{
		{"the session is still there", termEndExited, 4004},
		{"the session is gone", termEndSession, 4003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			var asked atomic.Int32
			rig := startTermConn(t, termRigOpts{
				open: fake.open,
				ended: func() termEnd {
					asked.Add(1)
					return tc.then
				},
			})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			fake.shell.feed <- []byte("bye\r\n")
			close(fake.shell.feed) // the shell exits

			end, frames, ce := rig.finish(t)
			assert.Equal(t, tc.then, end)
			assert.Equal(t, tc.code, ce.Code)
			assert.Equal(t, tc.then.reason, ce.Reason)
			require.Len(t, frames, 1, "what the shell said last is sent before the ending")
			assert.Equal(t, "bye\r\n", string(frames[0].data))
			assert.Equal(t, int32(1), asked.Load())
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_AnEndingLoomCausesNeverAsksEnded(t *testing.T) {
	fake := newTermOpenFake()
	var asked atomic.Int32
	rig := startTermConn(t, termRigOpts{
		open:  fake.open,
		ended: func() termEnd { asked.Add(1); return termEndExited },
	})
	sendResize(t, rig.client, 80, 24)
	expectLive(t, rig.client)
	rig.conn.end(termEndRevoked)
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndRevoked, end)
	assert.Equal(t, websocket.StatusCode(4002), ce.Code)
	assert.Equal(t, int32(0), asked.Load(), "closing the attach ends its output, which is not the shell exiting")
}

func TestTermConn_EveryWayItEndsClosesTheAttach(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger func(t *testing.T, rig *termRig)
		want    termEnd
		frame   bool // the ending is a close frame
	}{
		{"end", func(t *testing.T, r *termRig) { r.conn.end(termEndRevoked) }, termEndRevoked, true},
		{"a cancelled context", func(t *testing.T, r *termRig) { r.cancel() }, termEndStopping, true},
		{"the browser leaving", func(t *testing.T, r *termRig) { _ = r.client.CloseNow() }, termEnd{}, false},
		{"a bad frame", func(t *testing.T, r *termRig) {
			sendTerm(t, r.client, websocket.MessageText, []byte("nonsense"))
		}, termEndBadFrame, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newTermOpenFake()
			rig := startTermConn(t, termRigOpts{open: fake.open})
			sendResize(t, rig.client, 80, 24)
			expectLive(t, rig.client)
			tc.trigger(t, rig)
			if tc.frame {
				end, _, _ := rig.finish(t)
				assert.Equal(t, tc.want, end)
			} else {
				assert.Equal(t, tc.want, waitTermEnd(t, rig.result))
			}
			assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
		})
	}
}

func TestTermConn_APanicInAGoroutineGoesToRecoverPanicAndEndsTheConnection(t *testing.T) {
	fake := newTermOpenFake()
	fake.shell.panicOnRead = true
	got := make(chan string, 4)
	rig := startTermConn(t, termRigOpts{
		open: fake.open,
		recoverPanic: func(where string) {
			if r := recover(); r != nil {
				got <- where
			}
		},
	})
	sendResize(t, rig.client, 80, 24)

	select {
	case where := <-got:
		assert.Equal(t, "terminal attach", where)
	case <-time.After(5 * time.Second):
		t.Fatal("the panic never reached recoverPanic")
	}
	// The connection ends with no close frame, and run returns.
	assert.Equal(t, termEnd{}, waitTermEnd(t, rig.result))
	assert.GreaterOrEqual(t, fake.shell.closes.Load(), int32(1))
}
