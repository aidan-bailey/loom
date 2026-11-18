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

// readSession is session for a read route: a workspace terminal is no
// session of the app's, so it is 404 as an unknown one is.
func (c *call) readSession() (core.InstanceID, error) {
	id, err := c.session()
	if err != nil {
		return 0, err
	}
	if c.l.isWorkspaceTerminal(id) {
		return 0, &statusError{http.StatusNotFound, "no such session"}
	}
	return id, nil
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

// lifecycleDoc is the doc of a lifecycle route that names only the session.
func lifecycleDoc(request string) string {
	return "The " + request + " request of the session: 200 with its notices, 409 with the model's own words when it refuses, and 202 `{\"pending\": true}` when it is still running after 30 s, its outcome then arriving on the stream."
}

// routes is the table, in registration order. Story 2 of the web foundation
// moves the other routes onto it.
func (l *Layer) routes() []route {
	return []route{
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/send", log: "send", client: "send", ok: http.StatusOK,
			doc: "`{text}`: sends the text to the session through its loom mod as the person's own words (`core.OriginWeb`); a reply that is an allowed slash command runs it, and its result arrives as a notice. The text is 1 byte to 64 KiB after trimming, else 400.",
		}, l.sendReply),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/merge", log: "merge", client: "merge", ok: http.StatusOK,
			doc: "`{source}`: merges the session `source` names into the session in the URL, as the TUI's picker does. A `source` that is no session ID is 400, one that names no session 404, and a merge the model refuses 409 with its words.",
		}, l.mergeInto),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/pause", log: "pause", client: "pause", ok: http.StatusOK,
			doc: lifecycleDoc("pause"),
		}, l.sessionOp(core.Core.Pause)),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/resume", log: "resume", client: "resume", ok: http.StatusOK,
			doc: lifecycleDoc("resume"),
		}, l.sessionOp(core.Core.Resume)),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/kill", log: "kill", client: "kill", ok: http.StatusOK,
			doc: lifecycleDoc("kill"),
		}, l.sessionOp(core.Core.Kill)),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/push", log: "push", client: "push", ok: http.StatusOK,
			doc: lifecycleDoc("push"),
		}, l.sessionOp(core.Core.Push)),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/message", log: "message", client: "message", ok: http.StatusOK,
			doc: "The session's last message as a tree of plain nodes (`markdown.go`) with the text it was built from: `{text, blocks, truncated}`. No screen draws it now, and the route stays. A workspace terminal is no session of the app's: 404.",
		}, l.message),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/turns", log: "turns", client: "turns", ok: http.StatusOK,
			doc: "A page of the session's conversation: `{conversation, turns, earlier, reset, skipped}` and, on a subagent's page, `agent`. `before` (the page before, from a page's `earlier`) and `after` (the turns from a turn's `id` on) are byte offsets, at most one set, else 400; `agent` reads that subagent's own transcript in place of the conversation, and an id Claude could not have written is 400. A text item crosses as its tree, never as its text; a page holds at most 10 turns and, past its newest, 256 KiB of text.",
		}, l.readTurns),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/diff", log: "diff", client: "diff", ok: http.StatusOK,
			doc: "The session's worktree diff against its base commit: `{base, files}`, a file `{path, old_path, status, binary, added, removed, hunks, omitted}`. A diff keeps at most 1 MiB of lines, 5,000 lines a file and 2,000 bytes a line; `omitted` is `lines` or `budget` when a file's hunks were dropped.",
		}, l.readDiff),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/jobs/{job}/log", log: "job_log", client: "jobLog", ok: http.StatusOK,
			doc: "A job of the session's output, from the byte offset `after` or, when `after` is absent, its tail: `{text, start, end, cut, reset}`, at most 64 KiB, cleaned for display. An `after` past the file's end answers a fresh tail with `reset`. A malformed `job` or `after` is 400; a job that is not the session's, or that the queue no longer holds, is the model's 409.",
		}, l.readJobLog),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/flow", log: "flow", client: "flow", ok: http.StatusOK,
			doc: "The board of the flow whose plan (or spec, in design) is `plan` in the session's worktree: `{summary, stages, base, start, end, premise, stories, final, question, kept, others, costs}`. No `plan` is 400, and a session with no worktree the model's 409.",
		}, l.readFlow),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/sessions/{id}/flow/doc", log: "flow-doc", client: "flowDoc", ok: http.StatusOK,
			doc: "One of the flow's documents as its tree, `{path, blocks, cut}`, each heading with its source `line`; at most 1 MiB and 40,000 nodes. `doc` is `spec`, `skeleton`, `plan`, `record`, `drafts-state` or `report:<agent run>`, and the model refuses any other with 409; a missing `plan` or `doc` is 400.",
		}, l.readFlowDoc),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/flows", log: "flows", client: "flows", ok: http.StatusOK,
			doc: "Every session's flows and the host's past runs: `{live, past}`, a live one `{session, title, flow}`, a past one `{key, file, stem, plan, stories, ended, end, usd, unreadable}`, newest first.",
		}, l.readFlows),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/jobs/{job}/{action}", log: "job_control", client: "jobControl", ok: http.StatusOK,
			doc: "A control of one of the session's jobs (the job controls spec): `action` is `start` (start a queued job now, past the queue's fit), `park` or `unpark` (a waiting job), `pause` or `resume` (a running one). Any other action is 404 (`no such action`), a malformed `job` 400, and a refusal, a job that is not the session's included, the model's 409.",
		}, l.jobControl),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions/{id}/asks/{ask}/answer", log: "answer", client: "answer", ok: http.StatusOK,
			doc: "`{answers}` (each question's `labels`, `other` and `note`, by its text), `{chat, chat_text}`, or `{allow, suggestion}` / `{allow: false, message}`: answers one of the session's asks. 200 once the session's loom mod took the answer, 409 with the model's words for every refusal; an `ask` ID over 128 bytes, more than 8 answers or 8 labels a question, or a text over 64 KiB is 400.",
		}, l.answerAsk),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/sessions", log: "create", client: "create", ok: http.StatusCreated,
			doc: "`{workspace, title, prompt}` and the optional `account`, `model`, `permission_mode`, `effort`: creates and starts a session as `loom session new` does; 201 `{id, notices}`. The prompt is at most 64 KiB and the title as `core.CheckNewTitle` says; an override that is no registered account, no model in `config.ClaudeModelAliases()`, no mode in `config.ClaudePermissionModes` or no effort in `config.ClaudeEfforts` is 400, and a workspace that is full or read-only, a title the workspace holds, or remote control the account's login can't use is 409.",
		}, l.createSession),
		typed(l, spec{
			method: http.MethodGet, path: "/api/v1/launch-defaults", log: "launch-defaults", client: "launchDefaults", ok: http.StatusOK,
			doc: "`?workspace=<id>`: what the new-session sheet pre-fills (`account`, `model`, `permission_mode`, `effort`) and the choices it offers (`accounts`, `models`, `permission_modes`, `efforts`), from the account registry, `config.ClaudeModelAliases()`, `config.ClaudePermissionModes` and `config.ClaudeEfforts`, so the app hard-codes none. An unknown workspace is 400.",
		}, l.launchDefaults),
		typed(l, spec{
			method: http.MethodPut, path: "/api/v1/device", log: "notify", client: "putDevice", ok: http.StatusOK,
			doc: "`{waiting, finished, pause}`: this device's notification settings, each field optional and a missing one left as it is; `pause` is `\"1h\"` (until an hour from now) or `\"none\"` (ends a pause), any other value is 400. Answers the device as the model stored it.",
		}, l.putDevice),
		typed(l, spec{
			method: http.MethodPut, path: "/api/v1/quick-replies", log: "quick-replies", client: "putQuickReplies", ok: http.StatusOK,
			doc: "`{replies}`: the quick replies every paired phone offers above its reply box, or `null` (or no field) for the defaults; a list `core.NormalizeQuickReplies` refuses is 400 with its words. Answers `{quick_replies}`, the list as the app reads it.",
		}, l.putQuickReplies),
		typed(l, spec{
			method: http.MethodPost, path: "/api/v1/device/push", log: "subscribe", client: "postPush", ok: http.StatusOK,
			doc: "The browser's `PushSubscription.toJSON()`: stores this device's push subscription and answers the device. An endpoint that is not `https`, over 2 KiB, or not on `web.push.apple.com`, `fcm.googleapis.com`, `updates.push.services.mozilla.com` or a host ending in `.notify.windows.com`, with a user or a port but 443, or keys of the wrong size, is 400.",
		}, l.postPush),
		typed(l, spec{
			method: http.MethodDelete, path: "/api/v1/device/push", log: "unsubscribe", client: "deletePush", ok: http.StatusNoContent,
			doc: "Drops this phone's push subscription, which the model records as expired; the app asks for it when it finds on a launch that the phone holds none or may not notify. 204.",
		}, l.deletePush),
		typed(l, spec{
			method: http.MethodDelete, path: "/api/v1/device", log: "unpair", client: "deleteDevice", ok: http.StatusNoContent,
			doc: "Unpairs this phone: the model forgets the device, which ends its streams and drops its subscription, and the cookie is cleared. 204, a device already gone included.",
		}, l.deleteDevice),
	}
}

// registerRoutes mounts every entry of the table on m at its method and
// path, wrapped in authed unless the entry is open, which is served with no
// device.
func (l *Layer) registerRoutes(m *http.ServeMux) {
	for _, rt := range l.routes() {
		var h http.Handler
		if rt.spec.open {
			h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rt.serve(w, r, "") })
		} else {
			h = l.authed(rt.serve)
		}
		m.Handle(rt.spec.method+" "+rt.spec.path, h)
	}
}
