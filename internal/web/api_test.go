package web

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
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
	// stateJSON, when set, is the workspace's state.json in place of an empty
	// one.
	stateJSON string
}

// recordingBackend is the loop as the server serves it, keeping what the web
// layer asked it to send.
type recordingBackend struct {
	*core.Loop
	mu   sync.Mutex
	sent []core.Message
}

func (b *recordingBackend) Send(id core.InstanceID, msg core.Message, req core.ReqID) {
	b.mu.Lock()
	b.sent = append(b.sent, msg)
	b.mu.Unlock()
	b.Loop.Send(id, msg, req)
}

// loggedAction is one action the layer recorded.
type loggedAction struct {
	device, name string
	session      core.InstanceID
	result       string
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
	rb     *recordingBackend

	actMu   sync.Mutex
	actions []loggedAction
}

// logged is the actions the layer has recorded so far.
func (e *apiEnv) logged() []loggedAction {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	return append([]loggedAction(nil), e.actions...)
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
	state := f.stateJSON
	if state == "" {
		state = `{"instances":[]}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(state), 0o644))
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
	rb := &recordingBackend{Loop: s.loop}
	s.srv = rpc.NewServer(rb)
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
	env := &apiEnv{webStack: s, t: t, ws: ws.ID, ids: ids, rb: rb}
	s.layer.onAction = func(a actionLog, result string) {
		env.actMu.Lock()
		defer env.actMu.Unlock()
		env.actions = append(env.actions, loggedAction{device: a.device, name: a.name, session: a.sessionID, result: result})
	}
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
	env.addr, env.cookie = addr, cookie
	return env
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

func TestAPI_CreateAndLaunchDefaultsNeedADevice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, _, _ := env.send(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "x"}, false)
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _, _ = env.send(http.MethodGet, fmt.Sprintf("/api/v1/launch-defaults?workspace=%d", env.ws), nil, false)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Empty(t, env.layer.client.Views(env.ws), "nothing was created")
}

func TestAPI_CreateStartsASessionWithTheChosenOptions(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{
		"workspace": env.ws, "title": " fresh ", "prompt": "hello\n\n",
		"model": "opus", "permission_mode": "plan", "effort": "high",
	})

	require.Equal(t, http.StatusCreated, status, string(out))
	var got struct {
		ID      core.InstanceID `json:"id"`
		Notices []noticeJSON    `json:"notices"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	assert.NotZero(t, got.ID)
	assert.NotNil(t, got.Notices, "a list, even an empty one")
	v := env.view(got.ID)
	assert.Equal(t, "fresh", v.Title, "the title is trimmed")
	assert.Equal(t, session.Loading, v.Status, "the start plays out on the stream")
	opts, _ := launch.Parse(v.Program)
	assert.Equal(t, "opus", opts.Model)
	assert.Equal(t, "plan", opts.PermissionMode)
	assert.Equal(t, "high", opts.Effort)
}

func TestAPI_CreateRefusesWhatLoomSessionNewRefuses(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "taken", session.Running)}})
	ws := env.ws

	for _, c := range []struct {
		name   string
		body   any
		status int
		want   string
	}{
		{"no workspace", map[string]any{"title": "x"}, http.StatusBadRequest, "workspace: no such workspace"},
		{"an unknown workspace", map[string]any{"workspace": 424242, "title": "x"}, http.StatusBadRequest, "workspace: no such workspace"},
		{"a blank title", map[string]any{"workspace": ws, "title": "  "}, http.StatusBadRequest, "title cannot be empty"},
		{"a title too wide", map[string]any{"workspace": ws, "title": strings.Repeat("w", core.MaxTitleWidth+1)}, http.StatusBadRequest, "title cannot be longer than"},
		{"a title the workspace holds", map[string]any{"workspace": ws, "title": "taken"}, http.StatusConflict, `already has a session titled "taken"`},
		{"a model Claude lacks", map[string]any{"workspace": ws, "title": "x", "model": "gpt"}, http.StatusBadRequest, `model "gpt": want one of`},
		{"a permission mode it lacks", map[string]any{"workspace": ws, "title": "x", "permission_mode": "yolo"}, http.StatusBadRequest, `permission_mode "yolo": want one of`},
		{"an effort it lacks", map[string]any{"workspace": ws, "title": "x", "effort": "extreme"}, http.StatusBadRequest, `effort "extreme": want one of`},
		{"an unregistered account", map[string]any{"workspace": ws, "title": "x", "account": "ghost"}, http.StatusBadRequest, `account "ghost" is not registered`},
		{"a prompt over 64 KiB", map[string]any{"workspace": ws, "title": "x", "prompt": strings.Repeat("p", 64<<10+1)}, http.StatusBadRequest, "prompt: want at most"},
		{"a field it does not know", map[string]any{"workspace": ws, "title": "x", "branch": "main"}, http.StatusBadRequest, "unknown field"},
	} {
		status, out := env.do(http.MethodPost, "/api/v1/sessions", c.body)
		assert.Equal(t, c.status, status, c.name)
		assert.Contains(t, apiErrorOf(t, out), c.want, c.name)
	}
	assert.Len(t, env.layer.client.Views(ws), 1, "only the session that was there")
}

