package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamWait bounds how long a stream test waits for something to happen.
const streamWait = 3 * time.Second

// streamSession is a session the fixture's workspace holds from the start: the
// status it is in, and the wait fields it carries. None is ever started.
type streamSession struct {
	title          string
	status         session.Status
	reason, detail string
}

// streamEnv is Story 6's webStack over a model that serves the workspace "toy"
// with the given sessions, on a loop that holds its jobs and never ticks. The
// layer listens at addr, and cookie is the paired device "iPhone"'s. The hub's
// knobs are tuned to 20 ms between lists.
type streamEnv struct {
	*webStack

	addr     string
	cookie   *http.Cookie
	ws       core.WorkspaceID
	insts    map[string]*session.Instance
	ids      map[string]core.InstanceID
	sessions int
}

func newStreamEnv(t *testing.T, seeds ...streamSession) *streamEnv {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	repo := t.TempDir()
	def := config.Workspace{Name: "toy", Path: repo}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(`{"default_program":"claude"}`), 0o644))
	reg := &config.WorkspaceRegistry{}
	require.NoError(t, reg.Add("toy", repo))

	m := core.New(core.Options{Registry: reg, CmdExec: deadMachine()})
	m.Boot()
	var toy core.WorkspaceView
	for _, v := range m.Workspaces() {
		if v.Name == "toy" {
			toy = v
		}
	}
	require.NotZero(t, toy.ID, "the model serves the workspace toy")
	e := &streamEnv{
		ws:       toy.ID,
		insts:    map[string]*session.Instance{},
		ids:      map[string]core.InstanceID{},
		sessions: len(seeds),
	}
	for _, seed := range seeds {
		inst, err := session.NewInstance(session.InstanceOptions{Title: seed.title, Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		require.NoError(t, inst.TransitionTo(seed.status))
		inst.SetWaitReason(seed.reason)
		inst.SetWaitDetail(seed.detail)
		m.WorkspaceForTest(toy.ID).AddForTest(inst)
		e.insts[seed.title] = inst
		e.ids[seed.title] = m.IDOfForTest(inst)
	}

	// newWebStack's stack, but on a loop that holds its jobs and has not
	// begun, so nothing ticks.
	e.webStack = &webStack{loop: core.StartForTest(m)}
	e.srv = rpc.NewServer(e.loop)
	t.Cleanup(func() {
		e.layer.Close()
		if e.tui != nil {
			e.tui.Close()
		}
		e.srv.Close()
		e.loop.Stop()
	})
	e.start(t)
	e.addr, _ = e.enable(t)
	e.cookie = e.pair(t, e.addr, "iPhone")
	e.tune(func(h *hub) { h.every = 20 * time.Millisecond })
	e.settle(t)
	return e
}

// waitUntil polls cond, on the test's goroutine, until it holds.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * streamWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tune changes the hub's knobs under its lock, as its goroutines read them.
func (e *streamEnv) tune(fn func(h *hub)) {
	h := e.layer.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(h)
}

// waitHub polls ok, which runs under the hub's lock, until it holds.
func (e *streamEnv) waitHub(t *testing.T, what string, ok func(h *hub) bool) {
	t.Helper()
	h := e.layer.hub
	waitUntil(t, what, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return ok(h)
	})
}

// settle waits until the hub holds the fixture's sessions and the device, and
// has published all it holds, so a stream opened next is level with every
// other and sees only what the test does after.
func (e *streamEnv) settle(t *testing.T) {
	t.Helper()
	e.waitHub(t, "the hub never read the model and published it", func(h *hub) bool {
		if len(h.devices) == 0 || len(h.fleet.workspaces) == 0 {
			return false
		}
		total := 0
		for _, list := range h.fleet.sessions {
			total += len(list)
		}
		if total != e.sessions || !reflect.DeepEqual(h.fleet.workspaces, h.pubWorkspaces) {
			return false
		}
		for _, w := range h.fleet.workspaces {
			if !reflect.DeepEqual(h.fleet.sessions[w.ID], h.pubSessions[w.ID]) {
				return false
			}
		}
		return true
	})
}

// change edits a session on the model, then has the server publish it.
func (e *streamEnv) change(t *testing.T, edit func()) {
	t.Helper()
	edit()
	require.NoError(t, e.tui.FlushForTest())
}

