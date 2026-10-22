package web

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON_AnswersTheStatusAndTheBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"id": 7, "notices": []string{}})
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id": 7, "notices": []}`, rec.Body.String())
}

func TestWriteJSON_AValueThatWillNotEncodeIsA500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"c": make(chan int)})
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "never a 200 with half a body")
	assert.JSONEq(t, `{"error": "the answer could not be encoded"}`, rec.Body.String())
}

func TestWriteError_IsAnErrorObject(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusConflict, "the session is already paused")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error": "the session is already paused"}`, rec.Body.String())
}

func TestReadJSON(t *testing.T) {
	type body struct {
		Code string `json:"code"`
	}
	for _, tc := range []struct {
		name  string
		body  string
		limit int64  // a cap on the body, as guard sets; 0 for none
		want  string // part of the error, or "" for none
	}{
		{"one object", `{"code": "K7QM"}`, 0, ""},
		{"white space after it", "{\"code\": \"K7QM\"}\n  \n", 0, ""},
		{"a field the route does not take", `{"code": "K7QM", "extra": 1}`, 0, `unknown field "extra"`},
		{"nothing", ``, 0, "the body is empty"},
		{"only white space", " \n", 0, "the body is empty"},
		{"two objects", `{"code": "A"} {"code": "B"}`, 0, "more than one JSON value"},
		{"junk after the object", `{"code": "A"} x`, 0, "more than one JSON value"},
		{"not JSON", `not json`, 0, "the body is not the JSON this route takes"},
		{"truncated", `{"code": `, 0, "the body is not the JSON this route takes"},
		{"the wrong type", `{"code": 7}`, 0, "the body is not the JSON this route takes"},
		{"over the cap", `{"code": "K7QM-2XPA-and-more"}`, 16, "the body is over 16 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body))
			if tc.limit > 0 {
				req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, tc.limit)
			}
			var got body
			err := readJSON(req, &got)
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, "K7QM", got.Code)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// guardOrigin is the origin the guard tests enforce.
const guardOrigin = "https://loom.example.com"

// guardedBy is guard around a handler that answers 200 with the number of
// body bytes it read, or 413 with the reader's error. The layer enforces
// origin, as a bound listener's does.
func guardedBy(t *testing.T, origin string) http.Handler {
	t.Helper()
	rule, err := parseOrigin(origin)
	require.NoError(t, err)
	l := &Layer{}
	l.rule.Store(&rule)
	return l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"read": n})
	}))
}

