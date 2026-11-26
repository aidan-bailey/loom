package web

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// termPaired is when the devices of these tests were paired.
var termPaired = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

// modelWith is the batch the drain hands the terminals' part when the model
// publishes the switch and the paired devices.
func modelWith(on bool, devices ...core.WebDevice) []core.Event {
	return []core.Event{core.ModelChanged{View: core.ModelView{Web: core.WebView{Terminal: on, Devices: devices}}}}
}

func pairedDev(name string, at time.Time) core.WebDevice {
	return core.WebDevice{Name: name, Paired: at}
}

// newTestTerms is a part on a layer that has a listener's origin rule and no
// client, so the model is never asked for anything. Its grace is short.
func newTestTerms(t *testing.T) *terminals {
	t.Helper()
	l := &Layer{}
	rule := originSet{{scheme: "http", host: "loom.test"}}
	l.rule.Store(&rule)
	tm := newTerminals(l)
	tm.grace = 100 * time.Millisecond
	return tm
}

// isOn is the part's copy of the switch.
func (tm *terminals) isOn() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.on
}

// knows reports whether the part has read the device.
func (tm *terminals) knows(name string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	_, ok := tm.devices[name]
	return ok
}

// slotCount is how many slots are held now.
func (tm *terminals) slotCount() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.slots)
}

// endedWith is the ending the slot was given, and whether it was given one.
func (s *termSlot) endedWith() (termEnd, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ending, s.ended
}

// logBuf is a log's destination that tests read.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// plainLog writes to buf with no timestamp, so a test compares whole lines.
func plainLog(buf *logBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}))
}

func TestTerminals_ASlotNeedsTheSwitchOnAndAKnownDevice(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	assert.ErrorIs(t, err, errTermOff, "the switch is off")

	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err, "on, for a device the part has read")
	require.NotNil(t, slot)

	_, err = tm.reserve("stranger")
	var se *statusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusServiceUnavailable, se.status, "a device the part has not read yet is asked to try again")
	assert.Contains(t, se.msg, "try again")
}

func TestTerminals_NoListenerRefusesASlot(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	_, err := tm.reserve("phone")
	require.NoError(t, err, "a listener runs")

	tm.l.rule.Store(nil)
	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a request that passed guard before the listener closed is not counted")
}

func TestTerminals_TheCapsCountSlotsPerDeviceAndInAll(t *testing.T) {
	tm := newTestTerms(t)
	names := []string{"a", "b", "c", "d", "e"}
	var devices []core.WebDevice
	for _, n := range names {
		devices = append(devices, pairedDev(n, termPaired))
	}
	tm.apply(modelWith(true, devices...))
	held := map[string][]*termSlot{}
	fill := func(name string) {
		for i := 0; i < maxTerminalsPerDevice; i++ {
			s, err := tm.reserve(name)
			require.NoError(t, err, "%s's slot %d", name, i+1)
			held[name] = append(held[name], s)
		}
	}

	fill("a")
	_, err := tm.reserve("a")
	assert.ErrorIs(t, err, errTermsFull, "a fifth slot for one device")
	fill("b")
	fill("c")
	fill("d")
	require.Equal(t, maxTerminals, tm.slotCount(), "fixture: four devices of four slots are 16 in all")

	_, err = tm.reserve("e")
	assert.ErrorIs(t, err, errTermsFull, "a device with none is refused at 16 in all")
	tm.release(held["d"][0])
	_, err = tm.reserve("e")
	assert.NoError(t, err, "a released slot is room for the next")
}

func TestTerminals_TheSwitchTurningOffEndsEverySlot(t *testing.T) {
	tm := newTestTerms(t)
	devices := []core.WebDevice{pairedDev("a", termPaired), pairedDev("b", termPaired)}
	tm.apply(modelWith(true, devices...))
	sa, err := tm.reserve("a")
	require.NoError(t, err)
	sb, err := tm.reserve("b")
	require.NoError(t, err)

	tm.apply(modelWith(true, devices...))
	_, endedA := sa.endedWith()
	_, endedB := sb.endedWith()
	assert.False(t, endedA || endedB, "a model change that leaves the switch on ends nothing")

	tm.apply(modelWith(false, devices...))
	for name, s := range map[string]*termSlot{"a": sa, "b": sb} {
		e, ended := s.endedWith()
		assert.True(t, ended, name)
		assert.Equal(t, termEndOff, e, name)
	}
}