// pairAs pairs a device through the pairing route and waits until the hub has
// read its record, so a stream opened with the cookie is not refused for being
// ahead of the hub.
func (e *streamEnv) pairAs(t *testing.T, name string) *http.Cookie {
	t.Helper()
	cookie := e.pair(t, e.addr, name)
	e.waitHub(t, "the hub never read the device "+name, func(h *hub) bool {
		_, ok := h.devices[name]
		return ok
	})
	return cookie
}

// open connects a stream with cookie, which must be accepted.
func (e *streamEnv) open(t *testing.T, cookie *http.Cookie) *streamReader {
	t.Helper()
	resp := httpDo(t, e.addr, http.MethodGet, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(cookie) })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream answered %d: %s", resp.StatusCode, bodyText(t, resp))
	}
	return readFrames(t, resp)
}

// attempt connects a stream with cookie and returns the status, and the body
// of a refusal. A stream it was given is closed again at once.
func (e *streamEnv) attempt(t *testing.T, cookie *http.Cookie) (int, string) {
	t.Helper()
	resp := httpDo(t, e.addr, http.MethodGet, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(cookie) })
	if resp.StatusCode == http.StatusOK {
		_ = resp.Body.Close()
		return resp.StatusCode, ""
	}
	return resp.StatusCode, bodyText(t, resp)
}

// hubSaw runs trigger, then waits until the drain has handed the sinks an
// event that match accepts. The probe sink is added after the hub, so the hub
// has applied that batch by the time the probe sees it.
func (e *streamEnv) hubSaw(t *testing.T, match func(core.Event) bool, trigger func()) {
	t.Helper()
	saw := make(chan struct{})
	var once sync.Once
	e.layer.addSink(funcSink(func(evs []core.Event) {
		for _, ev := range evs {
			if match(ev) {
				once.Do(func() { close(saw) })
			}
		}
	}))
	trigger()
	select {
	case <-saw:
	case <-time.After(streamWait):
		t.Fatal("the drain never handed the sinks the change")
	}
}

// holdDrain stops the layer's drain inside a sink: it changes inst's program,
// which the app does not show, and waits until a batch has reached the gate.
// From then on the hub reads nothing more from the model, until the returned
// function lets the drain go on. The test's cleanup lets it go too, before the
// layer closes.
func (e *streamEnv) holdDrain(t *testing.T, inst *session.Instance) (release func()) {
	t.Helper()
	held, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.layer.addSink(funcSink(func([]core.Event) {
		once.Do(func() {
			close(held)
			<-gate
		})
	}))
	release = sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	e.change(t, func() { inst.SetProgram("held") })
	select {
	case <-held:
	case <-time.After(streamWait):
		t.Fatal("the drain never reached the gate")
	}
	return release
}

// viewsWhere matches a ViewsChanged of workspace ws that holds a view ok accepts.
func viewsWhere(ws core.WorkspaceID, ok func(core.InstanceView) bool) func(core.Event) bool {
	return func(ev core.Event) bool {
		vc, isViews := ev.(core.ViewsChanged)
		return isViews && vc.WS == ws && slices.ContainsFunc(vc.Views, ok)
	}
}

// streamFrame is one server-sent event, with the time it was read.
type streamFrame struct {
	Event string
	Data  json.RawMessage
	At    time.Time
}

// streamReader reads a response as server-sent events.
type streamReader struct {
	t      *testing.T
	frames chan streamFrame
	quit   chan struct{}
}

func readFrames(t *testing.T, resp *http.Response) *streamReader {
	t.Helper()
	r := &streamReader{t: t, frames: make(chan streamFrame, 256), quit: make(chan struct{})}
	t.Cleanup(func() { close(r.quit) })
	go func() {
		defer close(r.frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var event string
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if event != "" {
					f := streamFrame{Event: event, Data: json.RawMessage(strings.Join(data, "\n")), At: time.Now()}
					select {
					case r.frames <- f:
					case <-r.quit:
						return
					}
				}
				event, data = "", nil
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	return r
}

func (r *streamReader) next() streamFrame {
	r.t.Helper()
	select {
	case f, ok := <-r.frames:
		require.True(r.t, ok, "the stream ended")
		return f
	case <-time.After(streamWait):
		r.t.Fatal("no event within the wait")
		return streamFrame{}
	}
}

// expect reads the next event and requires its name.
func (r *streamReader) expect(event string) streamFrame {
	r.t.Helper()
	f := r.next()
	require.Equal(r.t, event, f.Event, "data: %s", f.Data)
	return f
}

// none requires that nothing arrives, and the stream stays open, for d.
func (r *streamReader) none(d time.Duration) {
	r.t.Helper()
	select {
	case f, ok := <-r.frames:
		if !ok {
			r.t.Fatal("the stream ended")
		}
		r.t.Fatalf("unexpected %s event: %s", f.Event, f.Data)
	case <-time.After(d):
	}
}

// ended reads until the stream ends and returns what came before.
func (r *streamReader) ended() []streamFrame {
	r.t.Helper()
	var got []streamFrame
	timeout := time.After(streamWait)
	for {
		select {
		case f, ok := <-r.frames:
			if !ok {
				return got
			}
			got = append(got, f)
		case <-timeout:
			r.t.Fatalf("the stream did not end; read %d events", len(got))
		}
	}
}

func unframe[T any](t *testing.T, f streamFrame) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(f.Data, &v), "data: %s", f.Data)
	return v
}

