package web

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The bounds of one terminal connection.
const (
	// termFrameMax caps a frame from the app, and the size of each frame loom
	// sends of a history.
	termFrameMax = 64 << 10
	// The size the app may ask for.
	termMinCols = 2
	termMaxCols = 500
	termMinRows = 1
	termMaxRows = 200
	// termPingEvery is how often the connection is pinged, and termPongWait
	// how long a ping may go unanswered before the connection is dropped.
	termPingEvery = 20 * time.Second
	termPongWait  = 10 * time.Second
	// termOutBuffer bounds, in bytes, the output queued for a browser that is
	// slow to read.
	termOutBuffer = 1 << 20
	// termCloseWait bounds the close handshake: the library's Close reads
	// whatever frame the browser is sending with no deadline of its own, so a
	// connection whose close has not finished this long after run started it is
	// closed outright.
	termCloseWait = 5 * time.Second
	// termWriteTimeout bounds one write to the browser: a peer that stops
	// reading without closing would otherwise hold the sender forever.
	termWriteTimeout = 30 * time.Second
	// termDrainWait bounds how long the remainder of an oversized frame is
	// read and thrown away before the connection is ended: the library's Close
	// reads what is left of a half-read frame with no timeout of its own, so a
	// sender that stalls inside one would hold Close, and the connection's slot,
	// for good.
	termDrainWait = 5 * time.Second
)

// termShell is what a connection needs of an attach: the bytes the shell
// writes, the bytes typed into it, its size, and an end that never ends the
// shell itself. *tmux.ShellAttach satisfies it.
type termShell interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols int, rows int) error
	Close() error
}

// termOpen opens the attach at a size and returns the history to send before
// its output: the rows above the visible screen, each ended by \r\n, and a
// non-empty history also ends with the SGR reset \x1b[0m, as
// tmux.ShellHistory returns it.
type termOpen func(cols int, rows int) (termShell, []byte, error)

// termEnd is a close frame's code and reason. The zero termEnd is no close
// frame from loom: the browser left or the network failed.
type termEnd struct {
	code   websocket.StatusCode
	reason string
}

// The endings the app shows, by code. The spec's rule for part 2's app is that
// it reconnects after 1001, 4005 and an ending with no close frame, and after
// no other; part 1's page never reconnects.
var (
	termEndOff      = termEnd{code: 4001, reason: "the terminal is off on this host"}
	termEndRevoked  = termEnd{code: 4002, reason: "this device is no longer paired"}
	termEndSession  = termEnd{code: 4003, reason: "the session was paused or killed"}
	termEndExited   = termEnd{code: 4004, reason: "the shell exited"}
	termEndSlow     = termEnd{code: 4005, reason: "the browser fell behind"}
	termEndBadFrame = termEnd{code: 4006, reason: "a frame loom does not understand"}
	termEndNoAttach = termEnd{code: 4007, reason: "loom could not attach to the shell"}
	termEndStopping = termEnd{code: websocket.StatusGoingAway, reason: "loom is stopping"}
)

// termConn is one upgraded connection. run serves it until one ending, which
// end or a failure of its own decides, and then closes the WebSocket and the
// attach and waits for its goroutines.
type termConn struct {
	ws *websocket.Conn
	// raw is the connection under ws (recordHijack's), closed outright when the
	// close handshake outlasts closeWait. nil is allowed, for a test that needs
	// none: the handshake is then bounded only by the library's own timeouts.
	raw          net.Conn
	open         termOpen
	ended        func() termEnd
	recoverPanic func(where string)

	// The bounds, copied so a test may shrink them before run.
	pingEvery time.Duration
	pongWait  time.Duration
	outBuffer int
	drainWait time.Duration
	closeWait time.Duration

	// ioCtx is the context every call into the library takes: the connection's
	// own, never the caller's. The library closes its socket the moment a
	// context it was given is cancelled, so a library call given run's ctx would
	// close the socket before the close frame of a stopping connection could be
	// written. run cancels ioCtx (through ioCancel) only after that frame has
	// gone out.
	ioCtx    context.Context
	ioCancel context.CancelFunc

	// done closes when the ending is decided; ending is written once, before.
	done   chan struct{}
	once   sync.Once
	ending termEnd

	// shell is the attach once open has returned it; shellClosed is set by
	// closeShell, so an attach that opens after the ending is closed unused.
	mu          sync.Mutex
	shell       termShell
	shellClosed bool

	wg sync.WaitGroup
}

// recordHijack wraps w for websocket.Accept: Accept is handed the returned
// writer in place of w, and the returned function gives the connection Accept
// hijacked through it (nil before the hijack, and when it failed). The wrapper
// hijacks through http.NewResponseController, so a w that wraps the server's
// writer and unwraps to it is hijacked as the library itself would hijack it.
func recordHijack(w http.ResponseWriter) (http.ResponseWriter, func() net.Conn) {
	h := &hijackRecorder{ResponseWriter: w}
	return h, func() net.Conn {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.conn
	}
}