func TestTerminals_ARevokedOrRepairedDeviceEndsItsSlotsOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after []core.WebDevice
	}{
		{"revoked", []core.WebDevice{pairedDev("keep", termPaired)}},
		{"paired again", []core.WebDevice{pairedDev("keep", termPaired), pairedDev("gone", termPaired.Add(time.Hour))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := newTestTerms(t)
			tm.apply(modelWith(true, pairedDev("keep", termPaired), pairedDev("gone", termPaired)))
			gone, err := tm.reserve("gone")
			require.NoError(t, err)
			keep, err := tm.reserve("keep")
			require.NoError(t, err)

			tm.apply(modelWith(true, tc.after...))

			e, ended := gone.endedWith()
			assert.True(t, ended, "the device that is gone or paired again")
			assert.Equal(t, termEndRevoked, e)
			_, ended = keep.endedWith()
			assert.False(t, ended, "another device's slot stays")
		})
	}
}

func TestTerminals_ASlotEndedBeforeItsConnectionExistsEndsItWhenItAttaches(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(false, pairedDev("phone", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	end, _, ce := rig.finish(t)

	assert.Equal(t, termEndOff, end)
	assert.Equal(t, websocket.StatusCode(4001), ce.Code)
	assert.Empty(t, open.calls(), "a connection ended before it began opens no attach")
}

func TestTerminals_ASlotThatIsNotEndedLetsItsConnectionWork(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	tm.apply(modelWith(true, pairedDev("phone", termPaired), pairedDev("other", termPaired)))

	open := newTermOpenFake()
	rig := startTermConn(t, termRigOpts{open: open.open, before: func(c *termConn) { slot.attach(c) }})
	sendResize(t, rig.client, 100, 30)
	expectLive(t, rig.client)
	assert.Equal(t, [][2]int{{100, 30}}, open.calls())
}

func TestTerminals_CloseEndsEverythingRefusesNewSlotsAndWaitsForTheHandlers(t *testing.T) {
	tm := newTestTerms(t)
	tm.grace = 5 * time.Second
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)
	rig := startTermConn(t, termRigOpts{before: func(c *termConn) { slot.attach(c) }})

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	end, _, ce := rig.finish(t)
	assert.Equal(t, termEndStopping, end)
	assert.Equal(t, websocket.StatusGoingAway, ce.Code)
	require.Never(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 150*time.Millisecond, 10*time.Millisecond, "close returned while a handler still held its slot")

	tm.release(slot)
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close never returned after the last handler released")

	_, err = tm.reserve("phone")
	assert.ErrorIs(t, err, errTermsStopping, "a closed part counts nothing")
	tm.close() // a second close returns at once
}

func TestTerminals_CloseGivesUpOnAHandlerThatNeverReleases(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		tm.close()
		close(done)
	}()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "close waited past its grace")
	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
}

func TestTerminals_EndAllLeavesThePartOpenForTheNextListener(t *testing.T) {
	tm := newTestTerms(t)
	tm.apply(modelWith(true, pairedDev("phone", termPaired)))
	slot, err := tm.reserve("phone")
	require.NoError(t, err)

	tm.endAll(termEndStopping)

	e, ended := slot.endedWith()
	assert.True(t, ended)
	assert.Equal(t, termEndStopping, e)
	_, err = tm.reserve("phone")
	assert.NoError(t, err, "a rebind ends the old connections and the new listener takes new ones")
}

// failingPty is a pty factory that cannot make one, and keeps the commands
// it was asked to start.
type failingPty struct{ cmds []*exec.Cmd }

func (p *failingPty) Start(cmd *exec.Cmd) (*os.File, error) {
	p.cmds = append(p.cmds, cmd)
	return nil, errors.New("no pty here")
}

func (p *failingPty) Close() {}

// masterPty hands the attach the master of a real pty pair.
type masterPty struct{ f *os.File }