// sessionNamed is the session titled title in list.
func sessionNamed(t *testing.T, list []sessionJSON, title string) sessionJSON {
	t.Helper()
	for _, s := range list {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("no session titled %q in %v", title, list)
	return sessionJSON{}
}

func TestStream_StartsWithASnapshot(t *testing.T) {
	e := newStreamEnv(t,
		streamSession{title: "fix login", status: session.Prompting, reason: "permission", detail: "rm -rf build"},
		streamSession{title: "add api", status: session.Running},
		streamSession{title: "old work", status: session.Paused},
	)

	s := e.open(t, e.cookie)
	snap := unframe[snapshotJSON](t, s.expect("snapshot"))

	assert.Equal(t, "test build", snap.Build)
	assert.Equal(t, "testhost", snap.Host)
	var labels []string
	for _, w := range snap.Workspaces {
		labels = append(labels, w.Label)
	}
	assert.ElementsMatch(t, []string{"global", "toy"}, labels)
	assert.Len(t, snap.Sessions, 2, "one list for each workspace")

	list := snap.Sessions[e.ws]
	require.Len(t, list, 3)
	fix := sessionNamed(t, list, "fix login")
	assert.Equal(t, e.ids["fix login"], fix.ID)
	assert.Equal(t, e.ws, fix.Workspace)
	assert.Equal(t, "needs_you", fix.Group)
	assert.Equal(t, "Needs you", fix.Label)
	assert.Equal(t, "Prompting", fix.Status)
	assert.Equal(t, waitJSON{Reason: "permission", Detail: "rm -rf build"}, fix.Wait)
	_, err := time.Parse(time.RFC3339, fix.Since)
	assert.NoError(t, err, "the status was entered in this process")
	assert.Equal(t, "working", sessionNamed(t, list, "add api").Group)
	assert.Equal(t, "paused", sessionNamed(t, list, "old work").Group)
	assert.Equal(t, 1, snap.Badge, "one session needs you")

	assert.Equal(t, deviceJSON{Name: "iPhone", Notify: notifyJSON{Waiting: true, Finished: true}}, snap.Device)
	assert.Empty(t, snap.Notices)
	s.none(100 * time.Millisecond)
}

func TestStream_AnEmptyFleetIsEmptyListsNotNulls(t *testing.T) {
	e := newStreamEnv(t)

	s := e.open(t, e.cookie)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(s.expect("snapshot").Data, &raw))

	assert.JSONEq(t, `[]`, string(raw["notices"]))
	assert.JSONEq(t, `0`, string(raw["badge"]))
	assert.NotEqual(t, "null", string(raw["workspaces"]))
	var lists map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["sessions"], &lists))
	require.Len(t, lists, 2, "the global workspace and toy")
	for id, list := range lists {
		assert.JSONEq(t, `[]`, string(list), "workspace %s", id)
	}
}

func TestStream_SendsTheWholeListWhenASessionChanges(t *testing.T) {
	e := newStreamEnv(t,
		streamSession{title: "fix login", status: session.Running, reason: "permission", detail: "go test ./..."},
		streamSession{title: "add api", status: session.Running},
	)
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	e.change(t, func() { require.NoError(t, e.insts["fix login"].TransitionTo(session.Prompting)) })

	list := unframe[sessionsJSON](t, s.expect("sessions"))
	assert.Equal(t, e.ws, list.Workspace)
	require.Len(t, list.Sessions, 2, "the whole list, not the one that changed")
	fix := sessionNamed(t, list.Sessions, "fix login")
	assert.Equal(t, "needs_you", fix.Group)
	assert.Equal(t, waitJSON{Reason: "permission", Detail: "go test ./..."}, fix.Wait)
	assert.Equal(t, "working", sessionNamed(t, list.Sessions, "add api").Group)
	s.none(100 * time.Millisecond)
}