// hijackRecorder is the writer recordHijack returns: it satisfies the
// http.Hijacker websocket.Accept needs, and keeps what Hijack returns.
type hijackRecorder struct {
	http.ResponseWriter

	mu   sync.Mutex
	conn net.Conn
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(h.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	return conn, brw, nil
}

// newTermConn builds the connection for ws. raw is the connection under ws, as
// recordHijack gives it: run closes it outright when the close handshake has
// not finished termCloseWait after run started it, so run returns within that
// bound whatever the browser sends or withholds; nil is allowed, for a test
// that needs none. open is called with the app's first size. ended is asked
// once, when the attach's output ends by itself, for the ending to send.
// recoverPanic is the layer's: it is deferred first in every goroutine the
// connection starts.
func newTermConn(ws *websocket.Conn, raw net.Conn, open termOpen, ended func() termEnd, recoverPanic func(where string)) *termConn {
	ioCtx, ioCancel := context.WithCancel(context.Background())
	return &termConn{
		ws:           ws,
		raw:          raw,
		open:         open,
		ended:        ended,
		recoverPanic: recoverPanic,
		pingEvery:    termPingEvery,
		pongWait:     termPongWait,
		outBuffer:    termOutBuffer,
		drainWait:    termDrainWait,
		closeWait:    termCloseWait,
		ioCtx:        ioCtx,
		ioCancel:     ioCancel,
		done:         make(chan struct{}),
	}
}

// end ends the connection with e. It never blocks and may be called from any
// goroutine, before run too, and more than once: the first ending wins.
func (c *termConn) end(e termEnd) {
	c.once.Do(func() {
		c.ending = e
		close(c.done)
	})
}

// stopped reports whether an ending has been decided.
func (c *termConn) stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// leave ends the connection with no close frame: the browser left or the
// network failed. It reads only the connection's own state: an ending already
// decided (run's stopping, an end from the route) stays, since the first one
// wins, and no library call fails on the caller's context, so a failure that
// reaches leave is never run's own stopping.
func (c *termConn) leave() {
	c.end(termEnd{})
}

// spawn runs f on a goroutine that run waits for. A panic in f recovers
// through the layer's recoverPanic, which stops the layer, and ends the
// connection with no close frame.
func (c *termConn) spawn(where string, f func()) {
	c.wg.Add(1)
	go func() {
		defer c.recoverPanic(where)
		defer c.wg.Done()
		finished := false
		defer func() {
			if !finished {
				c.end(termEnd{})
			}
		}()
		f()
		finished = true
	}()
}

// run serves the connection until it ends and returns the ending it sent: the
// zero termEnd when no close frame went out. Before it returns it has closed
// the attach and the WebSocket and its goroutines have returned.
//
// A cancelled ctx is exactly end(termEndStopping), and ctx goes no further
// than the select below: the library's calls take c.ioCtx, which is cancelled
// only once the close frame has been written. Whatever the browser sends or
// withholds, run returns within closeWait of starting the close (closeSocket).
func (c *termConn) run(ctx context.Context) termEnd {
	defer c.ioCancel()
	cctx := c.ioCtx
	c.ws.SetReadLimit(-1)
	c.spawn("terminal reader", func() { c.readLoop(cctx) })
	select {
	case <-c.done:
	case <-ctx.Done():
		c.end(termEndStopping)
		<-c.done
	}
	e := c.ending
	c.closeShell()
	c.closeSocket(e)
	c.ioCancel()
	c.wg.Wait()
	return e
}

// closeShell closes the attach, or marks it closed so one still opening is
// closed as soon as it is open.
func (c *termConn) closeShell() {
	c.mu.Lock()
	c.shellClosed = true
	sh := c.shell
	c.mu.Unlock()
	if sh != nil {
		// Closing a shell that already ended has nothing more to say.
		_ = sh.Close()
	}
}

// closeSocket sends e's close frame and closes the WebSocket; the zero
// ending closes with no frame. The close handshake's own errors (a peer that
// is already gone) change nothing: the connection is over either way.
//
// The handshake is bounded by closeWait. The library's Close reads, with no
// deadline, the rest of the frame the browser is part-way through sending and
// every data frame it sends after loom's close frame, so a browser that
// announces a large frame and stalls would hold Close, and the slot, for good.
// Close runs on a goroutine of its own, and when closeWait passes before it
// returns the hijacked connection is closed outright: every read and write
// on it then fails, Close returns, and so does every goroutine of the
// connection. With a nil raw the library's own timeouts are all there is.
func (c *termConn) closeSocket(e termEnd) {
	if e.code == 0 {
		_ = c.ws.CloseNow()
		return
	}
	closed := make(chan struct{})
	c.spawn("terminal close", func() {
		defer close(closed)
		_ = c.ws.Close(e.code, e.reason)
	})
	timer := time.NewTimer(c.closeWait)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
		if c.raw != nil {
			_ = c.raw.Close()
		}
		<-closed
	}
}

// readLoop is a stub that Task 6.2 replaces: it reads and drops every frame.
// ctx is the connection's own context.
func (c *termConn) readLoop(ctx context.Context) {
	for {
		if _, _, err := c.ws.Read(ctx); err != nil {
			c.leave()
			return
		}
	}
}