// sendGuarded runs one request through h with host as its Host header and
// origins as its Origin headers (none when nil).
func sendGuarded(h http.Handler, method, host string, origins []string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/x", body)
	req.Host = host
	if origins != nil {
		req.Header["Origin"] = origins
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestParseOrigin(t *testing.T) {
	for origin, host := range map[string]string{
		"https://loom.example.com":      "loom.example.com",
		"https://LOOM.example.com:443":  "loom.example.com",
		"https://loom.example.com:8443": "loom.example.com:8443",
		"http://127.0.0.1:7420":         "127.0.0.1:7420",
		"http://localhost:80":           "localhost",
	} {
		rule, err := parseOrigin(origin)
		require.NoError(t, err, origin)
		assert.Equal(t, host, rule.host, origin)
	}
	for _, bad := range []string{
		"", "loom.example.com", "ftp://loom.example.com", "https://", "https://user@loom.example.com",
		"https://loom.example.com/path", "https://loom.example.com/", "https://loom.example.com?x=1",
		"https://loom.example.com?", "https://loom.example.com#f", "https://:443",
	} {
		_, err := parseOrigin(bad)
		assert.Error(t, err, bad)
	}
}

func TestGuard_TheHostMustBeTheOrigins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
		host   string
		status int
	}{
		{"the origin's host", guardOrigin, "loom.example.com", http.StatusOK},
		{"in any case", guardOrigin, "LOOM.Example.COM", http.StatusOK},
		{"with the scheme's default port", guardOrigin, "loom.example.com:443", http.StatusOK},
		{"another port", guardOrigin, "loom.example.com:8443", http.StatusMisdirectedRequest},
		{"another host", guardOrigin, "evil.example", http.StatusMisdirectedRequest},
		{"the host as a prefix of another", guardOrigin, "loom.example.com.evil.example", http.StatusMisdirectedRequest},
		{"the host as a suffix of another", guardOrigin, "evil-loom.example.com", http.StatusMisdirectedRequest},
		{"no host", guardOrigin, "", http.StatusMisdirectedRequest},
		{"an origin with a port", "https://loom.example.com:8443", "loom.example.com:8443", http.StatusOK},
		{"an origin with a port, asked for by the bare host", "https://loom.example.com:8443", "loom.example.com", http.StatusMisdirectedRequest},
		{"an origin naming its default port", "https://loom.example.com:443", "loom.example.com", http.StatusOK},
		{"a development origin", "http://127.0.0.1:7420", "127.0.0.1:7420", http.StatusOK},
		{"a development origin on another port", "http://127.0.0.1:7420", "127.0.0.1:7421", http.StatusMisdirectedRequest},
		{"localhost is not 127.0.0.1", "http://127.0.0.1:7420", "localhost:7420", http.StatusMisdirectedRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendGuarded(guardedBy(t, tc.origin), http.MethodGet, tc.host, nil, nil)
			assert.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestGuard_AnUnsafeMethodMustCarryTheOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		origins []string
		status  int
	}{
		{"a POST from the origin", http.MethodPost, []string{guardOrigin}, http.StatusOK},
		{"a POST without an Origin", http.MethodPost, nil, http.StatusForbidden},
		{"a POST from another site", http.MethodPost, []string{"https://evil.example"}, http.StatusForbidden},
		{"a POST from the http scheme", http.MethodPost, []string{"http://loom.example.com"}, http.StatusForbidden},
		{"a POST from another port", http.MethodPost, []string{"https://loom.example.com:8443"}, http.StatusForbidden},
		{"a POST from a subdomain", http.MethodPost, []string{"https://app.loom.example.com"}, http.StatusForbidden},
		{"an opaque origin", http.MethodPost, []string{"null"}, http.StatusForbidden},
		{"an Origin with a path", http.MethodPost, []string{guardOrigin + "/x"}, http.StatusForbidden},
		{"two Origin headers", http.MethodPost, []string{guardOrigin, guardOrigin}, http.StatusForbidden},
		{"the origin in another case", http.MethodPost, []string{"HTTPS://LOOM.EXAMPLE.COM"}, http.StatusOK},
		{"the origin naming its default port", http.MethodPost, []string{"https://loom.example.com:443"}, http.StatusOK},
		{"a PUT without an Origin", http.MethodPut, nil, http.StatusForbidden},
		{"a DELETE from another site", http.MethodDelete, []string{"https://evil.example"}, http.StatusForbidden},
		{"a GET from another site is not checked", http.MethodGet, []string{"https://evil.example"}, http.StatusOK},
		{"a GET without one", http.MethodGet, nil, http.StatusOK},
		{"a HEAD from another site is not checked", http.MethodHead, []string{"https://evil.example"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendGuarded(guardedBy(t, guardOrigin), tc.method, "loom.example.com", tc.origins, nil)
			assert.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestGuard_EveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	h := guardedBy(t, guardOrigin)
	for _, tc := range []struct {
		name   string
		rec    *httptest.ResponseRecorder
		status int
	}{
		{"served", sendGuarded(h, http.MethodGet, "loom.example.com", nil, nil), http.StatusOK},
		{"a wrong host", sendGuarded(h, http.MethodGet, "evil.example", nil, nil), http.StatusMisdirectedRequest},
		{"a wrong origin", sendGuarded(h, http.MethodPost, "loom.example.com", []string{"https://evil.example"}, nil), http.StatusForbidden},
	} {
		assert.Equal(t, tc.status, tc.rec.Code, tc.name)
		header := tc.rec.Header()
		assert.Equal(t, "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
			header.Get("Content-Security-Policy"), tc.name)
		assert.Equal(t, "nosniff", header.Get("X-Content-Type-Options"), tc.name)
		assert.Equal(t, "no-referrer", header.Get("Referrer-Policy"), tc.name)
	}
}

func TestGuard_NoRuleAnswersNothing(t *testing.T) {
	called := false
	l := &Layer{}
	h := l.guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := sendGuarded(h, http.MethodGet, "loom.example.com", nil, nil)
	assert.Equal(t, http.StatusMisdirectedRequest, rec.Code, "a layer with no listener refuses every request")
	assert.False(t, called)
	assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"), "the refusal carries the headers too")
}

func TestGuard_TheBodyIsCapped(t *testing.T) {
	h := guardedBy(t, guardOrigin)

	rec := sendGuarded(h, http.MethodPost, "loom.example.com", []string{guardOrigin}, strings.NewReader(strings.Repeat("a", maxBody)))
	assert.Equal(t, http.StatusOK, rec.Code, "a body of exactly the cap")
	assert.JSONEq(t, `{"read": 1048576}`, rec.Body.String())

	rec = sendGuarded(h, http.MethodPost, "loom.example.com", []string{guardOrigin}, strings.NewReader(strings.Repeat("a", maxBody+1)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "one byte more")
	assert.Contains(t, rec.Body.String(), "too large")
}

// The stream (Story 7) flushes each event through http.ResponseController,
// which reaches the real Flusher only if guard hands the handler the
// ResponseWriter it was given, or one with an Unwrap() http.ResponseWriter.
// The handler stays open after its flush, so the client has the first event
// only if the flush went through to the connection.
func TestGuard_AHandlerBehindItCanFlushToTheClient(t *testing.T) {
	unblock := make(chan struct{})
	flushed := make(chan error, 1)
	l := &Layer{}
	srv := httptest.NewServer(l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\n\n")
		flushed <- http.NewResponseController(w).Flush()
		<-unblock
	})))
	defer srv.Close()
	defer close(unblock) // runs first, so Close does not wait on the handler
	rule, err := parseOrigin(srv.URL)
	require.NoError(t, err)
	l.rule.Store(&rule)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err, "the response head reached the client before the handler returned")
	defer func() { _ = resp.Body.Close() }()

	require.NoError(t, <-flushed, "Flush through the ResponseController failed behind guard")
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: ping\n", line)
}

// deadMachine is a machine with no live tmux session and no claude: every
// command fails, every output is empty.
func deadMachine() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, &exec.ExitError{} },
	}
}