func TestStream_ASessionsEventCarriesTheBadgeCount(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "fix login", status: session.Running})
	s := e.open(t, e.cookie)
	require.Zero(t, unframe[snapshotJSON](t, s.expect("snapshot")).Badge)

	e.change(t, func() { require.NoError(t, e.insts["fix login"].TransitionTo(session.Prompting)) })
	assert.Equal(t, 1, unframe[sessionsJSON](t, s.expect("sessions")).Badge, "a session entered Prompting")

	e.change(t, func() { require.NoError(t, e.insts["fix login"].TransitionTo(session.Running)) })
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(s.expect("sessions").Data, &raw))
	assert.JSONEq(t, `0`, string(raw["badge"]), "answered, the count is sent as 0, not left out")
}

func TestStream_AWorkspacesEventCarriesTheBadgeCount(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "fix login", status: session.Prompting})
	s := e.open(t, e.cookie)
	require.Equal(t, 1, unframe[snapshotJSON](t, s.expect("snapshot")).Badge)
	var toy []core.WorkspaceView
	for _, v := range e.tui.Workspaces() {
		if v.ID == e.ws {
			toy = append(toy, v)
		}
	}
	require.Len(t, toy, 1)

	// The hub is told what the drain would tell it when the other workspace
	// closes, then when toy does.
	e.layer.hub.apply([]core.Event{core.WorkspacesChanged{Views: toy}})
	assert.Equal(t, 1, unframe[workspacesJSON](t, s.expect("workspaces")).Badge, "toy's waiting session stays")

	e.layer.hub.apply([]core.Event{core.WorkspacesChanged{Views: nil}})
	f := s.expect("workspaces")
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(f.Data, &raw))
	assert.JSONEq(t, `0`, string(raw["badge"]), "toy's waiting session left with it, and the count is sent as 0")
}

func TestStream_SendsTheWorkspacesWhenTheyChange(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	require.NoError(t, e.tui.RenameWorkspace("toy", "renamed"))

	ws := unframe[workspacesJSON](t, s.expect("workspaces"))
	var toy workspaceJSON
	for _, w := range ws.Workspaces {
		if w.ID == e.ws {
			toy = w
		}
	}
	assert.Equal(t, workspaceJSON{ID: e.ws, Name: "renamed", Label: "renamed"}, toy, "the ID stays")
	s.none(100 * time.Millisecond) // its sessions did not change
}

func TestStream_BatchesAWorkspacesChangesIntoFewLists(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "busy", status: session.Running})
	e.tune(func(h *hub) { h.every = 150 * time.Millisecond })
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	const changes = 10
	for i := range changes {
		e.change(t, func() { e.insts["busy"].SetWaitReason(fmt.Sprintf("reason %d", i)) })
	}

	lists := 0
	for {
		list := unframe[sessionsJSON](t, s.expect("sessions"))
		lists++
		if sessionNamed(t, list.Sessions, "busy").Wait.Reason == fmt.Sprintf("reason %d", changes-1) {
			break
		}
	}
	assert.Less(t, lists, changes, "the changes were batched, not sent one by one")
}

func TestStream_PublishesAtMostFourListsASecond(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "busy", status: session.Running})
	e.tune(func(h *hub) { h.every = publishEvery })
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	// A change every 10 ms for a second and a half.
	var last string
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end); {
		last = fmt.Sprintf("reason at %d", time.Now().UnixNano())
		e.change(t, func() { e.insts["busy"].SetWaitReason(last) })
		time.Sleep(10 * time.Millisecond)
	}

	var arrivals []time.Time
	var final sessionsJSON
collect:
	for {
		select {
		case f, ok := <-s.frames:
			require.True(t, ok, "the stream ended")
			if f.Event == "sessions" {
				arrivals = append(arrivals, f.At)
				final = unframe[sessionsJSON](t, f)
			}
		case <-time.After(2 * publishEvery):
			break collect
		}
	}

	require.GreaterOrEqual(t, len(arrivals), 3, "the lists keep coming while the workspace changes")
	for i := 1; i < len(arrivals); i++ {
		assert.GreaterOrEqual(t, arrivals[i].Sub(arrivals[i-1]), publishEvery-100*time.Millisecond)
	}
	assert.Equal(t, last, sessionNamed(t, final.Sessions, "busy").Wait.Reason, "the last list holds the last change")
}