func TestAPI_CreateFollowsTheWorkspacesProgram(t *testing.T) {
	env := newAPIEnv(t, apiFixture{workspaceConfig: `{"default_program":"aider"}`})

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "x", "account": "default"})

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "account: aider is not Claude", apiErrorOf(t, out))
	status, out = env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "x"})
	assert.Equal(t, http.StatusCreated, status, string(out))
}

func TestAPI_CreateRefusesWhenRemoteControlIsBlocked(t *testing.T) {
	blocked := &session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "logged in with an API key"}

	env := newAPIEnv(t, apiFixture{auth: blocked})
	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "rc"})
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "remote control is unavailable: logged in with an API key")
	assert.Empty(t, env.layer.client.Views(env.ws), "nothing was created")

	// With remote control off in the workspace, there is nothing to block.
	off := newAPIEnv(t, apiFixture{auth: blocked, workspaceConfig: `{"default_program":"claude","claude_remote_control":false}`})
	status, out = off.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": off.ws, "title": "rc"})
	assert.Equal(t, http.StatusCreated, status, string(out))
}

func TestAPI_CreateRefusesAFullWorkspace(t *testing.T) {
	var insts []*session.Instance
	for i := 0; i < core.InstanceLimit; i++ {
		insts = append(insts, apiInstance(t, fmt.Sprintf("s%d", i), session.Running))
	}
	env := newAPIEnv(t, apiFixture{insts: insts})

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "one more"})

	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "you can't create more than")
}

// apiDefaults is the body of GET /api/v1/launch-defaults.
type apiDefaults struct {
	Account         string   `json:"account"`
	Accounts        []string `json:"accounts"`
	Model           string   `json:"model"`
	Models          []string `json:"models"`
	PermissionMode  string   `json:"permission_mode"`
	PermissionModes []string `json:"permission_modes"`
	Effort          string   `json:"effort"`
	Efforts         []string `json:"efforts"`
}

// defaults reads the launch defaults of the fixture's workspace.
func (e *apiEnv) defaults() apiDefaults {
	e.t.Helper()
	status, out := e.do(http.MethodGet, fmt.Sprintf("/api/v1/launch-defaults?workspace=%d", e.ws), nil)
	require.Equal(e.t, http.StatusOK, status, string(out))
	var got apiDefaults
	require.NoError(e.t, json.Unmarshal(out, &got))
	return got
}

func TestAPI_LaunchDefaultsFollowTheWorkspace(t *testing.T) {
	env := newAPIEnv(t, apiFixture{workspaceConfig: `{"default_program":"claude","claude_model":"sonnet","claude_permission_mode":"plan","claude_effort":"high"}`})
	names := env.layer.client.AccountNames()
	wantAccounts, wantAccount := []string{}, ""
	if names.Present {
		wantAccounts, wantAccount = names.Names, names.Default
	}

	got := env.defaults()

	assert.Equal(t, "sonnet", got.Model)
	assert.Equal(t, "plan", got.PermissionMode)
	assert.Equal(t, "high", got.Effort)
	assert.Equal(t, wantAccount, got.Account)
	assert.NotNil(t, got.Accounts, "a list, even an empty one")
	assert.ElementsMatch(t, wantAccounts, got.Accounts)
	assert.Equal(t, config.ClaudeModelAliases(), got.Models, "the app hard-codes none of the lists")
	assert.Equal(t, config.ClaudePermissionModes, got.PermissionModes)
	assert.Equal(t, config.ClaudeEfforts, got.Efforts)
}

