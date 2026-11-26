//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The web terminal end to end: a paired device's WebSocket reaches a session's worktree shell
// through a real daemon, as the app's terminal.html does. The route is refused while the host's
// switch is off and works once `loom web terminal on` turns it on.

// terminalEscapes matches the terminal escape sequences tmux draws around the shell's text, so
// the test reads the text alone: CSI sequences with any parameter and intermediate bytes (private
// markers `<`, `=`, `>` and `?` included), OSC strings ended by BEL or ST, charset selects and the
// single-character escapes.
var terminalEscapes = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78M]")

// terminalDialTimeout bounds one dial, well under the retry's window, so a slow answer from the
// model costs one attempt and not the whole window.
const terminalDialTimeout = 5 * time.Second

// terminalDial opens the terminal route of session id as the page does: with the device's cookie
// (none when token is empty) and the layer's origin. A refusal before the upgrade comes back as its
// status and body, with no connection.
func terminalDial(t *testing.T, addr, token string, id uint64) (conn *websocket.Conn, status int, body string) {
	t.Helper()
	header := http.Header{"Origin": {"http://" + addr}}
	if token != "" {
		header.Set("Cookie", (&http.Cookie{Name: webCookie, Value: token}).String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalDialTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/v1/sessions/%d/terminal", addr, id), &websocket.DialOptions{HTTPHeader: header})
	if resp != nil {
		status = resp.StatusCode
		if status != http.StatusSwitchingProtocols && resp.Body != nil {
			raw, _ := io.ReadAll(resp.Body)
			body = string(raw)
			_ = resp.Body.Close()
		}
	}
	if err == nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
		conn.SetReadLimit(1 << 20) // the daemon's history frames are up to 64 KiB; the library's default is 32 KiB
	}
	return conn, status, body
}

// awaitTerminalLive reads frames until the daemon's text frame says the attach is live, which
// follows the shell's history.
func awaitTerminalLive(ctx context.Context, t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		typ, data, err := conn.Read(ctx)
		require.NoError(t, err, "the terminal ended before it said it was live")
		if typ == websocket.MessageText {
			require.JSONEq(t, `{"state":"live"}`, string(data))
			return
		}
	}
}

// readTerminalUntil collects binary frames until their text, escapes removed, holds want, and
// returns that text.
func readTerminalUntil(ctx context.Context, t *testing.T, conn *websocket.Conn, want string) string {
	t.Helper()
	var raw bytes.Buffer
	text := ""
	for !strings.Contains(text, want) {
		typ, data, err := conn.Read(ctx)
		require.NoError(t, err, "no %q in the terminal's output: %q", want, text)
		if typ == websocket.MessageBinary {
			raw.Write(data)
			text = terminalEscapes.ReplaceAllString(raw.String(), "")
		}
	}
	return text
}

// A session is created over the API, and its terminal is refused while the switch is off: 401
// without the device's cookie, 403 with it. `loom web terminal on` opens it, and a client types a
// command into the shell and reads the shell's own output back. The shell is the tmux session
// term_<title>, which closing the connection leaves running.
func TestE2E_Web_TerminalReachesASessionsShell(t *testing.T) {
	// The daemon starts the shell from its own $SHELL, which it inherits from the first loom
	// command of the test: a developer's fish or zsh would change the prompt and the arithmetic.
	t.Setenv("SHELL", "/bin/sh")
	sb := newSandbox(t, "")
	addr := freeLoopbackAddr(t)
	web := &webClient{t: t, origin: "http://" + addr, client: &http.Client{Timeout: 45 * time.Second}}
	loomWeb := webCommand(t, sb)

	loomWeb("enable", "--listen", addr, "--origin", web.origin)
	awaitListening(t, addr)
	code := pairingCode.FindString(loomWeb("pair", "--name", "e2e-terminal"))
	require.NotEmpty(t, code, "loom web pair printed no code")
	web.pairWith(code)

	// A session, started over the API.
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
	status, body := web.call(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": ws, "title": "webterm"})
	require.Equal(t, http.StatusCreated, status, string(body))
	var created struct {
		ID uint64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotZero(t, created.ID)
	awaitEvent(t, events, "sessions", sessionsListing(ws, "webterm", true, nil))
	require.Eventually(t, func() bool { return hasSession(sb, "webterm") }, uiTimeout, 100*time.Millisecond, "the agent's tmux session never started")
	require.Eventually(t, func() bool {
		return strings.Contains(paneText(t, sb, tmux.ToLoomTmuxName("webterm")), "commands: work N")
	}, uiTimeout, 100*time.Millisecond, "the fake agent came up")
	shell := tmux.TerminalSessionName("webterm")
	require.False(t, hasSession(sb, shell), "no shell exists before a terminal is opened")

	// The switch is off by default: the cookie is checked first (401), then the switch (403), and
	// the model is never asked for a shell.
	_, status, refused := terminalDial(t, addr, "", created.ID)
	assert.Equal(t, http.StatusUnauthorized, status, "a request with no device cookie is refused before anything else: %s", refused)
	_, status, refused = terminalDial(t, addr, web.token, created.ID)
	assert.Equal(t, http.StatusForbidden, status, "the paired device is refused while the switch is off: %s", refused)
	var refusal struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(refused), &refusal), refused)
	assert.Equal(t, "the terminal is off on this host", refusal.Error)
	require.False(t, hasSession(sb, shell), "a refused request made no shell")

	// On: the same request upgrades. The model may still be loading the session, which it refuses
	// with a 409, so the dial is retried until it opens.
	out := loomWeb("terminal", "on")
	require.Contains(t, out, "The web terminal is on.")
	// A plain loop in the test's own goroutine: no dial outlives the window, so nothing writes
	// lastStatus or lastBody after the failure message reads them.
	var conn *websocket.Conn
	lastStatus, lastBody := 0, ""
	for deadline := time.Now().Add(uiTimeout); conn == nil; {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never opened: the last answer was %d %q", lastStatus, lastBody)
		}
		conn, lastStatus, lastBody = terminalDial(t, addr, web.token, created.ID)
		if conn == nil {
			time.Sleep(250 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), uiTimeout)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":100,"rows":30}}`)))
	awaitTerminalLive(ctx, t, conn)

	// The typed command is echoed back as typed, so the marker is one only the shell can make:
	// the arithmetic is in the command and its result is in the output.
	require.NoError(t, conn.Write(ctx, websocket.MessageBinary, []byte("echo LOOM$((20+22))\r")))
	text := readTerminalUntil(ctx, t, conn, "LOOM42")
	assert.Contains(t, text, "LOOM42")
	assert.True(t, hasSession(sb, shell), "the session's term_ shell is on the tmux server")

	// The browser leaving ends the attach and never the shell.
	require.NoError(t, conn.CloseNow())
	require.Never(t, func() bool { return !hasSession(sb, shell) }, 2*time.Second, 100*time.Millisecond, "the shell outlives its connection")
	assert.True(t, hasSession(sb, "webterm"), "the agent's own session is untouched")
}