func TestStream_AChangeTheAppCannotSeeIsNotPublished(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "fix", status: session.Running})
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	// The program is in the model's view of the session and not in the app's.
	e.hubSaw(t, viewsWhere(e.ws, func(v core.InstanceView) bool { return v.Program == "aider" }), func() {
		e.change(t, func() { e.insts["fix"].SetProgram("aider") })
	})
	s.none(200 * time.Millisecond)

	e.change(t, func() { e.insts["fix"].SetWaitReason("because") })
	list := unframe[sessionsJSON](t, s.expect("sessions"))
	assert.Equal(t, "because", sessionNamed(t, list.Sessions, "fix").Wait.Reason)
}

func TestStream_NoticesNotTiedToARequestReachEveryStreamAndTheNextSnapshot(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a := e.open(t, e.cookie)
	a.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	// Story 8's tracker takes a notice tied to a request of the web client, and
	// the sink leaves it alone; an empty notice says nothing. (A literal batch:
	// the model ties a notice to a request only inside a request's job.)
	e.layer.hub.apply([]core.Event{core.Notice{Info: "belongs to a request", Req: 5}, core.Notice{}})
	// A notice the model raises on its own reaches every connection.
	e.loop.WebServing("", errors.New("push failed"))

	for _, s := range []*streamReader{a, b} {
		n := unframe[noticeJSON](t, s.expect("notice"))
		assert.Contains(t, n.Text, "push failed", "the notice tied to a request was not published")
		assert.True(t, n.Error)
		assert.WithinDuration(t, time.Now(), n.At, 10*time.Second)
	}
	snap := unframe[snapshotJSON](t, e.open(t, e.cookie).expect("snapshot"))
	require.Len(t, snap.Notices, 1)
	assert.Contains(t, snap.Notices[0].Text, "push failed")
}

func TestHub_KeepsTheLastTwentyNotices(t *testing.T) {
	e := newStreamEnv(t)
	for i := range recentNotices + 5 {
		e.layer.hub.publish("notice", noticeJSON{Text: fmt.Sprintf("n%d", i), At: time.Now()})
	}

	snap := unframe[snapshotJSON](t, e.open(t, e.cookie).expect("snapshot"))

	require.Len(t, snap.Notices, recentNotices)
	assert.Equal(t, "n5", snap.Notices[0].Text)
	assert.Equal(t, "n24", snap.Notices[recentNotices-1].Text)
}

func TestHub_PublishAndSendToReachTheRightStreamsAndAreKept(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a := e.open(t, e.cookie)
	a.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	e.layer.hub.sendTo("iPhone", "notice", noticeJSON{Text: "only you", At: time.Now()})
	e.layer.hub.publish("notice", noticeJSON{Text: "everyone", At: time.Now()})
	e.layer.hub.sendTo("iPhone", "device", deviceJSON{Name: "iPhone"})

	assert.Equal(t, "only you", unframe[noticeJSON](t, a.expect("notice")).Text)
	assert.Equal(t, "everyone", unframe[noticeJSON](t, a.expect("notice")).Text)
	a.expect("device")
	assert.Equal(t, "everyone", unframe[noticeJSON](t, b.expect("notice")).Text)
	b.none(100 * time.Millisecond)

	// Both notices are in the next snapshot, the one sent to a single device too;
	// the device event is not a notice and is not kept.
	snap := unframe[snapshotJSON](t, e.open(t, padCookie).expect("snapshot"))
	require.Len(t, snap.Notices, 2)
	assert.Equal(t, "only you", snap.Notices[0].Text)
	assert.Equal(t, "everyone", snap.Notices[1].Text)
}

func TestHub_ANoticeSentToADeviceWithNoStreamIsInItsNextSnapshot(t *testing.T) {
	e := newStreamEnv(t)

	// Story 8 sends a request's late notice to its device, which may have
	// left: the phone finds it when it comes back.
	e.layer.hub.sendTo("iPhone", "notice", noticeJSON{Text: "while you were away", Error: true, At: time.Now()})

	snap := unframe[snapshotJSON](t, e.open(t, e.cookie).expect("snapshot"))
	require.Len(t, snap.Notices, 1)
	assert.Equal(t, "while you were away", snap.Notices[0].Text)
	assert.True(t, snap.Notices[0].Error)
}