func TestAPI_LaunchDefaultsOfAProgramThatIsNotClaudeOfferNoAccounts(t *testing.T) {
	env := newAPIEnv(t, apiFixture{workspaceConfig: `{"default_program":"aider"}`})

	got := env.defaults()

	assert.Equal(t, "", got.Account)
	assert.NotNil(t, got.Accounts, "a list, even an empty one")
	assert.Empty(t, got.Accounts, "the app offers no account picker for it")
	assert.Equal(t, "default", got.Model)
	assert.Equal(t, "default", got.PermissionMode)
	assert.Equal(t, "default", got.Effort)
}

func TestAPI_LaunchDefaultsNeedAWorkspace(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	for _, query := range []string{"", "?workspace=abc", "?workspace=0", "?workspace=424242"} {
		status, out := env.do(http.MethodGet, "/api/v1/launch-defaults"+query, nil)
		assert.Equal(t, http.StatusBadRequest, status, query)
		assert.Equal(t, "workspace: no such workspace", apiErrorOf(t, out), query)
	}
}

// notify is the paired device's notification settings in the model.
func (e *apiEnv) notify() core.NotifySettings {
	e.t.Helper()
	for _, d := range e.loop.Web().Devices {
		if d.Name == "phone" {
			return d.Notify
		}
	}
	e.t.Fatal("the model holds no device named phone")
	return core.NotifySettings{}
}

func TestAPI_DeviceRoutesNeedADevice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/device"},
		{http.MethodPost, "/api/v1/device/push"},
		{http.MethodDelete, "/api/v1/device/push"},
		{http.MethodDelete, "/api/v1/device"},
	} {
		status, _, _ := env.send(c.method, c.path, map[string]any{}, false)
		assert.Equal(t, http.StatusUnauthorized, status, c.method+" "+c.path)
	}
	assert.Len(t, env.loop.Web().Devices, 1, "nobody was unpaired")
}

func TestAPI_PutDeviceChangesOnlyWhatItNames(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	before := env.notify()

	status, out := env.do(http.MethodPut, "/api/v1/device", map[string]any{"waiting": !before.Waiting})
	require.Equal(t, http.StatusOK, status, string(out))
	var dev struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(out, &dev))
	assert.Equal(t, "phone", dev.Name)
	after := env.notify()
	assert.Equal(t, !before.Waiting, after.Waiting)
	assert.Equal(t, before.Finished, after.Finished, "a setting left out stays")
	assert.True(t, after.PausedUntil.IsZero())

	// Pausing keeps the settings it does not name.
	start := time.Now()
	status, out = env.do(http.MethodPut, "/api/v1/device", map[string]any{"pause": "1h"})
	require.Equal(t, http.StatusOK, status, string(out))
	paused := env.notify()
	assert.Equal(t, !before.Waiting, paused.Waiting)
	assert.WithinDuration(t, start.Add(time.Hour), paused.PausedUntil, time.Minute)

	status, out = env.do(http.MethodPut, "/api/v1/device", map[string]any{"pause": "none"})
	require.Equal(t, http.StatusOK, status, string(out))
	assert.True(t, env.notify().PausedUntil.IsZero())
}

