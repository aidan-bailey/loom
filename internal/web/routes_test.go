package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoBody, echoQuery and echoOut are the types of the routes these tests
// declare for themselves.
type echoBody struct {
	Text string `json:"text"`
}

type echoQuery struct {
	Name  string `query:"name"`
	Other string `query:"other"`
}

type echoOut struct {
	Text string `json:"text"`
}

// newRouteLayer is a layer with no model behind it whose log lines the test
// reads from the returned slice.
func newRouteLayer() (*Layer, *[]loggedAction) {
	var got []loggedAction
	l := &Layer{now: time.Now}
	l.onAction = func(a actionLog, result string) {
		got = append(got, loggedAction{device: a.device, name: a.name, session: a.sessionID, result: result})
	}
	return l, &got
}

// serveRoute calls rt as the paired device "phone" with body.
func serveRoute(rt route, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	rt.serve(rec, req, "phone")
	return rec
}

func TestTyped_AnswersTheHandlersValueWithTheRoutesStatus(t *testing.T) {
	for _, tc := range []struct {
		ok   int
		want string
	}{
		{http.StatusOK, "200 ok"},
		{http.StatusCreated, "201 created"},
	} {
		l, got := newRouteLayer()
		rt := typed(l, spec{method: http.MethodGet, path: "/api/v1/echo", log: "echo", client: "echo", ok: tc.ok},
			func(c *call, _ struct{}) (echoOut, error) { return echoOut{Text: "hi"}, nil })

		rec := serveRoute(rt, http.MethodGet, "/api/v1/echo", "")

		assert.Equal(t, tc.ok, rec.Code)
		assert.JSONEq(t, `{"text":"hi"}`, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.Equal(t, []loggedAction{{device: "phone", name: "echo", result: tc.want}}, *got)
	}
}

func TestTyped_A204AnswersNoBody(t *testing.T) {
	l, got := newRouteLayer()
	rt := typed(l, spec{method: http.MethodDelete, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusNoContent},
		func(c *call, _ struct{}) (struct{}, error) { return struct{}{}, nil })

	rec := serveRoute(rt, http.MethodDelete, "/api/v1/echo", "")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, []loggedAction{{device: "phone", name: "echo", result: "204 ok"}}, *got)
}

func TestTyped_RecordsTheTypesTheGeneratorReads(t *testing.T) {
	l, _ := newRouteLayer()
	noop := func(c *call, _ struct{}) (echoOut, error) { return echoOut{}, nil }

	none := typed(l, spec{method: http.MethodPost, path: "/a", ok: http.StatusOK}, noop)
	assert.Nil(t, none.query, "a route that takes neither has no query type")
	assert.Nil(t, none.body, "and no body type")
	assert.Equal(t, reflect.TypeFor[echoOut](), none.resp)
	assert.False(t, none.page)

	query := typed(l, spec{method: http.MethodGet, path: "/b", ok: http.StatusOK},
		func(c *call, _ echoQuery) (echoOut, error) { return echoOut{}, nil })
	assert.Equal(t, reflect.TypeFor[echoQuery](), query.query, "a GET's In is its query")
	assert.Nil(t, query.body)

	body := typed(l, spec{method: http.MethodPut, path: "/c", ok: http.StatusOK},
		func(c *call, _ echoBody) (echoOut, error) { return echoOut{}, nil })
	assert.Equal(t, reflect.TypeFor[echoBody](), body.body, "any other method's In is its body")
	assert.Nil(t, body.query)

	gone := typed(l, spec{method: http.MethodDelete, path: "/d", ok: http.StatusNoContent},
		func(c *call, _ struct{}) (struct{}, error) { return struct{}{}, nil })
	assert.Nil(t, gone.resp, "a 204 has no response type")
}

func TestTyped_AnswersAnErrorWithTheStatusItEarnsAndLogsIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		text   string
		logged string
	}{
		{"a status error", &statusError{http.StatusBadRequest, "text: want 1 to 5 bytes"}, http.StatusBadRequest, "text: want 1 to 5 bytes", "400 text: want 1 to 5 bytes"},
		{"a device the model lost", fmt.Errorf("revoke: %w", core.ErrNoDevice), http.StatusUnauthorized, "revoke: " + core.ErrNoDevice.Error(), "401 revoke: " + core.ErrNoDevice.Error()},
		{"a daemon that is stopping", core.ErrUnavailable, http.StatusServiceUnavailable, core.ErrUnavailable.Error(), "503 " + core.ErrUnavailable.Error()},
		{"a refusal of the model", errors.New("cannot pause: not started"), http.StatusConflict, "cannot pause: not started", "409 cannot pause: not started"},
		{"a request still running", errPending, http.StatusAccepted, "", "202 pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, got := newRouteLayer()
			rt := typed(l, spec{method: http.MethodPost, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusOK},
				func(c *call, _ struct{}) (echoOut, error) { return echoOut{}, tc.err })

			rec := serveRoute(rt, http.MethodPost, "/api/v1/echo", "")

			assert.Equal(t, tc.status, rec.Code)
			if tc.status == http.StatusAccepted {
				assert.JSONEq(t, `{"pending":true}`, rec.Body.String())
			} else {
				assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tc.text), rec.Body.String())
			}
			assert.Equal(t, []loggedAction{{device: "phone", name: "echo", result: tc.logged}}, *got)
		})
	}
}

