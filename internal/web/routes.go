package web

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/core"
)

// The route table. Every JSON route under /api/v1 is declared once, as a
// route: its method and path, whether it is open, the action its log line
// names, the generated client's name for it, its success status, its
// documentation, the types of its query, body and answer, and the handler.
// Registration (registerRoutes) wraps every route that is not open in
// authed, so a route cannot be added without it, and the generator reads the
// same table for the app's types.

// spec is what one table entry declares.
type spec struct {
	method string // "GET", "POST", "PUT" or "DELETE"
	path   string // the ServeMux pattern's path
	open   bool   // served without authed: only POST /api/v1/pair
	log    string // the action its log line names, "" when the handler logs for itself
	client string // the generated TS function's name, lowerCamelCase, unique in the table
	ok     int    // the success status: 200, 201 or 204
	doc    string // what the route does and refuses, limits included, in one to three sentences
}

// route is one entry of the table.
type route struct {
	spec  spec
	query reflect.Type // the decoded query's type, nil for none
	body  reflect.Type // the decoded body's type, nil for none
	resp  reflect.Type // the success body's type, nil for a 204 or a page route
	page  bool         // an HTML page the generated client only builds the URL of
	serve deviceHandler
}

// errPending is act's and actCreated's "still running" outcome: the wait ran
// out, and the request's outcome follows on the device's stream.
var errPending = errors.New("the request is still running")

// pendingJSON is the 202 body.
type pendingJSON struct {
	Pending bool `json:"pending"`
}

// Error makes a refusal that knows its status an error, so a typed handler
// returns it and the adapter answers its status and text.
func (e *statusError) Error() string { return e.msg }

// errStatus is the HTTP status err earns and the text it answers: a
// statusError's own, otherwise the status modelStatus gives and err's text.
func errStatus(err error) (int, string) {
	var se *statusError
	if errors.As(err, &se) {
		return se.status, se.msg
	}
	return modelStatus(err), err.Error()
}

// call is a typed handler's request.
type call struct {
	l      *Layer
	w      http.ResponseWriter // for headers and cookies only: the adapter writes the body
	r      *http.Request
	device string // the paired device's name, "" on an open route
	a      actionLog

	// pre holds the notices raised before the request (notice).
	pre []noticeJSON

	// id and idErr are session's answer, once idKnown.
	id      core.InstanceID
	idErr   error
	idKnown bool
}

// session is the session {id} names, setting the log line's session: a
// *statusError 404 "no such session" when the path holds no decimal ID the
// model serves. A second call returns the first one's answer.
func (c *call) session() (core.InstanceID, error) {
	if !c.idKnown {
		c.idKnown = true
		if id, ok := c.l.pathSession(c.r); ok {
			c.id = id
			c.a.sessionID = id
		} else {
			c.idErr = &statusError{http.StatusNotFound, "no such session"}
		}
	}
	return c.id, c.idErr
}

// setAction replaces the log line's action, for a route whose action
// depends on the path.
func (c *call) setAction(name string) { c.a.name = name }

// notice adds a notice raised before the request (create's launch-account
// notice), which act and actCreated put ahead of the Reply's.
func (c *call) notice(n noticeJSON) { c.pre = append(c.pre, n) }

// request makes the request send names with the layer's next request ID,
// waits for it, and settles it with the notices raised before it (c.notice)
// ahead of the Reply's.
func (c *call) request(send func(req core.ReqID)) ([]noticeJSON, core.Reply, error) {
	req := c.l.reqs.next()
	send(req)
	reply, notes, done := c.l.reqs.wait(c.r.Context(), req, c.device)
	notes, err := c.l.settle(c.device, reply, slices.Concat(c.pre, notes), done)
	return notes, reply, err
}

// act is doAction's request and wait: the notices on success (kept among the
// hub's recent ones), errPending when the wait runs out (the notices raised
// before the request go to the device's stream), and the Reply's error
// otherwise, its other notices sent to the device's stream.
func (c *call) act(send func(req core.ReqID)) (noticesJSON, error) {
	notes, _, err := c.request(send)
	if err != nil {
		return noticesJSON{}, err
	}
	return noticesJSON{Notices: notes}, nil
}