func TestAPI_PutDeviceRefusesWhatItCannotUse(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	before := env.notify()

	for _, c := range []struct {
		name string
		body any
		want string
	}{
		{"a pause it has no word for", map[string]any{"pause": "2h"}, `pause: want "1h" or "none"`},
		{"a setting that is not a boolean", map[string]any{"waiting": "yes"}, "bad request"},
		{"a field it does not know", map[string]any{"vibrate": true}, "unknown field"},
		{"a body that is not JSON", "not json", "bad request"},
	} {
		status, out := env.do(http.MethodPut, "/api/v1/device", c.body)
		assert.Equal(t, http.StatusBadRequest, status, c.name)
		assert.Contains(t, apiErrorOf(t, out), c.want, c.name)
	}
	assert.Equal(t, before, env.notify(), "a refused change changes nothing")
}

// apiSubscription is a push subscription body in the shape of the browser's
// PushSubscription.toJSON(), with a real P-256 point, its keys encoded by enc.
func apiSubscription(t *testing.T, endpoint string, enc *base64.Encoding) map[string]any {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return map[string]any{
		"endpoint":       endpoint,
		"expirationTime": nil,
		"keys": map[string]string{
			"p256dh": enc.EncodeToString(key.PublicKey().Bytes()),
			"auth":   enc.EncodeToString(auth),
		},
	}
}

func TestAPI_PostPushStoresTheSubscription(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	devicePush := func() core.PushState {
		for _, d := range env.loop.Web().Devices {
			if d.Name == "phone" {
				return d.Push
			}
		}
		return "no device"
	}
	require.Equal(t, core.PushNone, devicePush())
	const apple = "https://web.push.apple.com/push/1"

	// A host the daemon would not post to, and a key of the wrong size, are
	// refused before the model hears of them.
	status, out := env.do(http.MethodPost, "/api/v1/device/push", apiSubscription(t, "https://evil.example.com/push/1", base64.RawURLEncoding))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.NotEmpty(t, apiErrorOf(t, out))
	short := apiSubscription(t, apple, base64.RawURLEncoding)
	short["keys"] = map[string]string{"p256dh": "AAAA", "auth": "AAAA"}
	status, out = env.do(http.MethodPost, "/api/v1/device/push", short)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.NotEmpty(t, apiErrorOf(t, out))
	assert.Equal(t, core.PushNone, devicePush(), "refused: nothing stored")

	// Keys with their padding are taken: the model takes only the unpadded.
	status, out = env.do(http.MethodPost, "/api/v1/device/push", apiSubscription(t, apple, base64.URLEncoding))
	require.Equal(t, http.StatusOK, status, string(out))
	var dev struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(out, &dev))
	assert.Equal(t, "phone", dev.Name)
	assert.Equal(t, core.PushOK, devicePush())

	status, out = env.do(http.MethodPost, "/api/v1/device/push", apiSubscription(t, apple, base64.RawURLEncoding))
	assert.Equal(t, http.StatusOK, status, string(out))
}

func TestAPI_DeletePushDropsTheSubscription(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	devicePush := func() core.PushState {
		for _, d := range env.loop.Web().Devices {
			if d.Name == "phone" {
				return d.Push
			}
		}
		return "no device"
	}

	// With no subscription there is nothing to drop: the state stays none.
	status, out := env.do(http.MethodDelete, "/api/v1/device/push", nil)
	require.Equal(t, http.StatusNoContent, status, string(out))
	assert.Equal(t, core.PushNone, devicePush(), "nothing to expire")

	status, out = env.do(http.MethodPost, "/api/v1/device/push", apiSubscription(t, "https://web.push.apple.com/push/1", base64.RawURLEncoding))
	require.Equal(t, http.StatusOK, status, string(out))
	require.Equal(t, core.PushOK, devicePush())
	require.Len(t, env.loop.PushTargets(), 1, "control: the sender would push to it")

	status, out = env.do(http.MethodDelete, "/api/v1/device/push", nil)
	assert.Equal(t, http.StatusNoContent, status, string(out))
	assert.Equal(t, core.PushExpired, devicePush(), "the app shows notifications to turn on again")
	assert.Empty(t, env.loop.PushTargets(), "nothing is pushed to the dropped subscription")
	assert.Len(t, env.loop.Web().Devices, 1, "the phone stays paired")
}

