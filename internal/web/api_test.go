package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apiFixture is what an API test's daemon holds.
type apiFixture struct {
	// insts are added to the workspace "toy", never started.
	insts []*session.Instance
	// auth, when set, is the default account's remote-control auth.
	auth *session.RemoteControlAuth
	// workspaceConfig is the workspace's config.json; "" is the Claude
	// program with every other setting at its default.
	workspaceConfig string
}

// apiEnv is a webStack over a model with the workspace "toy" and the
// fixture's sessions, on a loop that holds its jobs. The layer listens at
// addr, and cookie is the paired device "phone".
type apiEnv struct {
	*webStack
	t      *testing.T
	addr   string
	cookie *http.Cookie
	ws     core.WorkspaceID
	ids    map[string]core.InstanceID
}

func newAPIEnv(t *testing.T, f apiFixture) *apiEnv {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	def := config.Workspace{Name: "toy", Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	wsConfig := f.workspaceConfig
	if wsConfig == "" {
		wsConfig = `{"default_program":"claude"}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(wsConfig), 0o644))
	dead := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	m := core.New(core.Options{Registry: &config.WorkspaceRegistry{Workspaces: []config.Workspace{def}}, CmdExec: dead})
	m.Boot()
	if f.auth != nil {
		m.SetRCAuth(*f.auth)
	}
	var ws core.WorkspaceView
	for _, v := range m.Workspaces() {
		if v.Name == "toy" {
			ws = v
		}
	}
	require.NotZero(t, ws.ID, "the model serves the workspace")
	ids := map[string]core.InstanceID{}
	for _, inst := range f.insts {
		m.WorkspaceForTest(ws.ID).AddForTest(inst)
		ids[inst.Title] = m.IDOfForTest(inst)
	}

	// newWebStack's stack, but on a loop that holds its jobs and has not
	// begun (no tick).
	s := &webStack{loop: core.StartForTest(m)}
	s.srv = rpc.NewServer(s.loop)
	t.Cleanup(func() {
		s.layer.Close()
		if s.tui != nil {
			s.tui.Close()
		}
		s.srv.Close()
		s.loop.Stop()
	})
	_ = s.loop.JobsForTest() // what the boot queued is never run
	s.start(t)
	addr, _ := s.enable(t)
	cookie := s.pair(t, addr, "phone")
	require.Eventually(t, func() bool {
		if _, ok := s.layer.client.Workspace(ws.ID); !ok {
			return false
		}
		for _, id := range ids {
			if _, ok := s.layer.client.View(id); !ok {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "the web client never saw the model's state")
	return &apiEnv{webStack: s, t: t, addr: addr, cookie: cookie, ws: ws.ID, ids: ids}
}

// apiInstance is a fixture session in status st, never started.
func apiInstance(t *testing.T, title string, st session.Status) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(st))
	return inst
}

// send makes one request to the layer's listener (httpDo), carrying the
// paired device's cookie when asDevice is set, and returns the status, the
// body and the headers. A string body goes out as it is, any other as JSON.
func (e *apiEnv) send(method, path string, body any, asDevice bool) (int, []byte, http.Header) {
	e.t.Helper()
	var raw string
	switch b := body.(type) {
	case nil:
	case string:
		raw = b
	default:
		enc, err := json.Marshal(b)
		require.NoError(e.t, err)
		raw = string(enc)
	}
	var mutate []func(*http.Request)
	if asDevice {
		mutate = append(mutate, func(r *http.Request) { r.AddCookie(e.cookie) })
	}
	resp := httpDo(e.t, e.addr, method, path, raw, mutate...)
	return resp.StatusCode, []byte(bodyText(e.t, resp)), resp.Header
}

// do is send as the paired device.
func (e *apiEnv) do(method, path string, body any) (int, []byte) {
	e.t.Helper()
	status, out, _ := e.send(method, path, body, true)
	return status, out
}

// view is the session id as the web client's replica holds it.
func (e *apiEnv) view(id core.InstanceID) core.InstanceView {
	e.t.Helper()
	v, ok := e.layer.client.View(id)
	require.True(e.t, ok, "the replica holds session %d", id)
	return v
}

// apiErrorOf is the "error" of a response body.
func apiErrorOf(t *testing.T, body []byte) string {
	t.Helper()
	var v struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &v), "body: %s", body)
	return v.Error
}

func TestAPI_SessionRoutesNeedADevice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	id := env.ids["one"]

	for _, action := range []string{"send", "merge", "pause", "resume", "kill", "push"} {
		path := fmt.Sprintf("/api/v1/sessions/%d/%s", id, action)
		status, out, _ := env.send(http.MethodPost, path, map[string]any{"text": "hi", "source": 1}, false)
		assert.Equal(t, http.StatusUnauthorized, status, action)
		assert.NotEmpty(t, apiErrorOf(t, out), action)
	}
	assert.Equal(t, session.Running, env.view(id).Status, "no request reached the model")
}

func TestAPI_AnUnknownSessionIs404(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})

	for _, path := range []string{
		"/api/v1/sessions/999999/pause",
		"/api/v1/sessions/abc/kill",
		"/api/v1/sessions/0/resume",
		"/api/v1/sessions/-4/push",
		"/api/v1/sessions/9007199254740993/send", // 2^53 + 1: no JSON number holds it
	} {
		status, out := env.do(http.MethodPost, path, map[string]any{"text": "hi"})
		assert.Equal(t, http.StatusNotFound, status, path)
		assert.Equal(t, "no such session", apiErrorOf(t, out), path)
	}

	merge := fmt.Sprintf("/api/v1/sessions/%d/merge", env.ids["one"])
	status, out := env.do(http.MethodPost, merge, map[string]any{"source": 999999})
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "source: no such session", apiErrorOf(t, out))
	for _, body := range []any{map[string]any{}, map[string]any{"source": 0}, "not json"} {
		status, out = env.do(http.MethodPost, merge, body)
		assert.Equal(t, http.StatusBadRequest, status, "%v", body)
		assert.NotEmpty(t, apiErrorOf(t, out))
	}
}

func TestAPI_ARefusalIs409WithTheModelsText(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{
		apiInstance(t, "busy", session.Loading),
		apiInstance(t, "idle", session.Running),
	}})
	busy, idle := env.ids["busy"], env.ids["idle"]

	for _, c := range []struct {
		name string
		path string
		body any
		want string
	}{
		{"kill a busy session", fmt.Sprintf("/api/v1/sessions/%d/kill", busy), nil, "kill busy: the session is busy (Loading)"},
		{"pause a busy session", fmt.Sprintf("/api/v1/sessions/%d/pause", busy), nil, "pause busy: the session is busy (Loading)"},
		{"push a busy session", fmt.Sprintf("/api/v1/sessions/%d/push", busy), nil, "push busy: the session is busy (Loading)"},
		{"resume a session that is not paused", fmt.Sprintf("/api/v1/sessions/%d/resume", idle), nil, "resume idle: the session is not paused (Running)"},
		{"reply to a session that has not started", fmt.Sprintf("/api/v1/sessions/%d/send", idle), map[string]any{"text": "hi"}, "send a prompt to idle: the session has not started"},
		{"merge into a session that has not started", fmt.Sprintf("/api/v1/sessions/%d/merge", idle), map[string]any{"source": busy}, "merge into idle: the session has not started"},
	} {
		status, out := env.do(http.MethodPost, c.path, c.body)
		assert.Equal(t, http.StatusConflict, status, c.name)
		assert.Contains(t, apiErrorOf(t, out), c.want, c.name)
	}
	assert.Equal(t, session.Loading, env.view(busy).Status, "a refusal changes nothing")
	assert.Equal(t, session.Running, env.view(idle).Status)
}

func TestAPI_SendChecksTheReply(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	path := fmt.Sprintf("/api/v1/sessions/%d/send", env.ids["idle"])

	for _, c := range []struct {
		name string
		body any
	}{
		{"an empty reply", map[string]any{"text": ""}},
		{"a blank reply", map[string]any{"text": " \n\t "}},
		{"no text", map[string]any{}},
		{"a reply over 64 KiB", map[string]any{"text": strings.Repeat("a", 64<<10+1)}},
		{"a field it does not know", map[string]any{"text": "hi", "from": "me"}},
		{"a body that is not JSON", "not json"},
	} {
		status, out := env.do(http.MethodPost, path, c.body)
		assert.Equal(t, http.StatusBadRequest, status, c.name)
		assert.NotEmpty(t, apiErrorOf(t, out), c.name)
	}

	// 64 KiB exactly is the longest the model is asked to take: it refuses
	// this session in its own words, since it never started.
	status, out := env.do(http.MethodPost, path, map[string]any{"text": strings.Repeat("a", 64<<10)})
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "the session has not started")
}

func TestAPI_AStoppingDaemonIs503(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	id := env.ids["one"]
	env.srv.Bye()
	require.Eventually(t, env.layer.client.Stopping, 5*time.Second, 10*time.Millisecond, "the client never heard the bye")

	status, out := env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/pause", id), nil)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, apiErrorOf(t, out), "unavailable")
	assert.Equal(t, session.Running, env.view(id).Status, "the model never saw it")
}

func TestAPI_APendingRequestAnswers202AndItsOutcomeGoesToTheStream(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "slow", session.Running)}})
	env.layer.reqs.timeout = 100 * time.Millisecond
	late := make(chan lateNotice, 4)
	env.layer.reqs.setLate(func(device string, n noticeJSON) { late <- lateNotice{device: device, n: n} })

	// The loop holds the pause's job, so no Reply comes within the wait.
	status, out := env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/pause", env.ids["slow"]), nil)
	assert.Equal(t, http.StatusAccepted, status)
	assert.JSONEq(t, `{"pending": true}`, string(out))

	// The job finishes after the answer: an unstarted session can't be
	// paused, so the model raises the failure as a Notice and a Reply.
	jobs := env.loop.JobsForTest()
	require.Len(t, jobs, 1)
	env.loop.DeliverForTest(jobs[0]())
	env.layer.client.ReloadAccounts() // makes the server publish what the delivery produced
	select {
	case got := <-late:
		assert.Equal(t, "phone", got.device)
		assert.True(t, got.n.Error)
		assert.Contains(t, got.n.Text, "cannot pause instance that has not been started")
	case <-time.After(5 * time.Second):
		t.Fatal("the outcome never reached the device's stream")
	}
	select {
	case again := <-late:
		t.Fatalf("the failure came twice: %+v", again)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAPI_ModelStatus(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{core.ErrNoSession, http.StatusNotFound},
		{fmt.Errorf("pause x: %w", core.ErrNoSession), http.StatusNotFound},
		{core.ErrUnavailable, http.StatusServiceUnavailable},
		{fmt.Errorf("%w: connection lost", core.ErrUnavailable), http.StatusServiceUnavailable},
		{core.ErrWebOff, http.StatusServiceUnavailable},
		{core.ErrNoDevice, http.StatusUnauthorized},
		{fmt.Errorf("%w: %q", core.ErrNoDevice, "phone"), http.StatusUnauthorized},
		{errors.New("kill x: the session is busy (Loading)"), http.StatusConflict},
		{errors.New("push x: rejected"), http.StatusConflict},
	} {
		assert.Equal(t, c.want, modelStatus(c.err), c.err.Error())
	}
}
