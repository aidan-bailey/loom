package modserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHost is a ModHost that knows one launch, "L1".
type fakeHost struct {
	mu      sync.Mutex
	hello   []core.ModHello
	msgs    []core.ModMessage
	wake    chan struct{}
	events  []core.ModEvent
	serving bool
}

func (h *fakeHost) ModsServing() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serving = true
}

// api is path under the API version the module speaks (claudemod.Version).
func api(path string) string { return fmt.Sprintf("/v%d%s", claudemod.Version, path) }

func (h *fakeHost) ModHello(launch string, hello core.ModHello) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	if hello.Poller == "old" {
		return core.ErrModSuperseded
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hello = append(h.hello, hello)
	return nil
}

func (h *fakeHost) ModInbox(launch, poller string) ([]core.ModMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	if poller == "old" {
		return nil, nil, core.ErrModSuperseded
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	return h.msgs, h.wake, nil
}

func (h *fakeHost) ModEvents(launch string, events []core.ModEvent) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, events...)
	return nil
}

func (h *fakeHost) polled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wake != nil
}

// stoppedHost is a ModHost whose model has stopped, as *core.Loop answers
// after Stop: no error, and a nil channel for a poll.
type stoppedHost struct{ fakeHost }

func (*stoppedHost) ModInbox(string, string) ([]core.ModMessage, <-chan struct{}, error) {
	return nil, nil, nil
}

// shortSocket is a socket path short enough for sun_path whatever TMPDIR is:
// in the temp dir when that is short (a Nix sandbox's /build), else in /tmp.
func shortSocket(t *testing.T) string {
	t.Helper()
	base := ""
	if len(os.TempDir()) > 60 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "lms")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "m.sock")
}

// unixClient is an HTTP client whose every request goes over path.
func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
}

func listen(t *testing.T, h core.ModHost, hold time.Duration) (*Server, *http.Client) {
	t.Helper()
	path := shortSocket(t)
	s, err := Listen(path, h, hold)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s, unixClient(path)
}