func TestAPI_DeleteDeviceUnpairsThePhone(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, out, header := env.send(http.MethodDelete, "/api/v1/device", nil, true)

	assert.Equal(t, http.StatusNoContent, status, string(out))
	cleared := false
	for _, c := range (&http.Response{Header: header}).Cookies() {
		if c.Name == cookieName {
			cleared = true
			assert.Negative(t, c.MaxAge, "the cookie is expired on the phone")
		}
	}
	assert.True(t, cleared, "the response clears the device cookie")
	assert.Empty(t, env.loop.Web().Devices)
	status, _ = env.do(http.MethodPut, "/api/v1/device", map[string]any{"waiting": true})
	assert.Equal(t, http.StatusUnauthorized, status, "the token no longer names a device")
	status, _ = env.do(http.MethodDelete, "/api/v1/device", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "and unpairing twice is refused, not an error")
}

// apiAnswer is a response, read.
type apiAnswer struct {
	status int
	body   []byte
}

// async makes the request on a goroutine of its own and returns where its
// answer will come, so a test can play the loop's part while the handler
// waits.
func (e *apiEnv) async(method, path string, body any) <-chan apiAnswer {
	ch := make(chan apiAnswer, 1)
	go func() {
		status, out := e.do(method, path, body)
		ch <- apiAnswer{status, out}
	}()
	return ch
}

// heldJob waits for the loop to hold a job, the handler's request having
// reached it, and returns the job.
func (e *apiEnv) heldJob() core.Job {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if jobs := e.loop.JobsForTest(); len(jobs) > 0 {
			require.Len(e.t, jobs, 1)
			return jobs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("the request never reached the loop")
	return nil
}

// lastReq is the ID of the web client's latest request.
func (e *apiEnv) lastReq() core.ReqID {
	e.layer.reqs.mu.Lock()
	defer e.layer.reqs.mu.Unlock()
	return e.layer.reqs.last
}

// finish delivers a job's result as the loop would and wakes the server to
// publish what it produced.
func (e *apiEnv) finish(result any) {
	e.loop.DeliverForTest(result)
	e.layer.client.ReloadAccounts()
}

// answer waits for an async request's answer.
func (e *apiEnv) answer(ch <-chan apiAnswer) apiAnswer {
	e.t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(10 * time.Second):
		e.t.Fatal("the handler never answered")
		return apiAnswer{}
	}
}

// lateRecorder replaces where late notices go with a channel.
func (e *apiEnv) lateRecorder() <-chan lateNotice {
	late := make(chan lateNotice, 8)
	e.layer.reqs.setLate(func(device string, n noticeJSON) { late <- lateNotice{device: device, n: n} })
	return late
}