// webStack is the daemon's stack in one process, built as internal/daemon
// builds it: a booted model on its loop, served by the real rpc.Server.
// layer is the web layer under test, once start has run; tui is an ordinary
// client of the same server, which is how `loom web` reaches it.
type webStack struct {
	loop  *core.Loop
	srv   *rpc.Server
	layer *Layer
	tui   *rpc.Client
}

// newWebStack boots a model over a fresh global dir.
func newWebStack(t *testing.T) *webStack { return newWebStackIn(t, t.TempDir()) }

// newWebStackIn boots a model over dir, which a test may seed first.
func newWebStackIn(t *testing.T, dir string) *webStack {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, dir)
	model := core.New(core.Options{CmdExec: deadMachine()})
	model.Boot()
	s := &webStack{loop: core.Start(model)}
	s.srv = rpc.NewServer(s.loop)
	s.loop.Begin()
	t.Cleanup(func() {
		s.layer.Close()
		if s.tui != nil {
			s.tui.Close()
		}
		s.srv.Close()
		s.loop.Stop()
	})
	return s
}

// start starts the web layer on the stack's server, with the loop as host.
func (s *webStack) start(t *testing.T) {
	t.Helper()
	layer, err := Start(Options{Server: s.srv, Host: s.loop, Hostname: "testhost", Build: "test build"})
	require.NoError(t, err)
	s.layer = layer
}

// connect joins the stack's server as an ordinary client, once.
func (s *webStack) connect(t *testing.T) *rpc.Client {
	t.Helper()
	if s.tui != nil {
		return s.tui
	}
	a, b := net.Pipe()
	s.srv.Serve(a)
	c, err := rpc.Dial(b)
	require.NoError(t, err)
	s.tui = c
	return c
}

// enable has the model enable the web on a free loopback port, with the
// origin a development phone would use, and waits until the layer serves it.
func (s *webStack) enable(t *testing.T) (addr, origin string) {
	t.Helper()
	addr = freeLoopbackAddr(t)
	origin = "http://" + addr
	require.NoError(t, s.connect(t).ConfigureWeb(addr, origin))
	s.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == addr })
	return addr, origin
}

// waitWeb waits until the published WebView satisfies ok, and returns it.
func (s *webStack) waitWeb(t *testing.T, ok func(core.WebView) bool) core.WebView {
	t.Helper()
	var view core.WebView
	require.Eventually(t, func() bool {
		view = s.connect(t).Web()
		return ok(view)
	}, 10*time.Second, 10*time.Millisecond, "the published web settings never became what the test waits for")
	return view
}