func post(t *testing.T, c *http.Client, path, body string) int {
	t.Helper()
	resp, err := c.Post("http://loom"+path, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// get makes a GET in the background and hands back its status (0 for a
// failed request).
func get(c *http.Client, path string) <-chan int {
	done := make(chan int, 1)
	go func() {
		resp, err := c.Get("http://loom" + path)
		if err != nil {
			done <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	return done
}

// A held poll must end before Claude's host aborts the fetch (30 s), and a
// working mod, held and then backing off at most 5 s, must never look silent
// to the model (core.ModPollTimeout).
func TestDefaultHold_FitsClaudesFetchAndTheModelsPollTimeout(t *testing.T) {
	assert.LessOrEqual(t, DefaultHold, 25*time.Second, "5 s under Claude's 30 s fetch abort")
	assert.Less(t, DefaultHold+5*time.Second, core.ModPollTimeout)
}

// Listen tells the model the mod is served from now: every launch's grace
// runs from then at the earliest.
func TestListen_SaysTheModIsServed(t *testing.T) {
	h := &fakeHost{}
	listen(t, h, time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.True(t, h.serving)
}

func TestListen_TheSocketIsPrivate(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	info, err := os.Stat(s.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestHello(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, api("/hello"), `{"launch":"L1","mod":1,"claude":"2.1.299","session":"s"}`))
	assert.Equal(t, []core.ModHello{{Mod: 1, Claude: "2.1.299", Session: "s"}}, h.hello)
	assert.Equal(t, http.StatusNotFound, post(t, c, api("/hello"), `{"launch":"L9"}`))
	assert.Equal(t, http.StatusBadRequest, post(t, c, api("/hello"), `{"launch":`))
	assert.Equal(t, http.StatusBadRequest, post(t, c, api("/hello"), `{"launch":"`+string(bytes.Repeat([]byte("x"), maxBody))+`"}`), "an oversized body")
}

func TestInbox_HandsOutWaitingMessagesAtOnce(t *testing.T) {
	h := &fakeHost{msgs: []core.ModMessage{{ID: "m1", Text: "hi", AsUser: true}}}
	_, c := listen(t, h, time.Hour)

	resp, err := c.Get("http://loom" + api("/inbox?launch=L1"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct{ Messages []core.ModMessage }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, h.msgs, body.Messages)
}

func TestInbox_AnUnknownLaunchIs404(t *testing.T) {
	_, c := listen(t, &fakeHost{}, time.Hour)
	assert.Equal(t, http.StatusNotFound, <-get(c, api("/inbox?launch=L9")))
}

func TestInbox_HoldsUntilWoken(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Hour)
	done := get(c, api("/inbox?launch=L1"))
	require.Eventually(t, h.polled, 5*time.Second, 10*time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("answered %d before any wake", code)
	case <-time.After(100 * time.Millisecond):
	}

	h.mu.Lock()
	close(h.wake)
	h.mu.Unlock()

	assert.Equal(t, http.StatusNoContent, <-done)
}

func TestInbox_AHoldEndsAfterTheHoldTime(t *testing.T) {
	_, c := listen(t, &fakeHost{}, 50*time.Millisecond)
	select {
	case code := <-get(c, api("/inbox?launch=L1")):
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the hold never ended")
	}
}

func TestInbox_AStopEndsAHoldWith503(t *testing.T) {
	h := &fakeHost{}
	s, c := listen(t, h, time.Hour)
	done := get(c, api("/inbox?launch=L1"))
	require.Eventually(t, h.polled, 5*time.Second, 10*time.Millisecond)

	s.Close()

	assert.Equal(t, http.StatusServiceUnavailable, <-done)
	assert.NoFileExists(t, s.path)
}

// A model that has stopped answers a poll with no channel: the daemon is
// going, and the poll hears 503 at once rather than waiting out the hold.
func TestInbox_AStoppedModelIs503(t *testing.T) {
	_, c := listen(t, &stoppedHost{}, time.Hour)
	select {
	case code := <-get(c, api("/inbox?launch=L1")):
		assert.Equal(t, http.StatusServiceUnavailable, code)
	case <-time.After(5 * time.Second):
		t.Fatal("a poll to a stopped model was held")
	}
}

func TestEvents(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, api("/events"), `{"launch":"L1","events":[{"kind":"queued","id":"m1"},{"kind":"complete","id":"m1","turnId":"t","text":"ok","reason":"answer"}]}`))
	assert.Equal(t, []core.ModEvent{{Kind: "queued", ID: "m1"}, {Kind: "complete", ID: "m1", TurnID: "t", Text: "ok", Reason: "answer"}}, h.events)
	assert.Equal(t, http.StatusNotFound, post(t, c, api("/events"), `{"launch":"L9","events":[]}`))
}

func TestClose_IsSafeTwiceAndOnNil(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	s.Close()
	s.Close()
	var none *Server
	none.Close()
}

// helloAt posts a hello for L1 to the mod socket at path, as a fresh client.
func helloAt(t *testing.T, path string) int {
	t.Helper()
	return post(t, unixClient(path), api("/hello"), `{"launch":"L1"}`)
}

// A socket file removed under the server (a tmp cleaner) is listened again
// at the same path, private, and serves.
func TestWatch_ARemovedSocketIsListenedAgain(t *testing.T) {
	h := &fakeHost{}
	s, _ := listen(t, h, time.Second)
	require.NoError(t, os.Remove(s.path))

	require.NoError(t, s.Watch(s.path))

	info, err := os.Stat(s.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, http.StatusNoContent, helloAt(t, s.path))
}

// A socket file something else put in the server's place is replaced by its
// own again.
func TestWatch_AReplacedSocketIsListenedAgain(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	require.NoError(t, os.Remove(s.path))
	require.NoError(t, os.WriteFile(s.path, nil, 0o600))

	require.NoError(t, s.Watch(s.path))

	assert.Equal(t, http.StatusNoContent, helloAt(t, s.path))
}

// An intact socket is left alone: the same file, the same listener, and a
// connection made before the watch still serves.
func TestWatch_AnIntactSocketIsLeftAlone(t *testing.T) {
	s, c := listen(t, &fakeHost{}, time.Second)
	before, err := os.Stat(s.path)
	require.NoError(t, err)

	require.NoError(t, s.Watch(s.path))

	after, err := os.Stat(s.path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "not listened again")
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/hello"), `{"launch":"L1"}`))
}

// A new wanted path (the daemon's socket moved) moves the mod socket: it
// serves there, and the old file, still its own, goes.
func TestWatch_AMovedPathMovesTheSocket(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	old := s.path
	moved := shortSocket(t)

	require.NoError(t, s.Watch(moved))

	assert.Equal(t, http.StatusNoContent, helloAt(t, moved))
	assert.NoFileExists(t, old)
	s.Close()
	assert.NoFileExists(t, moved, "Close removes the socket it listens on now")
}

// A re-listen tells the model the mod is served again: mods could not reach
// it meanwhile, so every launch's grace starts over.
func TestWatch_ARelistenSaysTheModIsServed(t *testing.T) {
	h := &fakeHost{}
	s, _ := listen(t, h, time.Second)
	h.mu.Lock()
	h.serving = false
	h.mu.Unlock()
	require.NoError(t, s.Watch(s.path))
	h.mu.Lock()
	assert.False(t, h.serving, "an intact socket changes nothing")
	h.mu.Unlock()

	require.NoError(t, os.Remove(s.path))
	require.NoError(t, s.Watch(s.path))

	h.mu.Lock()
	defer h.mu.Unlock()
	assert.True(t, h.serving)
}

// A path it can't listen on is an error, and the server keeps what it had.
func TestWatch_AFailedListenIsReported(t *testing.T) {
	s, c := listen(t, &fakeHost{}, time.Second)

	assert.Error(t, s.Watch(filepath.Join(t.TempDir(), "missing", "m.sock")))
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/hello"), `{"launch":"L1"}`))
}

// After Close, a watch listens nowhere.
func TestWatch_AfterCloseDoesNothing(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	s.Close()

	require.NoError(t, s.Watch(s.path))

	assert.NoFileExists(t, s.path)
}

// Close removes only its own socket: a file something else put at the path
// stays.
func TestClose_LeavesAFileThatIsNotItsOwn(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	require.NoError(t, os.Remove(s.path))
	require.NoError(t, os.WriteFile(s.path, []byte("not the mod's"), 0o600))

	s.Close()

	assert.FileExists(t, s.path)
}

// A poll from a module instance a newer one replaced hears 409, so it stops;
// the hello and the poll carry the poller to the model.
func TestInbox_ASupersededPollerIs409(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, api("/hello"), `{"launch":"L1","poller":"new"}`))
	assert.Equal(t, "new", h.hello[0].Poller)
	assert.Equal(t, http.StatusConflict, <-get(c, api("/inbox?launch=L1&poller=old")))
}

// A hello from a module instance loaded before the one the model holds hears
// 409 too, so it stops; the hello carries its load time to the model.
func TestHello_ASupersededModIs409(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, api("/hello"), `{"launch":"L1","poller":"new","loaded":2000}`))
	assert.Equal(t, int64(2000), h.hello[0].Loaded)
	assert.Equal(t, http.StatusConflict, post(t, c, api("/hello"), `{"launch":"L1","poller":"old","loaded":1000}`))
}