func (p masterPty) Start(*exec.Cmd) (*os.File, error) { return p.f, nil }
func (masterPty) Close()                              {}

func TestTerminalOpen_AFailedHistoryDoesNotStopTheAttach(t *testing.T) {
	tm := newTestTerms(t)
	buf := &logBuf{}
	tm.lg = plainLog(buf)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, errors.New("tmux is not answering") }}
	ptys := &failingPty{}
	tm.ptys = ptys

	sh, hist, err := tm.tmuxOpen("loom_term_live")(120, 40)

	require.Error(t, err)
	assert.True(t, sh == nil, "a nil attach inside the interface would be a shell that is not there")
	assert.Nil(t, hist)
	require.Len(t, ptys.cmds, 1, "the attach was tried although the history was not read")
	args := ptys.cmds[0].Args
	assert.Equal(t, []string{"attach-session", "-t", "=loom_term_live"}, args[len(args)-3:], "on the exact target")
	assert.Contains(t, buf.String(), "web.terminal_history_failed")
	assert.Contains(t, buf.String(), "web.terminal_attach_failed", "the cause is logged: the connection tells the app only that it could not attach")
}

func TestTerminalOpen_ReturnsTheHistoryAndTheAttach(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = slave.Close() })
	tm := newTestTerms(t)
	tm.exec = cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "display-message":
			return []byte("2\n"), nil
		case "capture-pane":
			return []byte("one\ntwo\n"), nil
		}
		return nil, errors.New("an unexpected tmux command")
	}}
	tm.ptys = masterPty{master}

	sh, hist, err := tm.tmuxOpen("loom_term_live")(100, 30)

	require.NoError(t, err)
	require.NotNil(t, sh)
	t.Cleanup(func() { _ = sh.Close() })
	assert.Equal(t, "one\r\ntwo\r\n\x1b[0m", string(hist), "the rows above the screen, each ended by CR LF and the history closed by an SGR reset, as ShellHistory gives them")
}

// shellScript is the model's answer to a request for a shell, scripted: the
// tmux name it names (""  names none), an error it refuses with, and a gate
// that holds the request until release. It records which sessions were asked
// for and which shell names were opened.
type shellScript struct {
	mu    sync.Mutex
	name  string
	err   error
	gate  chan struct{}
	once  sync.Once
	asked []core.InstanceID
	named []string
}

func (s *shellScript) askedIDs() []core.InstanceID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.InstanceID(nil), s.asked...)
}

func (s *shellScript) opened() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.named...)
}

func (s *shellScript) refuseWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// hold makes the next requests wait, in the layer's send, until release.
func (s *shellScript) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = make(chan struct{})
}

func (s *shellScript) release() {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		s.once.Do(func() { close(gate) })
	}
}

// scriptTerminals replaces the part's two seams: asking the model for a shell
// answers with a Reply as the drain would hand the layer's reqs, naming name,
// and opening the attach for that name goes through open.
func scriptTerminals(env *apiEnv, name string, open termOpen) *shellScript {
	s := &shellScript{name: name}
	terms := env.layer.terms
	terms.ensure = func(id core.InstanceID, req core.ReqID) {
		s.mu.Lock()
		s.asked = append(s.asked, id)
		gate, err, name := s.gate, s.err, s.name
		s.mu.Unlock()
		if gate != nil {
			<-gate
		}
		rep := core.Reply{Req: req, ID: id}
		if err != nil {
			rep.Err = err
		} else {
			rep.Terminal = name
		}
		env.layer.reqs.apply([]core.Event{rep})
	}
	terms.newOpen = func(n string) termOpen {
		s.mu.Lock()
		s.named = append(s.named, n)
		s.mu.Unlock()
		return open
	}
	return s
}

// newTermEnv is an apiEnv whose terminals' part has read the paired device.
// The switch is off.
func newTermEnv(t *testing.T, insts ...*session.Instance) *apiEnv {
	t.Helper()
	env := newAPIEnv(t, apiFixture{insts: insts})
	require.Eventually(t, func() bool { return env.layer.terms.knows("phone") }, 5*time.Second, 10*time.Millisecond, "the terminals' part never read the paired device")
	return env
}

