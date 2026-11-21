//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The web layer end to end (the loom PWA): a sandbox daemon with the web
// enabled pairs a device, streams the fleet to it, and creates, pauses,
// resumes and kills a fake agent's session over HTTP, as the phone's app
// does.

// pairingCode matches the code `loom web pair` prints, XXXX-XXXX.
var pairingCode = regexp.MustCompile(`[2-9A-HJKMNP-Z]{4}-[2-9A-HJKMNP-Z]{4}`)

// webCookie is the device cookie's name (cookieName in internal/web).
const webCookie = "__Host-loom"

// sseEvent is one server-sent event of the stream.
type sseEvent struct {
	Name string
	Data string
}

// readStream parses a server-sent event stream into events until it ends or
// ctx is done.
func readStream(ctx context.Context, body io.Reader) <-chan sseEvent {
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Name != "" {
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "event:"):
				ev.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.Data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}()
	return out
}

// awaitEvent reads the stream until an event named name that match accepts
// (nil: any) arrives, and returns it. The stream ending, or uiTimeout, fails
// the test.
func awaitEvent(t *testing.T, events <-chan sseEvent, name string, match func(sseEvent) bool) sseEvent {
	t.Helper()
	timeout := time.After(uiTimeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("the stream ended while waiting for a %q event", name)
			}
			if ev.Name == name && (match == nil || match(ev)) {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %q event arrived within %s", name, uiTimeout)
		}
	}
}

// awaitEnd reads the stream until it ends.
func awaitEnd(t *testing.T, events <-chan sseEvent) {
	t.Helper()
	timeout := time.After(uiTimeout)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatalf("the stream did not end within %s", uiTimeout)
		}
	}
}

// sessionsListing matches a `sessions` event of workspace ws, whose list
// holds a session titled title when want is true (and ok accepts its label,
// when ok is not nil), or none when want is false.
func sessionsListing(ws uint64, title string, want bool, ok func(label string) bool) func(sseEvent) bool {
	return func(ev sseEvent) bool {
		var got struct {
			Workspace uint64 `json:"workspace"`
			Sessions  []struct {
				Title string `json:"title"`
				Label string `json:"label"`
			} `json:"sessions"`
		}
		if json.Unmarshal([]byte(ev.Data), &got) != nil || got.Workspace != ws {
			return false
		}
		for _, s := range got.Sessions {
			if s.Title == title {
				return want && (ok == nil || ok(s.Label))
			}
		}
		return !want
	}
}

// webClient talks to a sandbox daemon's web layer as a phone does.
type webClient struct {
	t      *testing.T
	origin string
	token  string
	client *http.Client
}