func TestStream_ADeviceEventGoesToThatDevicesStreamsOnly(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a := e.open(t, e.cookie)
	a.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	off := false
	got, err := e.loop.SetNotify("iPhone", core.NotifyChange{Finished: &off})
	require.NoError(t, err)
	require.False(t, got.Notify.Finished)

	dev := unframe[deviceJSON](t, a.expect("device"))
	assert.Equal(t, deviceJSON{Name: "iPhone", Notify: notifyJSON{Waiting: true}}, dev)
	// The app's record has no last-seen time: touching a device says nothing.
	a.none(100 * time.Millisecond)
	b.none(100 * time.Millisecond)
}

func TestStream_RevokingADeviceEndsItsStreamsWithNoBye(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a1 := e.open(t, e.cookie)
	a1.expect("snapshot")
	a2 := e.open(t, e.cookie)
	a2.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	require.NoError(t, e.tui.RevokeDevice("iPhone"))

	for _, s := range []*streamReader{a1, a2} {
		for _, f := range s.ended() {
			assert.NotEqual(t, "bye", f.Event, "the app would call a revoked stream a restart")
		}
	}
	status, _ := e.attempt(t, e.cookie)
	assert.Equal(t, http.StatusUnauthorized, status, "the revoked cookie")
	e.layer.hub.publish("notice", noticeJSON{Text: "still here", At: time.Now()})
	assert.Equal(t, "still here", unframe[noticeJSON](t, b.expect("notice")).Text)
}

func TestStream_ADeviceTheHubHasNotReadYetIsRefusedUntilItHas(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	release := e.holdDrain(t, e.insts["one"])

	// The model pairs the iPad while the drain is held: it vouches for the
	// cookie, and the list the hub has read does not name the device.
	padCookie := e.pair(t, e.addr, "iPad")
	status, body := e.attempt(t, padCookie)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, body, "try again")

	release()
	waitUntil(t, "the hub never read the iPad's record", func() bool {
		status, _ := e.attempt(t, padCookie)
		return status == http.StatusOK
	})
}

func TestStream_ADeviceOfTheSameNamePairedAgainEndsTheOldStreams(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	old := e.open(t, e.cookie)
	old.expect("snapshot")
	release := e.holdDrain(t, e.insts["one"])

	// The iPhone is revoked and paired again while the hub looks away, so the
	// list it reads next names the iPhone with another pairing time.
	require.NoError(t, e.tui.RevokeDevice("iPhone"))
	fresh := e.pair(t, e.addr, "iPhone")
	release()

	for _, f := range old.ended() {
		assert.NotEqual(t, "bye", f.Event)
	}
	e.open(t, fresh).expect("snapshot")
}

func TestHub_CloseDeviceEndsOnlyThatDevicesStreams(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a := e.open(t, e.cookie)
	a.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	e.layer.hub.closeDevice("iPhone")

	for _, f := range a.ended() {
		assert.NotEqual(t, "bye", f.Event)
	}
	b.none(100 * time.Millisecond)
}

func TestStream_ATextInASessionCannotForgeAFrame(t *testing.T) {
	hostile := "x\n\nevent: bye\ndata: {}\n\n"
	e := newStreamEnv(t, streamSession{title: hostile, status: session.Running})
	s := e.open(t, e.cookie)
	snap := unframe[snapshotJSON](t, s.expect("snapshot"))
	require.Len(t, snap.Sessions[e.ws], 1)
	assert.Equal(t, hostile, snap.Sessions[e.ws][0].Title)

	e.change(t, func() { e.insts[hostile].SetWaitDetail(hostile) })

	list := unframe[sessionsJSON](t, s.expect("sessions"))
	assert.Equal(t, hostile, list.Sessions[0].Wait.Detail)
	s.none(100 * time.Millisecond) // no bye, and no event of its own
}

func TestStream_PingsKeepItOpen(t *testing.T) {
	e := newStreamEnv(t)
	e.tune(func(h *hub) { h.ping = 30 * time.Millisecond })
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	s.expect("ping")
	s.expect("ping")
}

func TestStream_SetsTheDeviceCookieAgain(t *testing.T) {
	e := newStreamEnv(t)

	resp := httpDo(t, e.addr, http.MethodGet, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(e.cookie) })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	readFrames(t, resp).expect("snapshot")

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			got = c
		}
	}
	require.NotNil(t, got, "the cookie is set again so a device in use never expires")
	assert.Equal(t, e.cookie.Value, got.Value)
	assert.Positive(t, got.MaxAge)
}