// actCreated is act for a create: the answer names the new session, which
// the log line carries too.
func (c *call) actCreated(send func(req core.ReqID)) (createdJSON, error) {
	notes, reply, err := c.request(send)
	if err != nil {
		return createdJSON{}, err
	}
	c.a.sessionID = reply.ID
	return createdJSON{ID: reply.ID, Notices: notes}, nil
}

// read is readReply's wait for a typed read route: the Reply, untracked once
// answered, or the model's error, or a *statusError 504 when replyWait ran
// out.
func (c *call) read(send func(req core.ReqID)) (core.Reply, error) {
	return c.l.readReply(c.r.Context(), c.device, send)
}

// typed is a JSON route. A path holding {id} has its session resolved first,
// answering its 404 before any decode and putting the session on the log
// line, as the handlers did when each resolved its own. In is struct{} when
// the route takes neither query nor body; otherwise a GET reads its query
// into In (readQuery) and any other method decodes its body (readJSON, a
// failure being 400). The handler's value is answered with the route's
// status; errPending as 202; a *statusError with its status and text; any
// other error with the status modelStatus gives it. Every answer is logged
// unless the route's log name is empty.
func typed[In, Out any](l *Layer, s spec, h func(c *call, in In) (Out, error)) route {
	rt := route{spec: s}
	if t := reflect.TypeFor[In](); t != reflect.TypeFor[struct{}]() {
		if s.method == http.MethodGet {
			rt.query = t
		} else {
			rt.body = t
		}
	}
	if s.ok != http.StatusNoContent {
		rt.resp = reflect.TypeFor[Out]()
	}
	rt.serve = func(w http.ResponseWriter, r *http.Request, device string) {
		c := &call{l: l, w: w, r: r, device: device, a: actionLog{device: device, name: s.log}}
		logged := s.log != ""
		refuse := func(status int, msg string) {
			if logged {
				l.refuse(w, c.a, status, msg)
				return
			}
			writeError(w, status, msg)
		}
		fail := func(err error) {
			if errors.Is(err, errPending) {
				if logged {
					l.record(c.a, http.StatusAccepted, "pending")
				}
				writeJSON(w, http.StatusAccepted, pendingJSON{Pending: true})
				return
			}
			status, msg := errStatus(err)
			refuse(status, msg)
		}
		if strings.Contains(s.path, "{id}") {
			if _, err := c.session(); err != nil {
				fail(err)
				return
			}
		}
		var in In
		switch {
		case rt.query != nil:
			readQuery(r, &in)
		case rt.body != nil:
			if err := readJSON(r, &in); err != nil {
				refuse(http.StatusBadRequest, "bad request: "+err.Error())
				return
			}
		}
		out, err := h(c, in)
		if err != nil {
			fail(err)
			return
		}
		if logged {
			what := "ok"
			if s.ok == http.StatusCreated {
				what = "created"
			}
			l.record(c.a, s.ok, what)
		}
		if s.ok == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, s.ok, out)
	}
	return rt
}

// page is a hand-served route: an HTML page the generated client only builds
// the URL of. Its handler answers for itself, in the page's own failures.
func page(_ *Layer, s spec, h deviceHandler) route {
	return route{spec: s, page: true, serve: h}
}

// readQuery sets each string field of the struct v points to from the query
// parameter its `query:"<name>"` tag names; a parameter with no field is
// ignored, and a field whose parameter is absent is set empty. A query type
// holds only string fields: TestRoutes_QueryTypesHoldOnlyStrings checks every
// entry of the table.
func readQuery(r *http.Request, v any) {
	rv := reflect.ValueOf(v).Elem()
	values := r.URL.Query()
	for i := range rv.NumField() {
		name, ok := rv.Type().Field(i).Tag.Lookup("query")
		if !ok || rv.Field(i).Kind() != reflect.String {
			continue
		}
		rv.Field(i).SetString(values.Get(name))
	}
}