func TestAPI_AnOpThatWorksAnswers200WithItsNotices(t *testing.T) {
	for _, c := range []struct {
		action string
		result func(inst *session.Instance, req core.ReqID, id core.InstanceID) any
		want   []noticeJSON
	}{
		{"kill", func(inst *session.Instance, req core.ReqID, id core.InstanceID) any {
			return core.TrackedForTest(req, id, core.KillResult{Instance: inst, Title: inst.Title, Notice: errors.New("kept the stash abc123")})
		}, []noticeJSON{{Text: "kept the stash abc123", Error: true}}},
		{"pause", func(inst *session.Instance, req core.ReqID, id core.InstanceID) any {
			return core.TrackedForTest(req, id, core.PauseResult{Title: inst.Title})
		}, []noticeJSON{}},
	} {
		t.Run(c.action, func(t *testing.T) {
			inst := apiInstance(t, "one", session.Running)
			env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
			id := env.ids["one"]
			late := env.lateRecorder()

			ch := env.async(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/%s", id, c.action), nil)
			env.heldJob() // the request is at the loop; its job is never run
			env.finish(c.result(inst, env.lastReq(), id))
			got := env.answer(ch)

			require.Equal(t, http.StatusOK, got.status, string(got.body))
			var body struct {
				Notices []noticeJSON `json:"notices"`
			}
			require.NoError(t, json.Unmarshal(got.body, &body))
			require.NotNil(t, body.Notices, "a list, even an empty one")
			require.Len(t, body.Notices, len(c.want))
			for i, want := range c.want {
				assert.Equal(t, want.Text, body.Notices[i].Text)
				assert.Equal(t, want.Error, body.Notices[i].Error)
			}
			select {
			case n := <-late:
				t.Fatalf("a notice that rode the answer also went to the stream: %+v", n)
			case <-time.After(200 * time.Millisecond):
			}
			assert.Equal(t, []loggedAction{{device: "phone", name: c.action, session: id, result: "200 ok"}}, env.logged())
		})
	}
}

func TestAPI_AFailedOpAnswers409AndItsRepeatedNoticeStaysOffTheStream(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	id := env.ids["one"]
	late := env.lateRecorder()

	ch := env.async(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/pause", id), nil)
	job := env.heldJob()
	// An unstarted session can't be paused: the model raises the failure as
	// a Notice and as the Reply's error.
	env.finish(job())
	got := env.answer(ch)

	require.Equal(t, http.StatusConflict, got.status, string(got.body))
	text := apiErrorOf(t, got.body)
	assert.Contains(t, text, "cannot pause instance that has not been started")
	select {
	case n := <-late:
		t.Fatalf("the notice that repeats the error also went to the stream: %+v", n)
	case <-time.After(200 * time.Millisecond):
	}
	assert.Equal(t, []loggedAction{{device: "phone", name: "pause", session: id, result: "409 " + text}}, env.logged())
}

func TestAPI_SendCarriesTheWebOrigin(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})

	// The model refuses it (never started), but the call is what is pinned.
	status, _ := env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/send", env.ids["idle"]), map[string]any{"text": "  hi  "})

	assert.Equal(t, http.StatusConflict, status)
	env.rb.mu.Lock()
	defer env.rb.mu.Unlock()
	require.Len(t, env.rb.sent, 1)
	assert.Equal(t, core.Message{Text: "hi", Origin: core.OriginWeb}, env.rb.sent[0], "the person's own words, no From line")
}

func TestAPI_EveryAnswerIsLoggedWithItsStatus(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{
		apiInstance(t, "idle", session.Running),
		apiInstance(t, "taken", session.Running),
	}})
	idle := env.ids["idle"]
	send := fmt.Sprintf("/api/v1/sessions/%d/send", idle)
	merge := fmt.Sprintf("/api/v1/sessions/%d/merge", idle)

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, send, "not json"},
		{http.MethodPost, send, map[string]any{"text": " "}},
		{http.MethodPost, "/api/v1/sessions/999999/kill", nil},
		{http.MethodPost, merge, map[string]any{"source": 0}},
		{http.MethodPost, merge, map[string]any{"source": 999999}},
		{http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": " "}},
		{http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "taken"}},
		{http.MethodGet, "/api/v1/launch-defaults?workspace=0", nil},
		{http.MethodPut, "/api/v1/device", map[string]any{"pause": "2h"}},
		{http.MethodPut, "/api/v1/device", map[string]any{"waiting": false}},
		{http.MethodPost, "/api/v1/device/push", map[string]any{}},
		{http.MethodDelete, "/api/v1/device/push", nil},
	} {
		env.do(c.method, c.path, c.body)
	}

	got := env.logged()
	want := []struct {
		name, result string
		session      core.InstanceID
	}{
		{"send", "400 bad request: ", idle},
		{"send", "400 text: want 1 to 65536 bytes", idle},
		{"kill", "404 no such session", 0},
		{"merge", "400 source: want a session ID", idle},
		{"merge", "404 source: no such session", idle},
		{"create", "400 title cannot be empty", 0},
		{"create", `409 toy already has a session titled "taken"`, 0},
		{"launch-defaults", "400 workspace: no such workspace", 0},
		{"notify", `400 pause: want "1h" or "none"`, 0},
		{"notify", "200 ok", 0},
		{"subscribe", "400 ", 0},
		{"unsubscribe", "204 ok", 0},
	}
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, "phone", got[i].device, w.name)
		assert.Equal(t, w.name, got[i].name)
		assert.Equal(t, w.session, got[i].session, w.name)
		assert.True(t, strings.HasPrefix(got[i].result, w.result), "%s: %q does not begin %q", w.name, got[i].result, w.result)
	}
}