func TestStream_AFifthStreamEndsTheDevicesOldest(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	var held []*streamReader
	for range maxStreamsPerDevice {
		s := e.open(t, e.cookie)
		s.expect("snapshot")
		held = append(held, s)
	}
	other := e.open(t, padCookie)
	other.expect("snapshot")

	fifth := e.open(t, e.cookie)
	fifth.expect("snapshot")

	for _, f := range held[0].ended() {
		assert.NotEqual(t, "bye", f.Event)
	}
	e.layer.hub.publish("notice", noticeJSON{Text: "alive", At: time.Now()})
	alive := append([]*streamReader{fifth, other}, held[1:]...)
	for _, s := range alive {
		assert.Equal(t, "alive", unframe[noticeJSON](t, s.expect("notice")).Text)
	}
}

func TestStream_ThirtyTwoStreamsAreTheMost(t *testing.T) {
	e := newStreamEnv(t)
	perDevice := maxStreams / maxStreamsPerDevice
	cookies := []*http.Cookie{e.cookie}
	for i := 1; i <= perDevice; i++ {
		cookies = append(cookies, e.pairAs(t, fmt.Sprintf("device-%d", i)))
	}
	var all []*streamReader
	for _, cookie := range cookies[:perDevice] {
		for range maxStreamsPerDevice {
			s := e.open(t, cookie)
			s.expect("snapshot")
			all = append(all, s)
		}
	}
	require.Len(t, all, maxStreams)

	// A 33rd, from a device with no stream, is refused.
	extra := cookies[perDevice]
	status, body := e.attempt(t, extra)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, body, "too many open streams")

	// A fifth from a device at its own limit takes that device's oldest
	// stream's place, so the total stays at 32.
	e.open(t, cookies[0]).expect("snapshot")
	for _, f := range all[0].ended() {
		assert.NotEqual(t, "bye", f.Event)
	}
	status, _ = e.attempt(t, extra)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestStream_AStreamThatCannotKeepUpIsEnded(t *testing.T) {
	e := newStreamEnv(t)
	slow, err := e.layer.hub.subscribe("iPhone") // nothing reads it
	require.NoError(t, err)
	t.Cleanup(func() { e.layer.hub.release(slow) })

	for range streamQueue + 5 {
		e.layer.hub.publish("notice", noticeJSON{Text: "x", At: time.Now()})
	}

	select {
	case <-slow.done:
	default:
		t.Fatal("the stream that fell behind was not ended")
	}
	e.layer.hub.mu.Lock()
	_, held := e.layer.hub.subs[slow]
	e.layer.hub.mu.Unlock()
	assert.False(t, held)

	// The next connection starts clean, with a snapshot.
	e.open(t, e.cookie).expect("snapshot")
}

func TestStream_TheLayersCloseSendsByeThenEndsEveryStreamAndRefusesMore(t *testing.T) {
	e := newStreamEnv(t)
	padCookie := e.pairAs(t, "iPad")
	a := e.open(t, e.cookie)
	a.expect("snapshot")
	b := e.open(t, padCookie)
	b.expect("snapshot")

	e.layer.Close()

	for _, s := range []*streamReader{a, b} {
		s.expect("bye")
		s.ended()
	}
	requireNoListener(t, e.addr)
	_, err := e.layer.hub.subscribe("iPhone")
	assert.ErrorIs(t, err, errHubClosed)
}

func TestHub_CloseOfNoHubIsANoop(t *testing.T) {
	var h *hub
	assert.NotPanics(t, h.close)
}

func TestHub_APanicInTheFlusherStopsTheWholeLayer(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	e.tune(func(h *hub) {
		h.every = 5 * time.Millisecond
		h.flushFn = func() { panic("boom") }
	})
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	e.change(t, func() { e.insts["one"].SetWaitReason("because") })

	// The layer stops as it does for a panic in the drain: the streams end (the
	// stop says bye to them first), the listener closes, and the model is told
	// why, which keeps a notice for the next TUI. The daemon goes on.
	s.ended()
	requireNoListener(t, e.addr)
	view := e.waitWeb(t, func(v core.WebView) bool { return !v.Serving && v.Error != "" })
	assert.Contains(t, view.Error, "hub")
	assert.NotEmpty(t, e.tui.Workspaces())
}