// switchTerminal sets the web terminal's switch through the model, as the
// host's command does, and waits until the part has seen it.
func (e *apiEnv) switchTerminal(on bool) {
	e.t.Helper()
	require.NoError(e.t, e.connect(e.t).SetWebTerminal(on))
	require.Eventually(e.t, func() bool { return e.layer.terms.isOn() == on }, 5*time.Second, 10*time.Millisecond, "the terminals' part never saw the switch")
}

// termDialed is the outcome of a handshake: the connection when it upgraded,
// else the status and body the layer refused with.
type termDialed struct {
	conn   *websocket.Conn
	status int
	body   string
	err    error
}

// termDial makes the handshake of a session's terminal at addr as the app's
// page does: the device's cookie and the layer's origin. edit changes the
// headers first.
func termDial(addr string, cookie *http.Cookie, id core.InstanceID, edit func(h http.Header)) termDialed {
	h := http.Header{"Origin": {"http://" + addr}}
	if cookie != nil {
		h.Set("Cookie", (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
	}
	if edit != nil {
		edit(h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/v1/sessions/%d/terminal", addr, id), &websocket.DialOptions{HTTPHeader: h})
	d := termDialed{conn: c, err: err}
	if resp != nil {
		d.status = resp.StatusCode
		if d.status != http.StatusSwitchingProtocols && resp.Body != nil {
			b, _ := io.ReadAll(resp.Body)
			d.body = string(b)
			_ = resp.Body.Close()
		}
	}
	return d
}

func (e *apiEnv) term(id core.InstanceID, edit func(h http.Header)) termDialed {
	return termDial(e.addr, e.cookie, id, edit)
}

// mustTerm opens a session's terminal and fails unless it upgraded.
func mustTerm(t *testing.T, env *apiEnv, id core.InstanceID) *websocket.Conn {
	t.Helper()
	got := env.term(id, nil)
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "body: %q, err: %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
	return got.conn
}

// requireClosed reads until the connection ends and checks it ended with a
// close frame of code and reason.
func requireClosed(t *testing.T, c *websocket.Conn, code websocket.StatusCode, reason string) {
	t.Helper()
	_, ce, err := readUntilClose(c)
	require.NoError(t, err, "the connection ended without a close frame")
	require.NotNil(t, ce)
	assert.Equal(t, code, ce.Code)
	assert.Equal(t, reason, ce.Reason)
}

// plainTerm makes a request of a session's terminal that is not a handshake,
// as a plain client would: the device's cookie, the given origin, and the
// headers edit leaves. It returns the status and the body.
func (e *apiEnv) plainTerm(method string, id core.InstanceID, origin string, edit func(h http.Header)) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s/api/v1/sessions/%d/terminal", e.addr, id), nil)
	require.NoError(e.t, err)
	req.Header.Set("Origin", origin)
	req.AddCookie(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value})
	edit(req.Header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)
	return resp.StatusCode, string(b)
}

// rawTerm writes the handshake of a session's terminal by hand on a new TCP
// connection, with proto on the request line and origin as the Origin, and
// returns the connection and the response read from it. It is for what the
// HTTP client never sends: a request below HTTP/1.1, and a frame header that
// announces more than follows. The other headers are right: the device's
// cookie, version 13 and a key of 16 bytes.
func (e *apiEnv) rawTerm(proto string, id core.InstanceID, origin string) (net.Conn, *http.Response) {
	e.t.Helper()
	nc, err := net.Dial("tcp", e.addr)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = nc.Close() })
	require.NoError(e.t, nc.SetDeadline(time.Now().Add(30*time.Second)))
	cookie := (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String()
	_, err = fmt.Fprintf(nc, "GET /api/v1/sessions/%d/terminal %s\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n", id, proto, e.addr, origin, cookie)
	require.NoError(e.t, err)
	resp, err := http.ReadResponse(bufio.NewReader(nc), nil)
	require.NoError(e.t, err)
	return nc, resp
}

func TestTerminal_ARequestThatIsNotAnUpgradeIs426BeforeAnythingElse(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	const (
		notUpgrade   = "this route is a WebSocket upgrade"
		badHandshake = "this route needs WebSocket version 13 and one Sec-WebSocket-Key of 16 bytes"
		key          = "dGhlIHNhbXBsZSBub25jZQ=="
	)
	headers := func(kv ...string) func(h http.Header) {
		return func(h http.Header) {
			for i := 0; i < len(kv); i += 2 {
				h.Set(kv[i], kv[i+1])
			}
		}
	}
	// handshake sets a whole handshake's headers, then kv over them (a "" value
	// removes the header).
	handshake := func(kv ...string) func(h http.Header) {
		return func(h http.Header) {
			headers("Connection", "Upgrade", "Upgrade", "websocket", "Sec-WebSocket-Version", "13", "Sec-WebSocket-Key", key)(h)
			for i := 0; i < len(kv); i += 2 {
				if kv[i+1] == "" {
					h.Del(kv[i])
				} else {
					h.Set(kv[i], kv[i+1])
				}
			}
		}
	}
	plain := func(method string, edit func(h http.Header)) func(origin string) (int, string) {
		return func(origin string) (int, string) { return env.plainTerm(method, id, origin, edit) }
	}
	http10 := func(origin string) (int, string) {
		nc, resp := env.rawTerm("HTTP/1.0", id, origin)
		defer func() { _ = resp.Body.Close(); _ = nc.Close() }()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(b)
	}

	for _, tc := range []struct {
		name    string
		status  int
		refusal string // "" for a HEAD, which has no body
		do      func(origin string) (int, string)
	}{
		{"a plain GET", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers())},
		{"no Upgrade header", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Connection", "Upgrade"))},
		{"no Connection header", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Upgrade", "websocket"))},
		{"another protocol", http.StatusUpgradeRequired, notUpgrade, plain(http.MethodGet, headers("Connection", "Upgrade", "Upgrade", "h2c"))},
		{"a HEAD with the upgrade headers", http.StatusUpgradeRequired, "", plain(http.MethodHead, headers("Connection", "Upgrade", "Upgrade", "websocket"))},
		{"HTTP/1.0 with the whole handshake", http.StatusUpgradeRequired, notUpgrade, http10},
		{"no Sec-WebSocket-Version", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Version", ""))},
		{"version 8", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Version", "8"))},
		{"no Sec-WebSocket-Key", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", ""))},
		{"two keys", http.StatusBadRequest, badHandshake, plain(http.MethodGet, func(h http.Header) { handshake()(h); h.Add("Sec-WebSocket-Key", key) })},
		{"a key that is not base64", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", "not base64!"))},
		{"a key of 15 bytes", http.StatusBadRequest, badHandshake, plain(http.MethodGet, handshake("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAA"))},
	} {
		// First with the switch off and an origin that is not loom's, which
		// the origin and the switch would each refuse 403, then with both
		// right, which only the upgrade and handshake checks can refuse before
		// the slot and the model.
		for _, st := range []struct {
			on     bool
			origin string
		}{{false, "https://evil.example"}, {true, "http://" + env.addr}} {
			env.switchTerminal(st.on)
			status, body := tc.do(st.origin)
			require.Equal(t, tc.status, status, "%s, switch %v, origin %s", tc.name, st.on, st.origin)
			if tc.refusal != "" {
				assert.Equal(t, tc.refusal, apiErrorOf(t, []byte(body)), "%s, switch %v", tc.name, st.on)
			}
		}
	}
	assert.Empty(t, script.askedIDs(), "the model was never asked for a shell")
	assert.Zero(t, env.layer.terms.slotCount(), "no slot was taken")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", result: "426 " + notUpgrade})
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", result: "400 " + badHandshake})

	mustTerm(t, env, id) // a handshake is still upgraded
}

func TestTerminal_AnOriginThatIsNotLoomsIsRefusedFirst(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	const refusal = "the request does not come from loom's origin"

	for _, on := range []bool{true, false} {
		env.switchTerminal(on)
		for _, tc := range []struct {
			name string
			edit func(h http.Header)
		}{
			{"none", func(h http.Header) { h.Del("Origin") }},
			{"another site", func(h http.Header) { h.Set("Origin", "https://evil.example") }},
			{"two", func(h http.Header) { h.Add("Origin", "http://"+env.addr) }},
			{"the origin with a path", func(h http.Header) { h.Set("Origin", "http://"+env.addr+"/x") }},
		} {
			got := env.term(id, tc.edit)
			assert.Equal(t, http.StatusForbidden, got.status, "%s, switch %v", tc.name, on)
			assert.Equal(t, refusal, apiErrorOf(t, []byte(got.body)), "%s, switch %v: the origin is checked before the switch", tc.name, on)
		}
	}
	assert.Empty(t, script.askedIDs(), "the model was never asked for a shell")
	assert.Zero(t, env.layer.terms.slotCount())

	env.switchTerminal(true)
	mustTerm(t, env, id) // the layer's own origin is accepted
}

func TestTerminal_TheSwitchOffIsRefused403(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)

	got := env.term(id, nil)
	assert.Equal(t, http.StatusForbidden, got.status)
	assert.Equal(t, "the terminal is off on this host", apiErrorOf(t, []byte(got.body)))
	assert.Empty(t, script.askedIDs(), "the model was not asked")
	assert.Zero(t, env.layer.terms.slotCount())
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", session: id, result: "403 the terminal is off on this host"})

	env.switchTerminal(true)
	mustTerm(t, env, id)
}

func TestTerminal_AnUnknownSessionOrAWorkspaceTerminalIs404(t *testing.T) {
	term, err := session.NewInstance(session.InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	env := newTermEnv(t, term, apiInstance(t, "live", session.Running))
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)

	for name, id := range map[string]core.InstanceID{"a workspace terminal": env.ids["term"], "an unknown session": 999999} {
		got := env.term(id, nil)
		assert.Equal(t, http.StatusNotFound, got.status, name)
		assert.Equal(t, "no such session", apiErrorOf(t, []byte(got.body)), name)
	}
	assert.Empty(t, script.askedIDs(), "the model was not asked")
	assert.Zero(t, env.layer.terms.slotCount())
	mustTerm(t, env, env.ids["live"]) // the session the app lists is served
}

func TestTerminal_TheModelsRefusalIs409AndFreesTheSlot(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	script.refuseWith(errors.New("the session is paused"))

	for i := 0; i <= maxTerminalsPerDevice; i++ {
		got := env.term(id, nil)
		require.Equal(t, http.StatusConflict, got.status, "refusal %d: body %q", i+1, got.body)
		assert.Equal(t, "the session is paused", apiErrorOf(t, []byte(got.body)), "the model's own text")
		require.Zero(t, env.layer.terms.slotCount(), "a refusal releases its slot")
	}
	assert.Len(t, script.askedIDs(), maxTerminalsPerDevice+1, "every refusal came from the model, none from the cap")

	script.refuseWith(nil)
	mustTerm(t, env, id)
}

func TestTerminal_AnInstanceTheModelHasNoShellForIs409(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "fresh", session.Running))
	id := env.ids["fresh"]
	env.switchTerminal(true)

	got := env.term(id, nil)

	assert.Equal(t, http.StatusConflict, got.status, "the model refuses an instance that was never started: body %q", got.body)
	assert.NotEmpty(t, apiErrorOf(t, []byte(got.body)))
	assert.Zero(t, env.layer.terms.slotCount())
}

func TestTerminal_AReplyWithoutAShellIs500(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "", newTermOpenFake().open)
	env.switchTerminal(true)

	got := env.term(id, nil)

	assert.Equal(t, http.StatusInternalServerError, got.status)
	assert.Equal(t, "loom's reply named no shell", apiErrorOf(t, []byte(got.body)))
	assert.Zero(t, env.layer.terms.slotCount())
}

func TestTerminal_PastTheLimitIs429BeforeTheModelIsAsked(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	script := scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)

	var conns []*websocket.Conn
	for i := 0; i < maxTerminalsPerDevice; i++ {
		conns = append(conns, mustTerm(t, env, id))
	}
	got := env.term(id, nil)
	assert.Equal(t, http.StatusTooManyRequests, got.status)
	assert.Equal(t, "too many open terminals", apiErrorOf(t, []byte(got.body)))
	assert.Len(t, script.askedIDs(), maxTerminalsPerDevice, "the model was not asked for the refused one")

	require.NoError(t, conns[0].Close(websocket.StatusNormalClosure, ""))
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == maxTerminalsPerDevice-1 }, 5*time.Second, 10*time.Millisecond, "a connection that left never gave its slot back")
	mustTerm(t, env, id)
}