func TestTyped_ARouteThatLogsForItselfIsAnsweredButNotLogged(t *testing.T) {
	l, got := newRouteLayer()
	fail := false
	rt := typed(l, spec{method: http.MethodGet, path: "/api/v1/echo", client: "echo", ok: http.StatusOK},
		func(c *call, _ struct{}) (echoOut, error) {
			if fail {
				return echoOut{}, &statusError{http.StatusServiceUnavailable, "push is not available"}
			}
			return echoOut{Text: "hi"}, nil
		})

	assert.Equal(t, http.StatusOK, serveRoute(rt, http.MethodGet, "/api/v1/echo", "").Code)
	fail = true
	rec := serveRoute(rt, http.MethodGet, "/api/v1/echo", "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"error":"push is not available"}`, rec.Body.String())
	assert.Empty(t, *got, "an empty log name means the handler logs for itself")
}

func TestTyped_ABodyThatDoesNotDecodeIs400AndNeverReachesTheHandler(t *testing.T) {
	for _, tc := range []struct{ name, body, text string }{
		{"not JSON", "not json", "bad request: the body is not the JSON this route takes"},
		{"empty", "", "bad request: the body is empty"},
		{"a field the route does not take", `{"text":"x","extra":1}`, "bad request: the body is not the JSON this route takes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, got := newRouteLayer()
			called := false
			rt := typed(l, spec{method: http.MethodPost, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusOK},
				func(c *call, in echoBody) (echoOut, error) {
					called = true
					return echoOut{Text: in.Text}, nil
				})

			rec := serveRoute(rt, http.MethodPost, "/api/v1/echo", tc.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.text)
			assert.False(t, called)
			require.Len(t, *got, 1)
			assert.True(t, strings.HasPrefix((*got)[0].result, "400 bad request: "), (*got)[0].result)
		})
	}
}

func TestTyped_ABodyThatDecodesReachesTheHandlerAndAGETReadsItsQuery(t *testing.T) {
	l, _ := newRouteLayer()
	post := typed(l, spec{method: http.MethodPost, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusOK},
		func(c *call, in echoBody) (echoOut, error) { return echoOut{Text: in.Text}, nil })
	rec := serveRoute(post, http.MethodPost, "/api/v1/echo", `{"text":"hello"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"text":"hello"}`, rec.Body.String())

	get := typed(l, spec{method: http.MethodGet, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusOK},
		func(c *call, in echoQuery) (echoOut, error) { return echoOut{Text: in.Name + "/" + in.Other}, nil })
	rec = serveRoute(get, http.MethodGet, "/api/v1/echo?name=a&other=b&extra=z", "")
	assert.JSONEq(t, `{"text":"a/b"}`, rec.Body.String())
}

func TestTyped_ARouteThatTakesNeitherDecodesNothing(t *testing.T) {
	l, _ := newRouteLayer()
	rt := typed(l, spec{method: http.MethodPost, path: "/api/v1/echo", log: "echo", client: "echo", ok: http.StatusOK},
		func(c *call, _ struct{}) (echoOut, error) { return echoOut{Text: "ran"}, nil })

	rec := serveRoute(rt, http.MethodPost, "/api/v1/echo", "garbage that is no JSON")

	assert.Equal(t, http.StatusOK, rec.Code, "a body is not read for a route that takes none")
	assert.JSONEq(t, `{"text":"ran"}`, rec.Body.String())
}