func TestStream_TheRouteNeedsADeviceAndTakesGetOnly(t *testing.T) {
	e := newStreamEnv(t)
	require.NotNil(t, e.layer.hub, "Start builds the hub with the layer's parts")
	assert.Contains(t, e.layer.sinks, sink(e.layer.hub), "the drain hands the hub every Sync's events")

	for name, cookie := range map[string]*http.Cookie{
		"no cookie":               nil,
		"a token no device holds": {Name: cookieName, Value: deviceToken(9)},
	} {
		resp := httpDo(t, e.addr, http.MethodGet, "/api/v1/stream", "", func(r *http.Request) {
			if cookie != nil {
				r.AddCookie(cookie)
			}
		})
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
	}

	post := httpDo(t, e.addr, http.MethodPost, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(e.cookie) })
	assert.Equal(t, http.StatusMethodNotAllowed, post.StatusCode)

	// The mux hands a HEAD to a GET pattern; it must not open a stream, which
	// would hold a slot and ping into a body nobody reads.
	head := httpDo(t, e.addr, http.MethodHead, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(e.cookie) })
	assert.Equal(t, http.StatusMethodNotAllowed, head.StatusCode)
	assert.Equal(t, http.MethodGet, head.Header.Get("Allow"))
	h := e.layer.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Empty(t, h.subs, "a HEAD holds no stream")
}

// deadlineRecorder is a response writer that supports write deadlines and
// records each one set.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadlines = append(d.deadlines, t)
	return nil
}

func (d *deadlineRecorder) seen() []time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.deadlines)
}

func TestStream_EachFrameIsWrittenUnderADeadlineThatIsClearedAfter(t *testing.T) {
	e := newStreamEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.layer.hub.serveStream(rec, req, "iPhone")
	}()
	waitUntil(t, "the snapshot is written", func() bool { return len(rec.seen()) >= 2 })
	cancel()
	<-done

	got := rec.seen()
	require.GreaterOrEqual(t, len(got), 2)
	assert.WithinDuration(t, time.Now().Add(streamWriteTimeout), got[0], 5*time.Second)
	assert.True(t, got[1].IsZero(), "the deadline is cleared after the write")
}

func TestStream_AWriterWithoutDeadlinesStillStreams(t *testing.T) {
	e := newStreamEnv(t)
	h := e.layer.hub
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder() // its controller answers ErrNotSupported
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serveStream(rec, req, "iPhone")
	}()
	waitUntil(t, "the handler takes the snapshot", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		for sub := range h.subs {
			return len(sub.out) == 0
		}
		return false
	})
	cancel()
	<-done
	assert.Contains(t, rec.Body.String(), "event: snapshot\n")
}

// endedHubWithAStream is the fixture's hub and a stream to be put in the set
// once the test ends the hub, to see what reaches it.
func endedHubWithAStream(t *testing.T) (*hub, *streamSub) {
	t.Helper()
	e := newStreamEnv(t)
	return e.layer.hub, &streamSub{device: "iPhone", out: make(chan sseFrame, streamQueue), done: make(chan struct{})}
}

func TestHub_AnEndedHubQueuesNothingFromApply(t *testing.T) {
	h, sub := endedHubWithAStream(t)
	require.True(t, h.end(false))
	// The flusher may leave a token queued from before the end when it
	// exits on stop; only a token apply queues is the test's to see.
	select {
	case <-h.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the flusher never exited")
	}
	select {
	case <-h.wake:
	default:
	}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	before := slices.Clone(h.fleet.workspaces)
	h.mu.Unlock()

	h.apply([]core.Event{
		core.WorkspacesChanged{Views: []core.WorkspaceView{{ID: 3, Label: "late"}}},
		core.Notice{Info: "late"},
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Empty(t, sub.out, "nothing is queued")
	assert.Equal(t, before, h.fleet.workspaces, "the fleet is not updated")
	assert.Empty(t, h.notices, "the notice is not kept")
	assert.Empty(t, h.wake, "the flusher is not woken")
}

func TestHub_AnEndedHubQueuesNothingFromFlush(t *testing.T) {
	h, sub := endedHubWithAStream(t)
	require.True(t, h.end(false))
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.fleet.setWorkspaces([]core.WorkspaceView{{ID: 3, Label: "late"}})
	before := slices.Clone(h.pubWorkspaces)
	h.mu.Unlock()

	h.flush()

	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Empty(t, sub.out, "nothing is queued")
	assert.Equal(t, before, h.pubWorkspaces, "nothing is recorded as published")
}