func TestTerminal_BytesGoBothWaysToTheShellTheModelNamed(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	script := scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 120, 40)
	expectLive(t, c)
	assert.Equal(t, []core.InstanceID{id}, script.askedIDs(), "the model was asked for this session's shell")
	assert.Equal(t, []string{"loom_term_live"}, script.opened(), "the attach is on the name the model gave")
	assert.Equal(t, [][2]int{{120, 40}}, open.calls(), "at the browser's size")

	sendTerm(t, c, websocket.MessageBinary, []byte("echo hi\r"))
	require.Eventually(t, func() bool { return open.shell.input() == "echo hi\r" }, 5*time.Second, 10*time.Millisecond, "the browser's bytes never reached the shell")
	open.shell.feed <- []byte("hi\r\n")
	typ, b := readTerm(t, c)
	assert.Equal(t, websocket.MessageBinary, typ)
	assert.Contains(t, string(b), "hi")
}

func TestTerminal_OpenAndCloseAreLoggedWithoutTheBytes(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	scriptTerminals(env, "loom_term_live", open.open)
	buf := &logBuf{}
	env.layer.terms.lg = plainLog(buf)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 80, 24)
	expectLive(t, c)
	sendTerm(t, c, websocket.MessageBinary, []byte("hunter2\r"))
	open.shell.feed <- []byte("a secret in the output\r\n")
	readTerm(t, c)
	require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == 0 }, 5*time.Second, 10*time.Millisecond)

	want := fmt.Sprintf("level=INFO msg=web.terminal_open device=phone session=%d\nlevel=INFO msg=web.terminal_close device=phone session=%d reason=\"the browser left\"\n", id, id)
	assert.Equal(t, want, buf.String(), "each open and close, with the device, the session and the reason, and nothing else")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "terminal", session: id, result: "101 ok"})
}