func TestAPI_ADeviceRouteFailureIsLoggedWithItsStatus(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	require.NoError(t, env.loop.RevokeDevice("phone")) // the model forgot it behind the layer's back

	// The cookie no longer names a device, so the route is not reached; call
	// the handler as authed would for a device that was there a moment ago.
	for _, c := range []struct {
		name   string
		call   func(w http.ResponseWriter, r *http.Request, device string)
		status int
	}{
		{"notify", env.layer.putDevice, http.StatusUnauthorized},
		{"subscribe", env.layer.postPush, http.StatusUnauthorized},
	} {
		body := `{"waiting": true}`
		if c.name == "subscribe" {
			enc, err := json.Marshal(apiSubscription(t, "https://web.push.apple.com/push/1", base64.RawURLEncoding))
			require.NoError(t, err)
			body = string(enc)
		}
		w := httptest.NewRecorder()
		c.call(w, httptest.NewRequest(http.MethodPut, "/api/v1/device", strings.NewReader(body)), "phone")
		assert.Equal(t, c.status, w.Code, c.name)
	}

	got := env.logged()
	require.Len(t, got, 2)
	for _, a := range got {
		assert.True(t, strings.HasPrefix(a.result, "401 "), "%s: %q holds the status in one format", a.name, a.result)
	}
}

func TestAPI_DeleteDeviceOfADeviceAlreadyGoneIs204(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	// A CLI revoke lands between authed and the handler: the model no longer
	// has the device, and tells the layer so over the wire, as text.
	require.NoError(t, env.loop.RevokeDevice("phone"))
	err := env.layer.client.RevokeDevice("phone")
	require.Error(t, err)
	require.NotErrorIs(t, err, core.ErrNoDevice, "control: the sentinel does not cross the wire")

	w := httptest.NewRecorder()
	env.layer.deleteDevice(w, httptest.NewRequest(http.MethodDelete, "/api/v1/device", nil), "phone")

	assert.Equal(t, http.StatusNoContent, w.Code, "a device already gone is a device unpaired")
	assert.Equal(t, []loggedAction{{device: "phone", name: "unpair", result: "204 ok"}}, env.logged())
}

func TestAPI_CreateTakesAPromptOf64KiBExactly(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "long", "prompt": strings.Repeat("p", 64<<10)})

	assert.Equal(t, http.StatusCreated, status, string(out))
}

func TestAPI_MergeRefusesASourceBeyondWhatJSONHolds(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	merge := fmt.Sprintf("/api/v1/sessions/%d/merge", env.ids["idle"])

	status, out := env.do(http.MethodPost, merge, `{"source": 9007199254740992}`) // 2^53
	assert.Equal(t, http.StatusBadRequest, status, string(out))
	assert.Equal(t, "source: want a session ID", apiErrorOf(t, out))

	// 2^53-1 is the largest ID the layer names; none holds it.
	status, out = env.do(http.MethodPost, merge, `{"source": 9007199254740991}`)
	assert.Equal(t, http.StatusNotFound, status, string(out))
	assert.Equal(t, "source: no such session", apiErrorOf(t, out))
}

func TestAPI_CreateInAWorkspaceWhoseStorageRefusesWritesIs409(t *testing.T) {
	env := newAPIEnv(t, apiFixture{stateJSON: `{"instances":{"not":"an array"}}`})
	v, ok := env.layer.client.Workspace(env.ws)
	require.True(t, ok)
	require.True(t, v.WritesRefused, "control: the fixture's storage refuses writes")

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "x"})

	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, apiErrorOf(t, out), "new sessions can't be created here")
}

func TestAPI_CreateTakesTheDefaultAccountOnAClaudeWorkspace(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, out := env.do(http.MethodPost, "/api/v1/sessions", map[string]any{"workspace": env.ws, "title": "x", "account": "default"})

	// "default" is no registered account, and still a name the layer takes.
	assert.Equal(t, http.StatusCreated, status, string(out))
}