// request builds a request to the layer; a non-GET carries the Origin the
// layer demands.
func (c *webClient) request(method, path string, body any) *http.Request {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(c.t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.origin+path, rd)
	require.NoError(c.t, err)
	if method != http.MethodGet {
		req.Header.Set("Origin", c.origin)
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// round sends req and returns the status, the body and the headers.
func (c *webClient) round(req *http.Request) (int, []byte, http.Header) {
	c.t.Helper()
	resp, err := c.client.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	return resp.StatusCode, out, resp.Header
}

// call makes the request as the paired device.
func (c *webClient) call(method, path string, body any) (int, []byte) {
	c.t.Helper()
	req := c.request(method, path, body)
	req.AddCookie(&http.Cookie{Name: webCookie, Value: c.token})
	status, out, _ := c.round(req)
	return status, out
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

// webCommand runs `loom web args…` in the sandbox, failing the test on an
// error, and returns what it printed.
func webCommand(t *testing.T, sb *devsandbox.Sandbox) func(args ...string) string {
	return func(args ...string) string {
		t.Helper()
		out, err := runLoom(t, sb, append([]string{sb.LoomBin(), "web"}, args...)...)
		require.NoError(t, err, out)
		return out
	}
}

// awaitListening waits until something accepts connections at addr.
func awaitListening(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, uiTimeout, 100*time.Millisecond, "the daemon never listened on %s", addr)
}

// pairWith posts code to /api/v1/pair and keeps the device cookie it sets as
// the client's token.
func (c *webClient) pairWith(code string) {
	c.t.Helper()
	status, body, header := c.round(c.request(http.MethodPost, "/api/v1/pair", map[string]string{"code": code}))
	require.Equal(c.t, http.StatusOK, status, string(body))
	for _, ck := range (&http.Response{Header: header}).Cookies() {
		if ck.Name == webCookie {
			c.token = ck.Value
		}
	}
	require.NotEmpty(c.t, c.token, "pairing sets the device cookie")
}

// stream opens the paired device's event stream and returns its events; the
// stream closes with the test. It has no timeout of its own: it stays open.
func (c *webClient) stream() <-chan sseEvent {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c.t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/api/v1/stream", nil)
	require.NoError(c.t, err)
	req.AddCookie(&http.Cookie{Name: webCookie, Value: c.token})
	resp, err := (&http.Client{Transport: c.client.Transport}).Do(req)
	require.NoError(c.t, err)
	c.t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(c.t, http.StatusOK, resp.StatusCode)
	return readStream(ctx, resp.Body)
}

// `loom web enable` starts the layer in the sandbox's daemon, `loom web
// pair` makes a code the phone posts for its cookie, and the cookie opens
// the stream and the routes. A session is created, paused, resumed and
// killed over HTTP; the agent's tmux session and the stream's lists follow
// each step. `loom web revoke` ends the stream and the cookie.
func TestE2E_Web_SessionLifecycleOverHTTP(t *testing.T) {
	sb := newSandbox(t, "")
	addr := freeLoopbackAddr(t)
	web := &webClient{t: t, origin: "http://" + addr, client: &http.Client{Timeout: 45 * time.Second}}
	loomWeb := webCommand(t, sb)

	loomWeb("enable", "--listen", addr, "--origin", web.origin)
	awaitListening(t, addr)

	// The guard, before any device is paired.
	status, _, _ := web.round(web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil))
	assert.Equal(t, http.StatusUnauthorized, status, "an API route needs the cookie")
	wrongHost := web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil)
	wrongHost.Host = "evil.example.com"
	status, _, _ = web.round(wrongHost)
	assert.Equal(t, http.StatusMisdirectedRequest, status, "a Host that is not the origin's is refused")

	// Pairing: the code posted alone gives the cookie.
	code := pairingCode.FindString(loomWeb("pair", "--name", "e2e"))
	require.NotEmpty(t, code, "loom web pair printed no code")
	web.pairWith(code)
	crossOrigin := web.request(http.MethodPost, "/api/v1/sessions/1/kill", nil)
	crossOrigin.Header.Set("Origin", "https://evil.example.com")
	crossOrigin.AddCookie(&http.Cookie{Name: webCookie, Value: web.token})
	status, _, _ = web.round(crossOrigin)
	assert.Equal(t, http.StatusForbidden, status, "an Origin that is not the origin is refused, cookie or not")

	// The stream: a snapshot names the sandbox's workspace.
	events := web.stream()
	snapshot := awaitEvent(t, events, "snapshot", nil)
	var fleet struct {
		Workspaces []struct {
			ID   uint64 `json:"id"`
			Name string `json:"name"`
		} `json:"workspaces"`
	}
	require.NoError(t, json.Unmarshal([]byte(snapshot.Data), &fleet))
	var ws uint64
	for _, w := range fleet.Workspaces {
		if w.Name == devsandbox.WorkspaceName {
			ws = w.ID
		}
	}
	require.NotZero(t, ws, "the snapshot lists the workspace %s: %s", devsandbox.WorkspaceName, snapshot.Data)

	// Create: answered at once with the new ID; the start follows on the
	// stream and in tmux.
	status, body := web.call(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": ws, "title": "webkeeper"})
	require.Equal(t, http.StatusCreated, status, string(body))
	var created struct {
		ID      uint64 `json:"id"`
		Notices []any  `json:"notices"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotZero(t, created.ID)
	awaitEvent(t, events, "sessions", sessionsListing(ws, "webkeeper", true, nil))
	require.Eventually(t, func() bool { return hasSession(sb, "webkeeper") }, uiTimeout, 100*time.Millisecond, "the agent's tmux session never started")
	require.Eventually(t, func() bool {
		return strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("webkeeper")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the fake agent came up")

	// Pause, resume, kill: each answered when the model has done it.
	path := fmt.Sprintf("/api/v1/sessions/%d/", created.ID)
	status, body = web.call(http.MethodPost, path+"pause", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	assert.False(t, hasSession(sb, "webkeeper"), "pausing ended the agent's tmux session")
	awaitEvent(t, events, "sessions", sessionsListing(ws, "webkeeper", true, func(label string) bool { return label == "Paused" }))

	status, body = web.call(http.MethodPost, path+"resume", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	require.Eventually(t, func() bool { return hasSession(sb, "webkeeper") }, uiTimeout, 100*time.Millisecond, "resuming never started the agent")

	status, body = web.call(http.MethodPost, path+"kill", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	assert.False(t, hasSession(sb, "webkeeper"), "killing ended the agent's tmux session")
	awaitEvent(t, events, "sessions", sessionsListing(ws, "webkeeper", false, nil))
	status, body = web.call(http.MethodPost, path+"kill", nil)
	assert.Equal(t, http.StatusNotFound, status, "the session is gone: %s", body)

	// Revoking the device ends its stream and its cookie.
	loomWeb("revoke", "e2e")
	awaitEnd(t, events)
	status, _ = web.call(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the revoked token names no device")
}

// The listener answers to its own local origin, http://loom.localhost:<port>,
// beside the configured one: a device pairs over it with a `loom web pair`
// code, its stream opens and an action passes the guard, while bare localhost
// and the IP literal are refused as Host and as Origin. The client dials the
// listener's address whatever the name, so the test does not depend on how
// the machine's resolver treats *.localhost.
func TestE2E_Web_PairsAndStreamsOverTheLocalOrigin(t *testing.T) {
	sb := newSandbox(t, "")
	addr := freeLoopbackAddr(t)
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	web := &webClient{t: t, origin: "http://loom.localhost:" + port, client: &http.Client{Timeout: 45 * time.Second, Transport: transport}}
	loomWeb := webCommand(t, sb)

	// The configured origin is the tunnel's; the local one comes with the listener.
	loomWeb("enable", "--listen", addr, "--origin", "https://loom.example.com")
	awaitListening(t, addr)

	// Before pairing: the guard lets the local host through to the route's
	// own 401, and still refuses the names that are not the listener's.
	status, _, _ := web.round(web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil))
	assert.Equal(t, http.StatusUnauthorized, status, "the local origin reaches the route, which needs the cookie")
	for _, host := range []string{"localhost:" + port, addr} {
		req := web.request(http.MethodGet, "/api/v1/launch-defaults?workspace=1", nil)
		req.Host = host
		status, _, _ = web.round(req)
		assert.Equal(t, http.StatusMisdirectedRequest, status, "the Host %s is not the listener's", host)
	}

	// Pairing over the local origin, then the stream.
	code := pairingCode.FindString(loomWeb("pair", "--name", "e2e-local"))
	require.NotEmpty(t, code, "loom web pair printed no code")
	web.pairWith(code)
	events := web.stream()
	snapshot := awaitEvent(t, events, "snapshot", nil)
	assert.Contains(t, snapshot.Data, devsandbox.WorkspaceName, "the snapshot lists the sandbox's workspace")

	// A write from the local origin passes the guard and reaches the model,
	// which has no such session; one from bare localhost or the IP literal
	// does not pass the guard, cookie or not.
	status, body := web.call(http.MethodPost, "/api/v1/sessions/1/kill", nil)
	assert.Equal(t, http.StatusNotFound, status, "the model says there is no session 1: %s", body)
	for _, origin := range []string{"http://localhost:" + port, "http://" + addr} {
		req := web.request(http.MethodPost, "/api/v1/sessions/1/kill", nil)
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: webCookie, Value: web.token})
		status, _, _ = web.round(req)
		assert.Equal(t, http.StatusForbidden, status, "the Origin %s is not the listener's", origin)
	}
}