func TestTerminal_ASlotEndedWhileTheModelWorksClosesWhenItUpgrades(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	script := scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)
	script.hold()

	dialed := make(chan termDialed, 1)
	go func() { dialed <- env.term(id, nil) }()
	require.Eventually(t, func() bool {
		return env.layer.terms.slotCount() == 1 && len(script.askedIDs()) == 1
	}, 5*time.Second, 10*time.Millisecond, "the request never reached the model")

	env.switchTerminal(false)
	script.release()

	var got termDialed
	select {
	case got = <-dialed:
	case <-time.After(10 * time.Second):
		t.Fatal("the request never answered")
	}
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "body %q, err %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
	requireClosed(t, got.conn, 4001, "the terminal is off on this host")
	assert.Empty(t, open.calls(), "the ended connection opened no attach")
}

func TestTerminal_TheAttachEndingAsksTheReplica(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func(t *testing.T) *session.Instance
		active bool
		want   termEnd
	}{
		{"started and active", func(t *testing.T) *session.Instance { inst, _ := apiWithWorktree(t, "live"); return inst }, true, termEndExited},
		{"never started", func(t *testing.T) *session.Instance { return apiInstance(t, "live", session.Running) }, false, termEndSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTermEnv(t, tc.build(t))
			id := env.ids["live"]
			require.Equal(t, tc.active, env.view(id).Active(), "fixture")
			open := newTermOpenFake()
			scriptTerminals(env, "loom_term_live", open.open)
			env.switchTerminal(true)

			c := mustTerm(t, env, id)
			sendResize(t, c, 80, 24)
			expectLive(t, c)
			close(open.shell.feed) // the attach ends by itself, as a shell's exit or a killed session does

			requireClosed(t, c, tc.want.code, tc.want.reason)
		})
	}
}