// openStream connects the paired device's stream and reads its snapshot.
func (e *apiEnv) openStream() (*streamReader, snapshotJSON) {
	e.t.Helper()
	resp := httpDo(e.t, e.addr, http.MethodGet, "/api/v1/stream", "", func(r *http.Request) { r.AddCookie(e.cookie) })
	require.Equal(e.t, http.StatusOK, resp.StatusCode)
	s := readFrames(e.t, resp)
	return s, unframe[snapshotJSON](e.t, s.expect("snapshot"))
}

func TestAPI_AResponsesNoticesAreKeptForTheNextSnapshotAndNotSentLive(t *testing.T) {
	inst := apiInstance(t, "one", session.Running)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
	id := env.ids["one"]
	live, _ := env.openStream()

	ch := env.async(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/kill", id), nil)
	env.heldJob()
	env.finish(core.TrackedForTest(env.lastReq(), id, core.KillResult{Instance: inst, Title: inst.Title, Notice: errors.New("kept the stash abc123")}))
	got := env.answer(ch)
	require.Equal(t, http.StatusOK, got.status, string(got.body))

	_, snap := env.openStream()
	require.Len(t, snap.Notices, 1, "kept once, though the response carried it")
	assert.Contains(t, snap.Notices[0].Text, "kept the stash abc123")
	assert.True(t, snap.Notices[0].Error)
	live.none(200 * time.Millisecond) // the response showed it; the stream would show it twice
}

func TestAPI_ACreatesNoticesAreKeptToo(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	rec := httptest.NewRecorder()
	now := time.Now()

	env.layer.writeOutcome(rec, actionLog{device: "phone", name: "create"}, core.Reply{ID: 7},
		[]noticeJSON{{Text: "prompt not sent", At: now}}, true, true)

	require.Equal(t, http.StatusCreated, rec.Code)
	_, snap := env.openStream()
	require.Len(t, snap.Notices, 1)
	assert.Equal(t, "prompt not sent", snap.Notices[0].Text)
}

func TestAPI_ANoticeThatReachedTheStreamIsNotKeptAgainByTheResponse(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "slow", session.Running)}})
	env.layer.reqs.timeout = 100 * time.Millisecond

	// The wait runs out (202) and the failure, a notice and the Reply's
	// error, follows on the device's stream.
	status, _ := env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/pause", env.ids["slow"]), nil)
	require.Equal(t, http.StatusAccepted, status)
	jobs := env.loop.JobsForTest()
	require.Len(t, jobs, 1)
	env.loop.DeliverForTest(jobs[0]())
	env.layer.client.ReloadAccounts()
	require.Eventually(t, func() bool {
		env.layer.hub.mu.Lock()
		defer env.layer.hub.mu.Unlock()
		return len(env.layer.hub.notices) > 0
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	_, snap := env.openStream()
	require.Len(t, snap.Notices, 1, "the streamed failure is kept once")
	assert.Contains(t, snap.Notices[0].Text, "cannot pause instance that has not been started")
}

func TestAPI_AFailedOpsErrorIsNotAKeptNotice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	id := env.ids["one"]

	ch := env.async(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/pause", id), nil)
	env.finish(env.heldJob()())
	require.Equal(t, http.StatusConflict, env.answer(ch).status)

	_, snap := env.openStream()
	assert.Empty(t, snap.Notices, "the 409 carries the error; the repeated notice is dropped, not kept")
}

func TestAPI_KeptResponseNoticesKeepTheLastTwentyInOrder(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	env.layer.hub.publish("notice", noticeJSON{Text: "first", At: time.Now()})
	for i := range recentNotices + 3 {
		env.layer.writeOutcome(httptest.NewRecorder(), actionLog{device: "phone", name: "pause"}, core.Reply{},
			[]noticeJSON{{Text: fmt.Sprintf("r%d", i), At: time.Now()}}, true, false)
	}

	_, snap := env.openStream()

	require.Len(t, snap.Notices, recentNotices)
	assert.Equal(t, "r3", snap.Notices[0].Text)
	assert.Equal(t, fmt.Sprintf("r%d", recentNotices+2), snap.Notices[recentNotices-1].Text)
}