// waitNotice waits until the ordinary client has been sent a notice whose
// text holds want. It reads the client's events, so each notice is seen once.
func (s *webStack) waitNotice(t *testing.T, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, ev := range s.connect(t).Sync() {
			n, ok := ev.(core.Notice)
			if !ok {
				continue
			}
			text := n.Info
			if n.Err != nil {
				text = n.Err.Error()
			}
			if strings.Contains(text, want) {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond, "no notice said %q", want)
}

// pair pairs a device called name through the pairing route, as the phone
// does, and returns the cookie it was given. The web must be serving at addr.
func (s *webStack) pair(t *testing.T, addr, name string) *http.Cookie {
	t.Helper()
	pairing, err := s.connect(t).PairDevice(name)
	require.NoError(t, err)
	resp := httpDo(t, addr, http.MethodPost, "/api/v1/pair", `{"code": "`+pairing.Code+`"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, bodyText(t, resp))
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	return cookies[0]
}

// whoAmI runs a request carrying cookie through authed and returns the
// status and the device the handler saw ("" when it did not run).
func (s *webStack) whoAmI(cookie *http.Cookie) (int, string) {
	var device string
	h := s.layer.authed(func(w http.ResponseWriter, r *http.Request, name string) {
		device = name
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/who", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, device
}

// funcSink is a sink that calls a function.
type funcSink func(evs []core.Event)

func (f funcSink) apply(evs []core.Event) { f(evs) }

// gateDrain returns a sink that stops the layer's drain, before it applies
// the batch, at the first published model change whose web view satisfies
// stop; held is closed once the drain stands there, and release lets it go
// (any number of times). Model changes pile up behind the stopped drain, and
// the client the layer drains coalesces them into one.
func gateDrain(stop func(core.WebView) bool) (sink funcSink, held <-chan struct{}, release func()) {
	heldCh, gate := make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	sink = func(evs []core.Event) {
		for _, ev := range evs {
			if m, ok := ev.(core.ModelChanged); ok && stop(m.View.Web) {
				holdOnce.Do(func() {
					close(heldCh)
					<-gate
				})
			}
		}
	}
	return sink, heldCh, func() { releaseOnce.Do(func() { close(gate) }) }
}

// boundServer is the http.Server the layer listens with now, nil while none
// does. Every bind makes a new one, which is how a test sees the layer bind
// again.
func boundServer(l *Layer) *http.Server {
	l.bindMu.Lock()
	defer l.bindMu.Unlock()
	return l.srv
}

// onOriginChange is a sink that calls f for each published model change that
// carries origin, which a test makes happen by configuring the web with that
// origin: a stray batch before it does not set f off.
func onOriginChange(origin string, f func()) funcSink {
	return func(evs []core.Event) {
		for _, ev := range evs {
			if m, ok := ev.(core.ModelChanged); ok && m.View.Web.Origin == origin {
				f()
			}
		}
	}
}

// freeLoopbackAddr is a loopback address nothing listens on yet.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// requireNoListener waits until nothing accepts connections at addr.
func requireNoListener(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return true
		}
		_ = c.Close()
		return false
	}, 10*time.Second, 10*time.Millisecond, "%s still accepts connections", addr)
}

// portOf is the port of a host:port address.
func portOf(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	return port
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
}

// httpDo sends method path to the layer at addr as the phone does: the Host
// is addr and a request that changes things carries the origin the stack's
// enable uses. mutate edits the request first.
func httpDo(t *testing.T, addr, method, path, body string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	require.NoError(t, err)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://"+addr)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := httpClient().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// httpProbe is the status of a GET of path at addr with host as the Host
// header (addr's own when empty), or 0 when nothing answers.
func httpProbe(addr, path, host string) int {
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return 0
	}
	if host != "" {
		req.Host = host
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func bodyText(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}

func TestStart_NeedsAServerAndAHost(t *testing.T) {
	s := newWebStack(t)
	_, err := Start(Options{Host: s.loop})
	assert.Error(t, err, "no server")
	_, err = Start(Options{Server: s.srv})
	assert.Error(t, err, "no host")
}

func TestStart_RefusesAStoppingServer(t *testing.T) {
	s := newWebStack(t)
	s.srv.Bye()
	layer, err := Start(Options{Server: s.srv, Host: s.loop})
	require.Error(t, err)
	assert.Nil(t, layer)
	assert.ErrorIs(t, err, core.ErrUnavailable, "a server that is stopping cannot be joined")
}

// The layer's own connection is the web role, which the server does not
// count as a client: a notice raised while no TUI is connected waits for the
// next one, rather than going to a layer that has no use for it.
func TestStart_ANoticeRaisedWithOnlyTheLayerConnectedWaitsForTheNextTUI(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	s.loop.WebServing("", errors.New("the port is taken"))

	s.connect(t) // the next TUI joins after the notice was raised
	s.waitNotice(t, "the port is taken")
}

func TestApplyWeb_ListensOnceTheModelEnablesTheWeb(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	tui := s.connect(t)
	assert.False(t, tui.Web().Serving, "the web is off until web.json enables it")
	assert.Empty(t, tui.Web().Error)

	addr, _ := s.enable(t)
	assert.Equal(t, addr, tui.Web().Addr)
	assert.Empty(t, tui.Web().Error)
	assert.Equal(t, http.StatusNotFound, httpDo(t, addr, http.MethodGet, "/nowhere", "").StatusCode, "the mux answers behind the guard")
}

func TestListener_GuardsEveryRequestAtTheDoor(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)
	const wrongCode = `{"code": "AAAA-AAAA"}`

	resp := httpDo(t, addr, http.MethodPost, "/api/v1/pair", wrongCode)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "the right Host and Origin reach the pairing route, which refuses a code nobody made")
	assert.Contains(t, bodyText(t, resp), core.ErrPairingCode.Error(), "with the model's refusal, which a truncated body (below) does not have")
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	resp = httpDo(t, addr, http.MethodPost, "/api/v1/pair", wrongCode, func(r *http.Request) { r.Host = "evil.example" })
	assert.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode, "a wrong Host")
	assert.Equal(t, contentSecurityPolicy, resp.Header.Get("Content-Security-Policy"), "the refusal carries the headers")

	resp = httpDo(t, addr, http.MethodPost, "/api/v1/pair", wrongCode, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a cross-site post")

	resp = httpDo(t, addr, http.MethodPost, "/api/v1/pair", `{"code":`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "a truncated body")
}

func TestApplyWeb_ChangingTheListenAddressMovesTheListener(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	first, _ := s.enable(t)

	second := freeLoopbackAddr(t)
	require.NoError(t, s.tui.ConfigureWeb(second, "http://"+second))
	s.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == second })

	requireNoListener(t, first)
	assert.Equal(t, http.StatusNotFound, httpDo(t, second, http.MethodGet, "/nowhere", "").StatusCode)
}

func TestApplyWeb_ChangingOnlyTheOriginChangesTheGuard(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)
	port := portOf(t, addr)

	require.Equal(t, http.StatusNotFound, httpProbe(addr, "/nowhere", ""), "the first origin's host is served")
	require.Equal(t, http.StatusMisdirectedRequest, httpProbe(addr, "/nowhere", "localhost:"+port), "and no other")

	require.NoError(t, s.tui.ConfigureWeb(addr, "http://localhost:"+port))
	require.Eventually(t, func() bool {
		return httpProbe(addr, "/nowhere", "localhost:"+port) == http.StatusNotFound
	}, 10*time.Second, 10*time.Millisecond, "the new origin's host is never served")
	assert.Equal(t, http.StatusMisdirectedRequest, httpProbe(addr, "/nowhere", ""), "the old origin's host no longer is")
}

func TestApplyWeb_DisablingStopsServing(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, origin := s.enable(t)

	require.NoError(t, s.tui.ConfigureWeb("", origin))
	s.waitWeb(t, func(v core.WebView) bool { return !v.Serving && v.Addr == "" })

	requireNoListener(t, addr)
	assert.Empty(t, s.tui.Web().Error)
}

func TestApplyWeb_ATakenPortLeavesTheWebOffAndSaysWhy(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	tui := s.connect(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })
	addr := taken.Addr().String()
	origin := "http://" + addr

	require.NoError(t, tui.ConfigureWeb(addr, origin), "the settings are valid; the port is the problem")
	view := s.waitWeb(t, func(v core.WebView) bool { return v.Error != "" })
	assert.False(t, view.Serving)
	assert.Empty(t, view.Addr)
	assert.Contains(t, view.Error, addr)
	s.waitNotice(t, addr)

	// Turning the web off clears the error the layer set, and turning it on
	// once the port is free listens. (The next tests retry with the very values
	// that failed, and show that nothing but a ConfigureWeb retries.)
	require.NoError(t, tui.ConfigureWeb("", origin))
	s.waitWeb(t, func(v core.WebView) bool { return v.Error == "" })
	require.NoError(t, taken.Close())
	require.NoError(t, tui.ConfigureWeb(addr, origin))
	s.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == addr })
}

// The layer acts on the revision every ConfigureWeb bumps, not on whether
// listen and origin differ: the very settings that failed bind again once the
// port is free, and nothing but a ConfigureWeb makes that happen.
func TestApplyWeb_TheSameSettingsAgainRetryAFailedBind(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	tui := s.connect(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })
	addr := taken.Addr().String()
	origin := "http://" + addr

	require.NoError(t, tui.ConfigureWeb(addr, origin))
	s.waitWeb(t, func(v core.WebView) bool { return v.Error != "" })
	require.NoError(t, taken.Close())
	require.Never(t, func() bool { return tui.Web().Serving }, 300*time.Millisecond, 10*time.Millisecond,
		"the port is free, but nothing but a ConfigureWeb retries a bind")

	require.NoError(t, tui.ConfigureWeb(addr, origin), "the very settings that failed")
	view := s.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == addr })
	assert.Empty(t, view.Error)
	assert.Equal(t, http.StatusNotFound, httpDo(t, addr, http.MethodGet, "/nowhere", "").StatusCode)
}

// A disable and an enable made while the drain is busy reach it as one
// ModelChanged, which carries the settings the listener already has. Only the
// revision tells it to close the listener and bind again.
func TestApplyWeb_TwoConfigurationsCoalescedIntoOneSyncStillRebind(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	tui := s.connect(t)
	addr, origin := s.enable(t)
	bound := boundServer(s.layer)
	require.NotNil(t, bound)
	rev := tui.Web().Rev

	// Stop the drain on the next change that leaves the settings alone (a
	// device pairing), so the two configurations below pile up behind it.
	sink, held, release := gateDrain(func(v core.WebView) bool { return v.Rev == rev && len(v.Devices) == 1 })
	t.Cleanup(release)
	s.layer.addSink(sink)
	s.pair(t, addr, "iPhone")
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the drain never reached the change it was to stop at")
	}

	require.NoError(t, tui.ConfigureWeb("", origin))
	require.NoError(t, tui.ConfigureWeb(addr, origin))
	final := tui.Web()
	require.NotEqual(t, rev, final.Rev, "each ConfigureWeb bumps the revision")
	require.Equal(t, addr, final.Listen, "the pair ends where it began")
	require.Equal(t, origin, final.Origin)
	require.Eventually(t, func() bool { return s.layer.client.Web().Rev == final.Rev }, 10*time.Second, 10*time.Millisecond,
		"the layer's client never received both configurations")
	require.Same(t, bound, boundServer(s.layer), "the stopped drain has not touched the listener")

	release()
	require.Eventually(t, func() bool {
		now := boundServer(s.layer)
		return now != nil && now != bound
	}, 10*time.Second, 10*time.Millisecond, "the layer never bound again")
	s.waitWeb(t, func(v core.WebView) bool { return v.Serving && v.Addr == addr })
	assert.Equal(t, http.StatusNotFound, httpDo(t, addr, http.MethodGet, "/nowhere", "").StatusCode)
}

// The model publishes again for a report to WebServing, the layer's own or
// anyone's. A report does not bump the revision, so it never makes the layer
// bind, however free the port is.
func TestApplyWeb_AWebServingReportNeverBinds(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	tui := s.connect(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })
	addr := taken.Addr().String()

	require.NoError(t, tui.ConfigureWeb(addr, "http://"+addr))
	s.waitWeb(t, func(v core.WebView) bool { return v.Error != "" })
	require.NoError(t, taken.Close())

	s.loop.WebServing("", errors.New("a report from elsewhere"))
	s.waitWeb(t, func(v core.WebView) bool { return strings.Contains(v.Error, "a report from elsewhere") })
	require.Never(t, func() bool { return boundServer(s.layer) != nil }, 300*time.Millisecond, 10*time.Millisecond,
		"listen is set and the port is free, but only a ConfigureWeb binds")
	assert.False(t, tui.Web().Serving)
}

func TestApplyWeb_AWebJSONTheModelLeftAloneKeepsItsReason(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, core.WebFile), []byte("{not json"), 0o600))
	s := newWebStackIn(t, dir)
	tui := s.connect(t)
	before := tui.Web().Error
	require.NotEmpty(t, before, "the model says why it left web.json alone")

	s.start(t)
	require.Never(t, func() bool { return tui.Web().Error != before }, 300*time.Millisecond, 10*time.Millisecond,
		"the layer must not clear or replace a reason it did not set")
	assert.False(t, tui.Web().Serving)
}

// The layer reports "off" only over a failure or a binding of its own. The
// model's own reason for a web it left off (web.json it would not read) is not
// the layer's to clear, and a layer that reported off for every view with no
// listen would wipe it.
func TestApplyWeb_OnlyWhatTheLayerDidIsReportedOff(t *testing.T) {
	h := &scriptedHost{}
	l := &Layer{host: h}

	l.applyWeb(core.WebView{Rev: 0, Listen: ""})
	assert.Empty(t, h.serving, "a first view that leaves the web off tells the host nothing")

	// A bind that fails is reported, and the layer owns that report.
	l.applyWeb(core.WebView{Rev: 1, Listen: "0.0.0.0:7420", Origin: "http://127.0.0.1:7420"})
	require.Len(t, h.serving, 1)
	assert.Empty(t, h.serving[0].addr)
	assert.Error(t, h.serving[0].err)

	// Disabling over it clears it, once.
	l.applyWeb(core.WebView{Rev: 2, Listen: ""})
	require.Len(t, h.serving, 2)
	assert.Equal(t, servingCall{}, h.serving[1], "the layer clears the error it set")

	// And a further disable has nothing of the layer's to clear.
	l.applyWeb(core.WebView{Rev: 3, Listen: ""})
	assert.Len(t, h.serving, 2)
}

func TestBind_ListensOnLoopbackOnly(t *testing.T) {
	for _, listen := range []string{
		"0.0.0.0:7420", "[::]:7420", "192.168.1.5:7420", ":7420", "localhost:7420", "example.com:7420", "127.0.0.1", "7420", "127.0.0.1:0",
	} {
		t.Run(listen, func(t *testing.T) {
			l := &Layer{}
			addr, err := l.bind(webKey{listen: listen, origin: "http://127.0.0.1:7420"})
			require.Error(t, err)
			assert.Empty(t, addr)
			assert.Nil(t, l.srv, "nothing listens")
			assert.Nil(t, l.rule.Load())
		})
	}

	listen := freeLoopbackAddr(t)
	l := &Layer{}
	addr, err := l.bind(webKey{listen: listen, origin: "http://127.0.0.1:7420"})
	require.NoError(t, err)
	t.Cleanup(l.shutListener)
	assert.True(t, strings.HasPrefix(addr, "127.0.0.1:"), addr)
	assert.NotNil(t, l.rule.Load())

	// An origin it cannot use leaves nothing listening, not the old listener.
	_, err = l.bind(webKey{listen: listen, origin: "loom.example.com"})
	require.Error(t, err)
	assert.Nil(t, l.srv)
	assert.Nil(t, l.rule.Load())
	requireNoListener(t, addr)

	// Once the layer is shut it binds nothing.
	l.shutListener()
	_, err = l.bind(webKey{listen: listen, origin: "http://127.0.0.1:7420"})
	assert.ErrorIs(t, err, errLayerClosed)
}

func TestDrain_HandsEveryBatchToEverySinkInTurn(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	var mu sync.Mutex
	var order []string
	var secondSawTheModelChange bool
	record := func(name string) funcSink {
		return func(evs []core.Event) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			for _, ev := range evs {
				if _, ok := ev.(core.ModelChanged); ok && name == "second" {
					secondSawTheModelChange = true
				}
			}
		}
	}
	s.layer.addSink(record("first"))
	s.layer.addSink(record("second"))

	require.NoError(t, s.tui.ConfigureWeb(addr, "http://localhost:"+portOf(t, addr)))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return secondSawTheModelChange
	}, 10*time.Second, 10*time.Millisecond, "the second sink never saw the model change")

	mu.Lock()
	defer mu.Unlock()
	for i, name := range order {
		assert.Equal(t, []string{"first", "second"}[i%2], name, "every batch goes to the first sink, then the second")
	}
}

func TestDrain_APanickingSinkStopsTheLayerNotTheDaemon(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	origin := "http://localhost:" + portOf(t, addr)
	s.layer.addSink(onOriginChange(origin, func() { panic("boom") }))
	require.NoError(t, s.tui.ConfigureWeb(addr, origin), "a changed origin makes a batch")

	// The layer stops, and the host is told why.
	view := s.waitWeb(t, func(v core.WebView) bool { return !v.Serving && strings.Contains(v.Error, "boom") })
	assert.Contains(t, view.Error, "drain panicked")
	s.waitNotice(t, "boom")
	requireNoListener(t, addr)

	// The daemon side carries on: the model still answers an ordinary client's
	// request and publishes what it changed.
	assert.NotEmpty(t, s.tui.Workspaces(), "the daemon goes on")
	require.NoError(t, s.tui.ConfigureWeb("", origin))
	s.waitWeb(t, func(v core.WebView) bool { return v.Listen == "" })
	assert.NotPanics(t, s.layer.Close, "a layer that failed still closes")
}

// recoverPanic is what each goroutine the layer starts defers first (Stories
// 7 and 12 defer it in their own): it must swallow the panic, stop the layer
// and say where it happened.
func TestRecoverPanic_StopsTheLayerAndNamesWhere(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	returned := make(chan struct{})
	go func() {
		defer close(returned) // runs after the recovery
		defer s.layer.recoverPanic("the test part")
		panic("kaboom")
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine never returned")
	}

	view := s.waitWeb(t, func(v core.WebView) bool { return !v.Serving && strings.Contains(v.Error, "kaboom") })
	assert.Contains(t, view.Error, "the test part panicked", "the error names the goroutine")
	s.waitNotice(t, "kaboom")
	requireNoListener(t, addr)
	assert.NotEmpty(t, s.tui.Workspaces(), "the daemon goes on")
}

// A part calls fail from the goroutine that panicked, which may be the drain
// that Close waits for: fail must not wait for the stop it starts.
func TestFail_ReturnsAtOnceFromTheDrainItself(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	origin := "http://localhost:" + portOf(t, addr)
	took := make(chan time.Duration, 1)
	var once sync.Once
	s.layer.addSink(onOriginChange(origin, func() {
		once.Do(func() {
			began := time.Now()
			s.layer.fail(errors.New("a part gave up"))
			took <- time.Since(began)
		})
	}))
	require.NoError(t, s.tui.ConfigureWeb(addr, origin), "a changed origin makes a batch")

	select {
	case d := <-took:
		assert.Less(t, d, drainGrace/2, "fail waited for the layer to stop, on the goroutine the stop waits for")
	case <-time.After(2 * drainGrace):
		t.Fatal("fail never returned")
	}
	view := s.waitWeb(t, func(v core.WebView) bool { return !v.Serving && strings.Contains(v.Error, "a part gave up") })
	assert.False(t, view.Serving)
	requireNoListener(t, addr)
}

// fail and Close run one stop between them. Whichever comes first stops the
// layer, the other waits for it, and neither hangs nor closes anything twice
// (a second close of the drain's channel panics).
func TestFail_RacingAClose_StopsOnceAndHangsNowhere(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); s.layer.fail(errors.New("a part failed")) }()
			go func() { defer wg.Done(); s.layer.Close() }()
		}
		wg.Wait()
	}()
	select {
	case <-finished:
	case <-time.After(2 * drainGrace):
		t.Fatal("a fail racing a Close hung")
	}

	// A Close returns only once the stop it shares is complete, so nothing
	// listens now, without waiting.
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = c.Close()
	}
	assert.Error(t, err, "the listener was open after Close returned")
	assert.NotPanics(t, s.layer.Close)
}

func TestClose_ClosesTheListenerAndMayBeRepeated(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	s.layer.Close()
	requireNoListener(t, addr)
	assert.NotPanics(t, s.layer.Close, "a second close is harmless")
	assert.NotPanics(t, (*Layer)(nil).Close, "so is one on a layer that never started")
}