func TestTerminal_APanicInTheAttachStopsTheLayer(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	open := newTermOpenFake()
	open.shell.panicOnRead = true
	scriptTerminals(env, "loom_term_live", open.open)
	env.switchTerminal(true)

	c := mustTerm(t, env, id)
	sendResize(t, c, 80, 24) // opens the attach, whose output goroutine panics
	select {
	case <-env.layer.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a panic in the connection's goroutine did not stop the layer: it was not recovered through the layer's recoverPanic")
	}
}

func TestTerminal_ABrowserThatStallsMidFrameCannotHoldItsSlot(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	nc, resp := env.rawTerm("HTTP/1.1", id, "http://"+env.addr)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Equal(t, 1, env.layer.terms.slotCount())

	// Part of a frame that announces 1000 bytes: the connection's reader is
	// inside it, where the library's close handshake would wait for the rest
	// with no deadline. The browser then stalls, and the switch turns off.
	_, err := nc.Write(append(rawFrameHeader(2, 1000), bytes.Repeat([]byte("a"), 10)...))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	env.switchTerminal(false)

	// The connection closes the hijacked socket outright termCloseWait after
	// its close frame, so the test takes about that long.
	require.Eventually(t, func() bool { return env.layer.terms.slotCount() == 0 }, termCloseWait+5*time.Second, 50*time.Millisecond,
		"the slot was never given back: the hijacked connection was not closed outright termCloseWait after the close frame")
}

