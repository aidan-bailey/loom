package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

const (
	// maxReplyBytes is the longest reply a device may send a session, and
	// maxPromptBytes the longest first prompt of a new one.
	maxReplyBytes  = 64 << 10
	maxPromptBytes = 64 << 10
	// maxIDValue is the largest ID a request may name: IDs are JSON numbers,
	// exact in JavaScript up to 53 bits.
	maxIDValue = 1<<53 - 1
)

// noticesJSON is the body of an action that worked: the notices it raised.
type noticesJSON struct {
	Notices []noticeJSON `json:"notices"`
}

// createdJSON is the body of a create that worked.
type createdJSON struct {
	ID      core.InstanceID `json:"id"`
	Notices []noticeJSON    `json:"notices"`
}

// actionLog names one action for the log: who did what to which session.
type actionLog struct {
	device    string
	name      string
	sessionID core.InstanceID
}

// record logs the action with how it ended.
func (a actionLog) record(result string) {
	log.For("web").Info("web.action", "device", a.device, "action", a.name, "session", uint64(a.sessionID), "result", result)
}

// routeAPI registers the sessions' actions under /api/v1. Every route needs
// a paired device (authed); /pair, /stream and /push-key are other stories'.
func (l *Layer) routeAPI(m *http.ServeMux) {
	m.Handle("POST /api/v1/sessions/{id}/send", l.authed(l.sendReply))
	m.Handle("POST /api/v1/sessions/{id}/merge", l.authed(l.mergeInto))
	m.Handle("POST /api/v1/sessions/{id}/pause", l.authed(l.sessionOp("pause", core.Core.Pause)))
	m.Handle("POST /api/v1/sessions/{id}/resume", l.authed(l.sessionOp("resume", core.Core.Resume)))
	m.Handle("POST /api/v1/sessions/{id}/kill", l.authed(l.sessionOp("kill", core.Core.Kill)))
	m.Handle("POST /api/v1/sessions/{id}/push", l.authed(l.sessionOp("push", core.Core.Push)))
}

// modelStatus is the HTTP status a failed request earns: the session or the
// device is not there (404, 401), the daemon can't take the request (503), or
// the model refused or failed it, in words of its own (409).
func modelStatus(err error) int {
	switch {
	case errors.Is(err, core.ErrNoSession):
		return http.StatusNotFound
	case errors.Is(err, core.ErrNoDevice):
		return http.StatusUnauthorized
	case errors.Is(err, core.ErrUnavailable), errors.Is(err, core.ErrWebOff):
		return http.StatusServiceUnavailable
	}
	return http.StatusConflict
}

// decodeBody reads the request's JSON body into v, answering 400 when it is
// not that (an unknown field included).
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := readJSON(r, v); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return false
	}
	return true
}

// sessionFromPath is the session the path names, answered 404 when the path
// holds no decimal ID the model serves.
func (l *Layer) sessionFromPath(w http.ResponseWriter, r *http.Request) (core.InstanceID, bool) {
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil && n != 0 && n <= maxIDValue {
		if _, ok := l.client.View(core.InstanceID(n)); ok {
			return core.InstanceID(n), true
		}
	}
	writeError(w, http.StatusNotFound, "no such session")
	return 0, false
}

// doAction makes the request send names, with the web client's next request
// ID, and answers from how it ended.
func (l *Layer) doAction(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID)) {
	req := l.reqs.next()
	send(req)
	reply, notes, done := l.reqs.wait(r.Context(), req, a.device)
	l.writeOutcome(w, a, reply, notes, done, false)
}

// writeOutcome answers an action from how its request ended. Not done is the
// wait running out: 202, the outcome following on the stream. An error in the
// Reply keeps the model's text with the status it earns, and any notice
// beside it goes to the device's stream; otherwise the notices ride the
// response, with the new session's ID for a create.
func (l *Layer) writeOutcome(w http.ResponseWriter, a actionLog, reply core.Reply, notes []noticeJSON, done, created bool) {
	switch {
	case !done:
		a.record("pending")
		writeJSON(w, http.StatusAccepted, map[string]bool{"pending": true})
	case reply.Err != nil:
		text := reply.Err.Error()
		for _, n := range notes {
			if n.Text != text {
				l.reqs.stream(a.device, n)
			}
		}
		status := modelStatus(reply.Err)
		a.record(fmt.Sprintf("%d %s", status, text))
		writeError(w, status, text)
	case created:
		a.sessionID = reply.ID
		a.record("created")
		writeJSON(w, http.StatusCreated, createdJSON{ID: reply.ID, Notices: nonNil(notes)})
	default:
		a.record("ok")
		writeJSON(w, http.StatusOK, noticesJSON{Notices: nonNil(notes)})
	}
}

// nonNil is notes, as an empty list rather than null when there are none.
func nonNil(notes []noticeJSON) []noticeJSON {
	if notes == nil {
		return []noticeJSON{}
	}
	return notes
}

// sessionOp is the handler of a lifecycle request that names only the
// session: pause, resume, kill and push.
func (l *Layer) sessionOp(name string, op func(core.Core, core.InstanceID, core.ReqID)) deviceHandler {
	return func(w http.ResponseWriter, r *http.Request, device string) {
		id, ok := l.sessionFromPath(w, r)
		if !ok {
			return
		}
		l.doAction(w, r, actionLog{device: device, name: name, sessionID: id}, func(req core.ReqID) {
			op(l.client, id, req)
		})
	}
}

// sendBody is POST /api/v1/sessions/{id}/send.
type sendBody struct {
	Text string `json:"text"`
}

// sendReply sends the device's reply to the session through the loom mod
// (core.OriginWeb: the person's own words, no "From" line).
func (l *Layer) sendReply(w http.ResponseWriter, r *http.Request, device string) {
	id, ok := l.sessionFromPath(w, r)
	if !ok {
		return
	}
	var body sendBody
	if !decodeBody(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len(text) > maxReplyBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("text: want 1 to %d bytes", maxReplyBytes))
		return
	}
	l.doAction(w, r, actionLog{device: device, name: "send", sessionID: id}, func(req core.ReqID) {
		l.client.Send(id, core.Message{Text: text, Origin: core.OriginWeb}, req)
	})
}

// mergeBody is POST /api/v1/sessions/{id}/merge.
type mergeBody struct {
	Source core.InstanceID `json:"source"`
}

// mergeInto merges the source session into the one in the URL, as the TUI's
// picker does; the model checks both (admit).
func (l *Layer) mergeInto(w http.ResponseWriter, r *http.Request, device string) {
	id, ok := l.sessionFromPath(w, r)
	if !ok {
		return
	}
	var body mergeBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Source == 0 || body.Source > maxIDValue {
		writeError(w, http.StatusBadRequest, "source: want a session ID")
		return
	}
	if _, ok := l.client.View(body.Source); !ok {
		writeError(w, http.StatusNotFound, "source: no such session")
		return
	}
	l.doAction(w, r, actionLog{device: device, name: "merge", sessionID: id}, func(req core.ReqID) {
		l.client.Merge(id, body.Source, req)
	})
}