func TestTyped_AnIDPathIsResolvedBeforeItsBodyIsDecoded(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	idle := env.ids["idle"]
	ran := 0
	rt := typed(env.layer, spec{method: http.MethodPost, path: "/api/v1/sessions/{id}/echo", log: "echo", client: "echo", ok: http.StatusOK},
		func(c *call, in echoBody) (echoOut, error) {
			ran++
			return echoOut{Text: in.Text}, nil
		})
	serve := func(id, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/echo", strings.NewReader(body))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		rt.serve(rec, req, "phone")
		return rec
	}

	rec := serve("999999", "not json")
	assert.Equal(t, http.StatusNotFound, rec.Code, "the session's 404 comes before the body's 400")
	assert.JSONEq(t, `{"error":"no such session"}`, rec.Body.String())

	rec = serve(strconv.FormatUint(uint64(idle), 10), "not json")
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	rec = serve(strconv.FormatUint(uint64(idle), 10), `{"text":"hi"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, ran, "the handler ran for the one request that named a session and sent a body")

	got := env.logged()
	require.Len(t, got, 3)
	assert.Equal(t, loggedAction{device: "phone", name: "echo", session: 0, result: "404 no such session"}, got[0])
	assert.Equal(t, idle, got[1].session, "the 400 is logged with the session")
	assert.True(t, strings.HasPrefix(got[1].result, "400 bad request: "), got[1].result)
	assert.Equal(t, loggedAction{device: "phone", name: "echo", session: idle, result: "200 ok"}, got[2])
}

func TestPage_MountsItsHandlerAsItIsAndTakesNoTypes(t *testing.T) {
	l, _ := newRouteLayer()
	var gotDevice string
	rt := page(l, spec{method: http.MethodGet, path: "/api/v1/page", log: "page", client: "page", ok: http.StatusOK},
		func(w http.ResponseWriter, r *http.Request, device string) {
			gotDevice = device
			_, _ = w.Write([]byte("<p>"))
		})

	rec := serveRoute(rt, http.MethodGet, "/api/v1/page", "")

	assert.True(t, rt.page)
	assert.Nil(t, rt.resp)
	assert.Nil(t, rt.query)
	assert.Nil(t, rt.body)
	assert.Equal(t, "phone", gotDevice)
	assert.Equal(t, "<p>", rec.Body.String(), "nothing wraps the handler's own answer")
}

func TestReadQuery_SetsEachTaggedStringAndIgnoresTheRest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?name=a%20b&other=&unknown=z", nil)
	q := echoQuery{Other: "stale"}

	readQuery(r, &q)

	assert.Equal(t, echoQuery{Name: "a b", Other: ""}, q, "a parameter that is present sets its field, an absent or empty one clears it")

	var untouched struct {
		Name string `query:"name"`
		Free string
	}
	untouched.Free = "kept"
	readQuery(r, &untouched)
	assert.Equal(t, "a b", untouched.Name)
	assert.Equal(t, "kept", untouched.Free, "a field with no query tag is not set")
}

func TestStatusError_IsAnErrorWithItsText(t *testing.T) {
	var err error = &statusError{http.StatusNotFound, "no such session"}

	assert.EqualError(t, err, "no such session")
	var se *statusError
	require.ErrorAs(t, fmt.Errorf("wrapped: %w", err), &se)
	assert.Equal(t, http.StatusNotFound, se.status)
}

func TestCall_SessionIsResolvedOnceAndAnswersTheSameTwice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	id := env.ids["one"]
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.SetPathValue("id", strconv.FormatUint(uint64(id), 10))
	c := &call{l: env.layer, r: req}

	got, err := c.session()
	require.NoError(t, err)
	assert.Equal(t, id, got)
	assert.Equal(t, id, c.a.sessionID, "the log line names the session")

	req.SetPathValue("id", "999999")
	again, err := c.session()
	require.NoError(t, err)
	assert.Equal(t, id, again, "a second call returns the first one's answer")
}

func TestCall_SessionRefusesAPathThatNamesNoSessionWith404(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})
	for _, bad := range []string{"999999", "0", "x", "-1", "9007199254740992", ""} {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.SetPathValue("id", bad)
		c := &call{l: env.layer, r: req}

		_, err := c.session()

		var se *statusError
		require.ErrorAs(t, err, &se, "id %q", bad)
		assert.Equal(t, http.StatusNotFound, se.status, bad)
		assert.Equal(t, "no such session", se.msg, bad)
		assert.Zero(t, c.a.sessionID, bad)
		_, again := c.session()
		assert.Equal(t, err, again, "a refusal is answered the same the second time: %q", bad)
	}
}

func TestCall_SetActionReplacesTheLogLinesAction(t *testing.T) {
	c := &call{a: actionLog{device: "phone", name: "job_control"}}

	c.setAction("job_park")

	assert.Equal(t, "job_park", c.a.name)
	assert.Equal(t, "phone", c.a.device)
}

func TestCall_NoticeCollectsTheNoticesRaisedBeforeTheRequestInOrder(t *testing.T) {
	c := &call{}

	c.notice(noticeJSON{Text: "first"})
	c.notice(noticeJSON{Text: "second"})

	require.Len(t, c.pre, 2)
	assert.Equal(t, "first", c.pre[0].Text)
	assert.Equal(t, "second", c.pre[1].Text)
}

// replyWith is a send that answers its request at once with the events evs
// makes, as the drain would, so the wait that follows returns.
func replyWith(l *Layer, evs func(req core.ReqID) []core.Event) func(core.ReqID) {
	return func(req core.ReqID) { l.reqs.apply(evs(req)) }
}

// newActCall is a call as the adapter makes one for the device "phone".
func newActCall(env *apiEnv) *call {
	return &call{l: env.layer, r: httptest.NewRequest(http.MethodPost, "/x", nil), device: "phone", a: actionLog{device: "phone", name: "pause"}}
}

// lateTexts records what goes to the device's stream, and returns the
// recorded texts.
func lateTexts(env *apiEnv) func() []string {
	var texts []string
	env.layer.reqs.setLate(func(device string, n noticeJSON) { texts = append(texts, device+": "+n.Text) })
	return func() []string { return texts }
}

func TestSettle_AWaitThatRanOutIsPendingAndStreamsTheNoticesItWasGiven(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	late := lateTexts(env)

	notes, err := env.layer.settle("phone", core.Reply{}, []noticeJSON{{Text: "x", At: time.Now()}}, false)

	require.ErrorIs(t, err, errPending)
	assert.Nil(t, notes)
	_, snap := env.openStream()
	assert.Empty(t, snap.Notices, "the notices of a request still running are not among the hub's recent ones: they follow on the stream")
	assert.Equal(t, []string{"phone: x"}, late(), "the notices raised before a request that goes pending are not lost: they go to the device's stream")
}

func TestSettle_AnOutcomeThatWorkedKeepsItsNoticesAndNeverReturnsNil(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	none, err := env.layer.settle("phone", core.Reply{}, nil, true)
	require.NoError(t, err)
	assert.Equal(t, []noticeJSON{}, none, "an empty list, not null, so the body is {\"notices\":[]}")

	notes, err := env.layer.settle("phone", core.Reply{}, []noticeJSON{{Text: "kept the stash abc123", At: time.Now()}}, true)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	_, snap := env.openStream()
	require.Len(t, snap.Notices, 1, "kept among the recent notices the snapshot carries")
	assert.Equal(t, "kept the stash abc123", snap.Notices[0].Text)
}

func TestSettle_ARefusalIsTheRepliesErrorAfterTheOtherNoticesGoToTheStream(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	late := lateTexts(env)
	refusal := errors.New("cannot pause: not started")

	notes, err := env.layer.settle("phone", core.Reply{Err: refusal},
		[]noticeJSON{{Text: "stash kept"}, {Text: refusal.Error()}}, true)

	require.ErrorIs(t, err, refusal)
	assert.Nil(t, notes)
	assert.Equal(t, []string{"phone: stash kept"}, late(), "the notice that repeats the error is dropped, the others go to the device")
	_, snap := env.openStream()
	assert.Empty(t, snap.Notices, "a refusal's notices are not kept")
}

func TestCall_ActListsTheNoticesRaisedBeforeTheRequestFirstAndKeepsThemAll(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	c := newActCall(env)
	c.notice(noticeJSON{Text: "moved off a removed account", At: time.Now()})

	out, err := c.act(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Notice{Req: req, Info: "stash kept"}, core.Reply{Req: req}}
	}))

	require.NoError(t, err)
	require.Len(t, out.Notices, 2)
	assert.Equal(t, "moved off a removed account", out.Notices[0].Text)
	assert.Equal(t, "stash kept", out.Notices[1].Text)
	_, snap := env.openStream()
	require.Len(t, snap.Notices, 2, "the notice raised before is kept with the Reply's")
}

func TestCall_ActWithNoNoticesAnswersAnEmptyList(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	out, err := newActCall(env).act(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Reply{Req: req}}
	}))

	require.NoError(t, err)
	assert.Equal(t, noticesJSON{Notices: []noticeJSON{}}, out)
}

func TestCall_ActStreamsTheNoticesOfARefusalWithTheOnesRaisedBeforeAndReturnsItsError(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	late := lateTexts(env)
	c := newActCall(env)
	c.notice(noticeJSON{Text: "moved off a removed account"})
	refusal := errors.New("cannot pause: not started")

	_, err := c.act(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Notice{Req: req, Info: "stash kept"}, core.Reply{Req: req, Err: refusal}}
	}))

	require.ErrorIs(t, err, refusal)
	assert.Equal(t, []string{"phone: moved off a removed account", "phone: stash kept"}, late())
}

func TestCall_ActIsPendingWhenTheWaitRunsOut(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	env.layer.reqs.timeout = 50 * time.Millisecond

	_, err := newActCall(env).act(func(core.ReqID) {})

	assert.ErrorIs(t, err, errPending)
}

func TestCall_ActCreatedAnswersTheNewSessionsIDAndLogsIt(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	c := newActCall(env)

	out, err := c.actCreated(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Reply{Req: req, ID: 7}}
	}))

	require.NoError(t, err)
	assert.Equal(t, createdJSON{ID: 7, Notices: []noticeJSON{}}, out)
	assert.Equal(t, core.InstanceID(7), c.a.sessionID, "the log line names the session the create made")

	_, err = newActCall(env).actCreated(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Reply{Req: req, Err: errors.New("no room")}}
	}))
	assert.EqualError(t, err, "no room")
}

func TestCall_ReadAnswersTheReplyAndStopsTrackingTheRequest(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	tracked := func() int {
		env.layer.reqs.mu.Lock()
		defer env.layer.reqs.mu.Unlock()
		return len(env.layer.reqs.entries)
	}
	before := tracked()

	reply, err := newActCall(env).read(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Reply{Req: req, ID: 5}}
	}))

	require.NoError(t, err)
	assert.Equal(t, core.InstanceID(5), reply.ID)
	assert.Equal(t, before, tracked(), "a read's Reply carries its data, so the request is dropped once answered")
}

func TestCall_ReadReturnsTheModelsErrorAndA504WhenTheWaitRunsOut(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	refusal := errors.New("no such job")

	_, err := newActCall(env).read(replyWith(env.layer, func(req core.ReqID) []core.Event {
		return []core.Event{core.Reply{Req: req, Err: refusal}}
	}))
	assert.Equal(t, refusal, err, "the model's own error, for the adapter to map to 409")

	env.layer.reqs.timeout = 50 * time.Millisecond
	_, err = newActCall(env).read(func(core.ReqID) {})
	var se *statusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusGatewayTimeout, se.status)
	assert.Equal(t, "loom did not answer in time", se.msg)
}

// intQuery and untaggedQuery are query types readQuery cannot fill.
type intQuery struct {
	N int `query:"n"`
}

type untaggedQuery struct {
	S string
}

// concretePath is a route's path with a value in each of its path values.
func concretePath(path string) string {
	return strings.NewReplacer("{id}", "1", "{ask}", "a", "{job}", "1", "{action}", "pause").Replace(path)
}

func TestRoutes_OnlyPairIsOpen(t *testing.T) {
	var open []string
	for _, rt := range (&Layer{}).routes() {
		if rt.spec.open {
			open = append(open, rt.spec.method+" "+rt.spec.path)
		}
	}
	assert.Equal(t, []string{"POST /api/v1/pair"}, open, "an open route is served to anyone: the pairing route, and only it, must be")
}

func TestRoutes_EveryClosedRouteRefusesAnUnpairedDevice(t *testing.T) {
	host := &scriptedHost{}
	l := &Layer{host: host}
	m := http.NewServeMux()
	l.registerRoutes(m)
	cookies := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"a token no device holds", &http.Cookie{Name: cookieName, Value: deviceToken(9)}},
	}
	closed := 0
	for _, rt := range l.routes() {
		if rt.spec.open {
			continue
		}
		closed++
		for _, tc := range cookies {
			req := httptest.NewRequest(rt.spec.method, concretePath(rt.spec.path), strings.NewReader(`{}`))
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			rec := httptest.NewRecorder()

			// The layer has no client, so a route that is not behind authed
			// panics on it: report that as this route's failure, not as the
			// end of the test binary.
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("%s %s with %s ran without a paired device and panicked: %v", rt.spec.method, rt.spec.path, tc.name, p)
					}
				}()
				m.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s with %s", rt.spec.method, rt.spec.path, tc.name)
			}()
		}
	}
	assert.NotZero(t, closed)
	assert.Empty(t, host.touched, "no device was touched: no route ran")
}

// queryTypeProblem says what is wrong with t as a route's query type, "" when
// nothing is: readQuery fills only string fields that carry a query tag.
func queryTypeProblem(t reflect.Type) string {
	if t.Kind() != reflect.Struct {
		return "not a struct"
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Type.Kind() != reflect.String {
			return f.Name + " is not a string"
		}
		if f.Tag.Get("query") == "" {
			return f.Name + " has no query tag"
		}
	}
	return ""
}

func TestRoutes_QueryTypesHoldOnlyStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"strings with tags", reflect.TypeFor[echoQuery](), ""},
		{"an int", reflect.TypeFor[intQuery](), "N is not a string"},
		{"no tag", reflect.TypeFor[untaggedQuery](), "S has no query tag"},
		{"a pointer", reflect.TypeFor[*echoQuery](), "not a struct"},
	} {
		assert.Equal(t, tc.want, queryTypeProblem(tc.typ), tc.name)
	}

	for _, rt := range (&Layer{}).routes() {
		if rt.query != nil {
			assert.Empty(t, queryTypeProblem(rt.query), "%s %s", rt.spec.method, rt.spec.path)
		}
	}
}

// clientProblems are the faults in the routes' client names: empty, not
// lowerCamelCase, or used twice.
func clientProblems(rs []route) []string {
	var out []string
	seen := map[string]bool{}
	for _, rt := range rs {
		c := rt.spec.client
		switch {
		case c == "":
			out = append(out, rt.spec.method+" "+rt.spec.path+": no client name")
		case c[:1] != strings.ToLower(c[:1]):
			out = append(out, c+": not lowerCamelCase")
		case seen[c]:
			out = append(out, c+": used twice")
		}
		seen[c] = true
	}
	return out
}

func TestRoutes_ClientNamesAreUnique(t *testing.T) {
	named := func(names ...string) []route {
		var rs []route
		for _, n := range names {
			rs = append(rs, route{spec: spec{method: http.MethodGet, path: "/" + n, client: n}})
		}
		return rs
	}
	assert.Empty(t, clientProblems(named("send", "merge")))
	assert.Equal(t, []string{"GET /: no client name"}, clientProblems(named("")))
	assert.Equal(t, []string{"Send: not lowerCamelCase"}, clientProblems(named("Send")))
	assert.Equal(t, []string{"send: used twice"}, clientProblems(named("send", "send")))

	assert.Empty(t, clientProblems((&Layer{}).routes()))
}

func TestRoutes_EveryRouteHasADoc(t *testing.T) {
	for _, rt := range (&Layer{}).routes() {
		key := rt.spec.method + " " + rt.spec.path
		assert.NotEmpty(t, rt.spec.doc, key)
		assert.True(t, strings.HasSuffix(rt.spec.doc, "."), "%s: a doc is sentences: %q", key, rt.spec.doc)
		assert.LessOrEqual(t, len(rt.spec.doc), 700, "%s: a doc is one to three sentences", key)
	}
}

// tableRow is what one route of the table must declare.
type tableRow struct {
	method string
	path   string
	client string
	ok     int
	query  reflect.Type
	body   reflect.Type
	resp   reflect.Type
}

// routeOf is the table's entry for method and path.
func routeOf(t *testing.T, l *Layer, method, path string) route {
	t.Helper()
	for _, rt := range l.routes() {
		if rt.spec.method == method && rt.spec.path == path {
			return rt
		}
	}
	require.FailNow(t, "the table has no such route", "%s %s", method, path)
	return route{}
}

// requireRows checks each row against the table.
func requireRows(t *testing.T, rows []tableRow) {
	t.Helper()
	l := &Layer{}
	for _, row := range rows {
		key := row.method + " " + row.path
		rt := routeOf(t, l, row.method, row.path)
		assert.Equal(t, row.client, rt.spec.client, key)
		assert.Equal(t, row.ok, rt.spec.ok, key)
		assert.Equal(t, row.query, rt.query, "%s: its query type", key)
		assert.Equal(t, row.body, rt.body, "%s: its body type", key)
		assert.Equal(t, row.resp, rt.resp, "%s: its response type", key)
		assert.False(t, rt.page, key)
	}
}

func TestRoutes_TheReadRoutesAreOnTheTable(t *testing.T) {
	requireRows(t, []tableRow{
		{http.MethodGet, "/api/v1/sessions/{id}/message", "message", http.StatusOK, nil, nil, reflect.TypeFor[messageJSON]()},
		{http.MethodGet, "/api/v1/sessions/{id}/turns", "turns", http.StatusOK, reflect.TypeFor[turnsParams](), nil, reflect.TypeFor[turnsJSON]()},
		{http.MethodGet, "/api/v1/sessions/{id}/diff", "diff", http.StatusOK, nil, nil, reflect.TypeFor[patchJSON]()},
		{http.MethodGet, "/api/v1/sessions/{id}/jobs/{job}/log", "jobLog", http.StatusOK, reflect.TypeFor[jobLogParams](), nil, reflect.TypeFor[jobLogJSON]()},
		{http.MethodGet, "/api/v1/sessions/{id}/flow", "flow", http.StatusOK, reflect.TypeFor[flowParams](), nil, reflect.TypeFor[flowJSON]()},
		{http.MethodGet, "/api/v1/sessions/{id}/flow/doc", "flowDoc", http.StatusOK, reflect.TypeFor[flowDocParams](), nil, reflect.TypeFor[flowDocJSON]()},
		{http.MethodGet, "/api/v1/flows", "flows", http.StatusOK, nil, nil, reflect.TypeFor[flowsJSON]()},
	})
}

func TestAPI_JobLogAndFlowReadsOfAWorkspaceTerminalAre404(t *testing.T) {
	term, err := session.NewInstance(session.InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{term}})
	id := env.ids["term"]

	for _, path := range []string{
		fmt.Sprintf("/api/v1/sessions/%d/jobs/7/log", id),
		fmt.Sprintf("/api/v1/sessions/%d/flow?plan=p.md", id),
		fmt.Sprintf("/api/v1/sessions/%d/flow/doc?plan=p.md&doc=spec", id),
	} {
		status, out := env.do(http.MethodGet, path, nil)
		assert.Equal(t, http.StatusNotFound, status, path)
		assert.Equal(t, "no such session", apiErrorOf(t, out), path)
	}
}

func TestRoutes_TheActionRoutesAreOnTheTable(t *testing.T) {
	requireRows(t, []tableRow{
		{http.MethodPost, "/api/v1/sessions/{id}/asks/{ask}/answer", "answer", http.StatusOK, nil, reflect.TypeFor[answerBody](), reflect.TypeFor[noticesJSON]()},
		{http.MethodPost, "/api/v1/sessions/{id}/jobs/{job}/{action}", "jobControl", http.StatusOK, nil, nil, reflect.TypeFor[noticesJSON]()},
		{http.MethodPost, "/api/v1/sessions", "create", http.StatusCreated, nil, reflect.TypeFor[createBody](), reflect.TypeFor[createdJSON]()},
		{http.MethodGet, "/api/v1/launch-defaults", "launchDefaults", http.StatusOK, reflect.TypeFor[launchDefaultsParams](), nil, reflect.TypeFor[launchDefaultsJSON]()},
	})
}

func TestAPI_AnswerWithAnOversizedAskAndABadBodyGetsTheDecodes400(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "idle", session.Running)}})
	long := fmt.Sprintf("/api/v1/sessions/%d/asks/%s/answer", env.ids["idle"], strings.Repeat("a", 129))

	status, out := env.do(http.MethodPost, long, "not json")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, apiErrorOf(t, out), "bad request: ", "the adapter decodes the body before the handler looks at the ask")

	status, out = env.do(http.MethodPost, long, map[string]any{"allow": true})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "ask: want an ID of 1 to 128 bytes", apiErrorOf(t, out), "with a good body the ask's own check answers")
}

func TestAPI_JobControlOfAnUnknownSessionAndAnUnknownActionIsTheSessions404(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready)}})

	status, out := env.do(http.MethodPost, "/api/v1/sessions/999999/jobs/7/nap", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "no such session", apiErrorOf(t, out), "the adapter resolves {id} before the handler checks the action")

	status, out = env.do(http.MethodPost, "/api/v1/sessions/999999/jobs/7/park", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "no such session", apiErrorOf(t, out), "a valid action on an unknown session")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "job_control", result: "404 no such session"}, "the handler never ran, so the line keeps the route's name, not job_park")

	status, out = env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/jobs/7/nap", env.ids["fresh"]), nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "no such action", apiErrorOf(t, out), "a known session and an unknown action")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "job_control", session: env.ids["fresh"], result: "404 no such action"})
}

func TestAPI_JobControlLogsItsActionByName(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready)}})

	status, _ := env.do(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/jobs/7/park", env.ids["fresh"]), nil)

	assert.Equal(t, http.StatusConflict, status, "the model has no job 7")
	got := env.logged()
	require.Len(t, got, 1)
	assert.Equal(t, "job_park", got[0].name, "the log line names the action the path chose")
	assert.Equal(t, env.ids["fresh"], got[0].session)
}

// routeNamed is the table's entry whose client name is client.
func routeNamed(t *testing.T, l *Layer, client string) route {
	t.Helper()
	for _, rt := range l.routes() {
		if rt.spec.client == client {
			return rt
		}
	}
	require.FailNow(t, "no route of the table has this client name", client)
	return route{}
}

func TestRoutes_TheDeviceRoutesAreOnTheTable(t *testing.T) {
	requireRows(t, []tableRow{
		{http.MethodPut, "/api/v1/device", "putDevice", http.StatusOK, nil, reflect.TypeFor[deviceBody](), reflect.TypeFor[deviceJSON]()},
		{http.MethodPut, "/api/v1/quick-replies", "putQuickReplies", http.StatusOK, nil, reflect.TypeFor[quickRepliesBody](), reflect.TypeFor[quickRepliesJSON]()},
		{http.MethodPost, "/api/v1/device/push", "postPush", http.StatusOK, nil, reflect.TypeFor[subscriptionBody](), reflect.TypeFor[deviceJSON]()},
		{http.MethodDelete, "/api/v1/device/push", "deletePush", http.StatusNoContent, nil, nil, nil},
		{http.MethodDelete, "/api/v1/device", "deleteDevice", http.StatusNoContent, nil, nil, nil},
	})
}

// typeProblem says what is wrong with t as a route's query, body or response
// type, "" when nothing is: Story 5's generator takes JSON names from named
// types, so only struct{} (no data) may be unnamed.
func typeProblem(t reflect.Type) string {
	if t == nil || t == reflect.TypeFor[struct{}]() {
		return ""
	}
	if t.Name() == "" {
		return "unnamed type " + t.String()
	}
	return ""
}

func TestRoutes_TypesAreNamed(t *testing.T) {
	assert.Empty(t, typeProblem(nil))
	assert.Empty(t, typeProblem(reflect.TypeFor[struct{}]()))
	assert.Empty(t, typeProblem(reflect.TypeFor[echoBody]()))
	assert.NotEmpty(t, typeProblem(reflect.TypeFor[struct{ Code string }]()), "an unnamed struct is refused")

	for _, rt := range (&Layer{}).routes() {
		key := rt.spec.method + " " + rt.spec.path
		assert.Empty(t, typeProblem(rt.query), "%s: query", key)
		assert.Empty(t, typeProblem(rt.body), "%s: body", key)
		assert.Empty(t, typeProblem(rt.resp), "%s: resp", key)
	}
}

func TestRoutes_TheTableHoldsExactlyTheRoutesOfTheAPI(t *testing.T) {
	want := []string{
		"POST /api/v1/pair pair",
		"GET /api/v1/push-key pushKey",
		"POST /api/v1/sessions create",
		"GET /api/v1/launch-defaults launchDefaults",
		"POST /api/v1/sessions/{id}/send send",
		"POST /api/v1/sessions/{id}/asks/{ask}/answer answer",
		"POST /api/v1/sessions/{id}/merge merge",
		"POST /api/v1/sessions/{id}/pause pause",
		"POST /api/v1/sessions/{id}/resume resume",
		"POST /api/v1/sessions/{id}/kill kill",
		"POST /api/v1/sessions/{id}/push push",
		"GET /api/v1/sessions/{id}/message message",
		"GET /api/v1/sessions/{id}/turns turns",
		"GET /api/v1/sessions/{id}/diff diff",
		"GET /api/v1/sessions/{id}/mockup mockup",
		"GET /api/v1/sessions/{id}/jobs/{job}/log jobLog",
		"POST /api/v1/sessions/{id}/jobs/{job}/{action} jobControl",
		"GET /api/v1/sessions/{id}/flow flow",
		"GET /api/v1/sessions/{id}/flow/doc flowDoc",
		"GET /api/v1/flows flows",
		"PUT /api/v1/device putDevice",
		"PUT /api/v1/quick-replies putQuickReplies",
		"POST /api/v1/device/push postPush",
		"DELETE /api/v1/device/push deletePush",
		"DELETE /api/v1/device deleteDevice",
	}
	var got []string
	for _, rt := range (&Layer{}).routes() {
		got = append(got, rt.spec.method+" "+rt.spec.path+" "+rt.spec.client)
	}
	assert.ElementsMatch(t, want, got, "every route under /api/v1 but the stream is declared once, in the table")
}

func TestRoutes_PairAndPushKeyAndTheMockup(t *testing.T) {
	requireRows(t, []tableRow{
		{http.MethodPost, "/api/v1/pair", "pair", http.StatusOK, nil, reflect.TypeFor[pairBody](), reflect.TypeFor[pairJSON]()},
		{http.MethodGet, "/api/v1/push-key", "pushKey", http.StatusOK, nil, nil, reflect.TypeFor[pushKeyJSON]()},
	})
	assert.Empty(t, routeOf(t, &Layer{}, http.MethodPost, "/api/v1/pair").spec.log, "pair logs for itself")
	assert.Empty(t, routeOf(t, &Layer{}, http.MethodGet, "/api/v1/push-key").spec.log, "so does the push key")

	mockup := routeOf(t, &Layer{}, http.MethodGet, "/api/v1/sessions/{id}/mockup")
	assert.True(t, mockup.page, "the mockup is a page the generated client only builds the URL of")
	assert.Equal(t, "mockup", mockup.spec.client)
	assert.Nil(t, mockup.resp)
	assert.Nil(t, mockup.query)
	assert.Nil(t, mockup.body)
}

func TestRoutes_TheOpenEntryIsServedToARequestWithNoCookie(t *testing.T) {
	host := &scriptedHost{name: "iPhone", token: deviceToken(7)}
	l := &Layer{host: host}
	m := http.NewServeMux()
	l.registerRoutes(m)
	rec := httptest.NewRecorder()

	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pair", strings.NewReader(`{"code": "k7qm-2xpa"}`)))

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"k7qm-2xpa"}, host.redeemed, "the pair handler ran, with no cookie and so no device")
	assert.JSONEq(t, `{"device":"iPhone"}`, rec.Body.String())
}