func TestTerminal_TheSwitchTurningOffEndsAnOpenConnection(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	env.switchTerminal(false)

	requireClosed(t, c, 4001, "the terminal is off on this host")
}

func TestTerminal_ARevokedDeviceEndsItsConnection(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	require.NoError(t, env.connect(t).RevokeDevice("phone"))

	requireClosed(t, c, 4002, "this device is no longer paired")
}

func TestTerminal_ARebindEndsTheConnectionsAndTheNextListenerTakesNew(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	id := env.ids["live"]
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.switchTerminal(true)
	c := mustTerm(t, env, id)

	newAddr := freeLoopbackAddr(t)
	require.NoError(t, env.connect(t).ConfigureWeb(newAddr, "http://"+newAddr))

	requireClosed(t, c, 1001, "loom is stopping")
	env.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == newAddr })
	got := termDial(newAddr, env.cookie, id, nil)
	require.Equal(t, http.StatusSwitchingProtocols, got.status, "the part stays open for the next listener: body %q, err %v", got.body, got.err)
	t.Cleanup(func() { _ = got.conn.CloseNow() })
}

func TestTerminal_TheLayersStopEndsTheConnections(t *testing.T) {
	env := newTermEnv(t, apiInstance(t, "live", session.Running))
	scriptTerminals(env, "loom_term_live", newTermOpenFake().open)
	env.layer.terms.grace = 30 * time.Second // longer than the test: only the handlers' return can end close's wait
	env.switchTerminal(true)
	c := mustTerm(t, env, env.ids["live"])

	// The connection's close handshake finishes only while the browser reads,
	// so the browser holds off and the layer stops on a goroutine.
	stopped := make(chan struct{})
	go func() {
		env.layer.Close()
		close(stopped)
	}()

	require.Eventually(t, func() bool {
		slot, err := env.layer.terms.reserve("phone")
		if err == nil {
			env.layer.terms.release(slot)
		}
		return err == errTermsStopping
	}, 5*time.Second, 5*time.Millisecond, "the part never refused a slot while the layer stopped")
	assert.NotNil(t, env.layer.rule.Load(), "the listener's rule is still set: the refusal is the part's own")
	select {
	case <-stopped:
		t.Fatal("the layer's stop returned before the connection wrote its closing frame: close did not wait for the handlers")
	case <-time.After(300 * time.Millisecond):
	}
	assert.Equal(t, 1, env.layer.terms.slotCount(), "the handler still holds its slot")

	requireClosed(t, c, 1001, "loom is stopping") // the browser reads, and the handshake finishes
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the layer's stop never returned")
	}
	assert.Zero(t, env.layer.terms.slotCount(), "close waited for the handler to give its slot back")
}
