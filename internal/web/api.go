package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
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

// record logs the action with how it ended: the HTTP status it answered and
// a word or the refusal's text. Every answer a route gives, one that worked
// or one the layer or the model refused, comes through here or refuse, so the
// log holds the device, the action, the session and the result of each, in
// one format ("<status> <what>").
func (l *Layer) record(a actionLog, status int, what string) {
	result := fmt.Sprintf("%d %s", status, what)
	if l.onAction != nil {
		l.onAction(a, result)
		return
	}
	log.For("web").Info("web.action", "device", a.device, "action", a.name, "session", uint64(a.sessionID), "result", result)
}

// refuse records the action's refusal and answers it with msg.
func (l *Layer) refuse(w http.ResponseWriter, a actionLog, status int, msg string) {
	l.record(a, status, msg)
	writeError(w, status, msg)
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
	m.Handle("GET /api/v1/sessions/{id}/message", l.authed(l.message))
	m.Handle("GET /api/v1/sessions/{id}/turns", l.authed(l.readTurns))
	m.Handle("GET /api/v1/sessions/{id}/diff", l.authed(l.readDiff))
	m.Handle("POST /api/v1/sessions", l.authed(l.createSession))
	m.Handle("GET /api/v1/launch-defaults", l.authed(l.launchDefaults))
	m.Handle("PUT /api/v1/device", l.authed(l.putDevice))
	m.Handle("PUT /api/v1/quick-replies", l.authed(l.putQuickReplies))
	m.Handle("POST /api/v1/device/push", l.authed(l.postPush))
	m.Handle("DELETE /api/v1/device/push", l.authed(l.deletePush))
	m.Handle("DELETE /api/v1/device", l.authed(l.deleteDevice))
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
func (l *Layer) decodeBody(w http.ResponseWriter, r *http.Request, a actionLog, v any) bool {
	if err := readJSON(r, v); err != nil {
		l.refuse(w, a, http.StatusBadRequest, "bad request: "+err.Error())
		return false
	}
	return true
}

// sessionFromPath is the session the path names, answered 404 when the path
// holds no decimal ID the model serves.
func (l *Layer) sessionFromPath(w http.ResponseWriter, r *http.Request, a actionLog) (core.InstanceID, bool) {
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil && n != 0 && n <= maxIDValue {
		if _, ok := l.client.View(core.InstanceID(n)); ok {
			return core.InstanceID(n), true
		}
	}
	l.refuse(w, a, http.StatusNotFound, "no such session")
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
// response, with the new session's ID for a create, and are also kept among
// the recent notices the snapshot carries.
func (l *Layer) writeOutcome(w http.ResponseWriter, a actionLog, reply core.Reply, notes []noticeJSON, done, created bool) {
	switch {
	case !done:
		l.record(a, http.StatusAccepted, "pending")
		writeJSON(w, http.StatusAccepted, map[string]bool{"pending": true})
	case reply.Err != nil:
		text := reply.Err.Error()
		for _, n := range notes {
			if n.Text != text {
				l.reqs.stream(a.device, n)
			}
		}
		status := modelStatus(reply.Err)
		l.refuse(w, a, status, text)
	case created:
		l.keepNotices(notes)
		a.sessionID = reply.ID
		l.record(a, http.StatusCreated, "created")
		writeJSON(w, http.StatusCreated, createdJSON{ID: reply.ID, Notices: nonNil(notes)})
	default:
		l.keepNotices(notes)
		l.record(a, http.StatusOK, "ok")
		writeJSON(w, http.StatusOK, noticesJSON{Notices: nonNil(notes)})
	}
}

// keepNotices keeps the notices a response carries among the hub's recent
// ones, which the stream's snapshot sends, so a response the phone never got
// loses none (a kill's stash SHA above all). It is not for a notice that went
// to the stream, which the hub kept already.
func (l *Layer) keepNotices(notes []noticeJSON) {
	if l.hub != nil {
		l.hub.keep(notes)
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
		a := actionLog{device: device, name: name}
		id, ok := l.sessionFromPath(w, r, a)
		if !ok {
			return
		}
		a.sessionID = id
		l.doAction(w, r, a, func(req core.ReqID) {
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
	a := actionLog{device: device, name: "send"}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	var body sendBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len(text) > maxReplyBytes {
		l.refuse(w, a, http.StatusBadRequest, fmt.Sprintf("text: want 1 to %d bytes", maxReplyBytes))
		return
	}
	l.doAction(w, r, a, func(req core.ReqID) {
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
	a := actionLog{device: device, name: "merge"}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	var body mergeBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	if body.Source == 0 || body.Source > maxIDValue {
		l.refuse(w, a, http.StatusBadRequest, "source: want a session ID")
		return
	}
	if _, ok := l.client.View(body.Source); !ok {
		l.refuse(w, a, http.StatusNotFound, "source: no such session")
		return
	}
	l.doAction(w, r, a, func(req core.ReqID) {
		l.client.Merge(id, body.Source, req)
	})
}

// statusError is a refusal that knows its HTTP status.
type statusError struct {
	status int
	msg    string
}

// createBody is POST /api/v1/sessions: the overrides are optional.
type createBody struct {
	Workspace      core.WorkspaceID `json:"workspace"`
	Title          string           `json:"title"`
	Prompt         string           `json:"prompt"`
	Account        string           `json:"account"`
	Model          string           `json:"model"`
	PermissionMode string           `json:"permission_mode"`
	Effort         string           `json:"effort"`
}

// createSession creates and starts a session as `loom session new` does: the
// checks the TUI makes (core's), a title no row of the workspace holds, then
// the launch options (launchOptions), then the Create, whose Reply names the
// new session at once. The start plays out on the stream.
func (l *Layer) createSession(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "create"}
	var body createBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	prompt := strings.TrimRight(body.Prompt, " \t\r\n")
	if len(prompt) > maxPromptBytes {
		l.refuse(w, a, http.StatusBadRequest, fmt.Sprintf("prompt: want at most %d bytes", maxPromptBytes))
		return
	}
	ws, ok := l.client.Workspace(body.Workspace)
	if !ok {
		l.refuse(w, a, http.StatusBadRequest, "workspace: no such workspace")
		return
	}
	if err := core.CheckNewTitle(title, ws); err != nil {
		l.refuse(w, a, http.StatusBadRequest, err.Error())
		return
	}
	views := l.client.Views(ws.ID)
	for _, check := range []error{core.CheckRoom(len(views)), core.CheckWritable(ws)} {
		if check != nil {
			l.refuse(w, a, http.StatusConflict, check.Error())
			return
		}
	}
	for _, v := range views {
		if v.Title == title {
			l.refuse(w, a, http.StatusConflict, fmt.Sprintf("%s already has a session titled %q", ws.Label, title))
			return
		}
	}
	program := ws.Settings.GetProgram()
	l.client.ReloadAccounts()
	opts, notice, refused := l.launchOptions(ws, program, body)
	if refused != nil {
		l.refuse(w, a, refused.status, refused.msg)
		return
	}
	var pre []noticeJSON
	if notice != "" {
		pre = append(pre, noticeOf(core.Notice{Info: notice}, l.now()))
	}
	req := l.reqs.next()
	l.client.Create(ws.ID, core.NewInstance{
		Title: title, Path: ws.RepoPath, Program: program, Prompt: prompt, Launch: opts, Start: true,
	}, req)
	reply, notes, done := l.reqs.wait(r.Context(), req, device)
	l.writeOutcome(w, a, reply, append(pre, notes...), done, true)
}

// launchOptions are the options a new session in ws launches program with:
// what the Session Launch Options modal pre-fills (the workspace's settings,
// then core.ChooseLaunchAccount), then body's overrides. The caller reloads
// the accounts first. The notice is the modal's, when the launch moved off a
// removed account. Remote control the account's login can't use is refused,
// as `loom session new` refuses it: the app has no flag to start without it,
// so the message names the workspace's setting.
func (l *Layer) launchOptions(ws core.WorkspaceView, program string, body createBody) (launch.Options, string, *statusError) {
	for _, o := range []struct {
		name, value string
		allowed     []string
	}{
		{"model", body.Model, config.ClaudeModelAliases()},
		{"permission_mode", body.PermissionMode, config.ClaudePermissionModes},
		{"effort", body.Effort, config.ClaudeEfforts},
	} {
		if o.value != "" && !slices.Contains(o.allowed, o.value) {
			return launch.Options{}, "", &statusError{http.StatusBadRequest, fmt.Sprintf("%s %q: want one of %s", o.name, o.value, strings.Join(o.allowed, ", "))}
		}
	}
	opts, notice := core.ChooseLaunchAccount(l.client, launch.FromSettings(ws.Settings), program)
	if body.Account != "" {
		switch {
		case !session.IsClaudeProgram(program):
			return launch.Options{}, "", &statusError{http.StatusBadRequest, fmt.Sprintf("account: %s is not Claude", program)}
		case body.Account != account.DefaultName:
			if _, ok := l.client.Account(body.Account); !ok {
				return launch.Options{}, "", &statusError{http.StatusBadRequest, fmt.Sprintf("account %q is not registered", body.Account)}
			}
		}
		opts.Account = body.Account
		// The notice names the account the launch moved off to, which the
		// device's own choice replaced.
		notice = ""
	}
	if body.Model != "" {
		opts.Model = body.Model
	}
	if body.PermissionMode != "" {
		opts.PermissionMode = body.PermissionMode
	}
	if body.Effort != "" {
		opts.Effort = body.Effort
	}
	if auth := l.client.RCAuthFor(opts.Account); launch.RemoteControlBlocked(auth, launch.EffectiveRemoteControl(opts), program) {
		return launch.Options{}, "", &statusError{http.StatusConflict, fmt.Sprintf("remote control is unavailable: %s (turn it off in the workspace's settings to start without it)", auth.Reason)}
	}
	return opts, notice, nil
}

// launchDefaultsJSON is what the new-session sheet pre-fills, the account
// names it offers and the lists it picks from.
type launchDefaultsJSON struct {
	Account         string   `json:"account"`
	Accounts        []string `json:"accounts"`
	Model           string   `json:"model"`
	Models          []string `json:"models"`
	PermissionMode  string   `json:"permission_mode"`
	PermissionModes []string `json:"permission_modes"`
	Effort          string   `json:"effort"`
	Efforts         []string `json:"efforts"`
}

// launchDefaults answers GET /api/v1/launch-defaults?workspace=<id> as the
// modal would pre-fill a new session in that workspace. The accounts are the
// registry's, for a Claude program only: any other takes no account.
func (l *Layer) launchDefaults(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "launch-defaults"}
	n, err := strconv.ParseUint(r.URL.Query().Get("workspace"), 10, 64)
	ws, ok := l.client.Workspace(core.WorkspaceID(n))
	if err != nil || !ok {
		l.refuse(w, a, http.StatusBadRequest, "workspace: no such workspace")
		return
	}
	program := ws.Settings.GetProgram()
	l.client.ReloadAccounts()
	opts, _ := core.ChooseLaunchAccount(l.client, launch.FromSettings(ws.Settings), program)
	out := launchDefaultsJSON{
		Account:         opts.Account,
		Accounts:        []string{},
		Model:           opts.Model,
		Models:          config.ClaudeModelAliases(),
		PermissionMode:  opts.PermissionMode,
		PermissionModes: slices.Clone(config.ClaudePermissionModes),
		Effort:          opts.Effort,
		Efforts:         slices.Clone(config.ClaudeEfforts),
	}
	if names := l.client.AccountNames(); names.Present && session.IsClaudeProgram(program) {
		out.Accounts = append(out.Accounts, names.Names...)
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, out)
}

// deviceBody is PUT /api/v1/device: the notification settings to change; one
// left out stays as it is. Pause is "1h" (an hour from now) or "none".
type deviceBody struct {
	Waiting  *bool   `json:"waiting"`
	Finished *bool   `json:"finished"`
	Pause    *string `json:"pause"`
}

// putDevice changes the device's notification settings, only the ones the
// body names (core.NotifyChange), and answers the device as the model stored
// it.
func (l *Layer) putDevice(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "notify"}
	var body deviceBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	change := core.NotifyChange{Waiting: body.Waiting, Finished: body.Finished}
	if body.Pause != nil {
		switch *body.Pause {
		case "1h":
			until := l.now().Add(time.Hour)
			change.PausedUntil = &until
		case "none":
			change.PausedUntil = &time.Time{}
		default:
			l.refuse(w, a, http.StatusBadRequest, `pause: want "1h" or "none"`)
			return
		}
	}
	stored, err := l.host.SetNotify(device, change)
	if err != nil {
		l.refuse(w, a, modelStatus(err), err.Error())
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, deviceOf(stored))
}

// message answers GET /api/v1/sessions/{id}/message: the tree of the
// session's last message (buildTree), from the web client's replica, with the
// text it was built from, so the app can tell it is the message it shows. A
// workspace terminal is no session of the app's.
func (l *Layer) message(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "message"}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	v, ok := l.client.View(id)
	if !ok || v.IsWorkspaceTerminal {
		l.refuse(w, a, http.StatusNotFound, "no such session")
		return
	}
	out := messageJSON{Blocks: []*mdNode{}}
	if v.HasLastMessage {
		out.Text = v.LastMessage
		out.Blocks, out.Truncated = buildTree(v.LastMessage)
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, out)
}

// quickRepliesBody is PUT /api/v1/quick-replies: the replies to offer, or
// null (or no field) for the defaults.
type quickRepliesBody struct {
	Replies []string `json:"replies"`
}

// putQuickReplies stores the quick replies every paired phone offers
// (WebHost.SetQuickReplies) and answers the list as the app reads it. A list
// the model refuses is 400 with its words; any other error is the model's
// (modelStatus), as for the device's settings.
func (l *Layer) putQuickReplies(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "quick-replies"}
	var body quickRepliesBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	stored, err := l.host.SetQuickReplies(body.Replies)
	if err != nil {
		status := modelStatus(err)
		if errors.Is(err, core.ErrQuickReplies) {
			status = http.StatusBadRequest
		}
		l.refuse(w, a, status, err.Error())
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, quickRepliesJSON{QuickReplies: resolvedQuickReplies(stored)})
}

// subscriptionKeys are a subscription's keys, base64url as the browser gives them.
type subscriptionKeys struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// subscriptionBody is POST /api/v1/device/push, in the shape of the browser's
// PushSubscription.toJSON(). Its expirationTime is read and ignored.
type subscriptionBody struct {
	Endpoint       string           `json:"endpoint"`
	ExpirationTime any              `json:"expirationTime"`
	Keys           subscriptionKeys `json:"keys"`
}

// postPush stores the device's push subscription, refused when it names a
// host the daemon would not post to or keys of the wrong size
// (core.ValidatePushSubscription), and answers the device. Padding is
// stripped from the keys first.
func (l *Layer) postPush(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "subscribe"}
	var body subscriptionBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	// The model takes unpadded base64url only; a browser may give padding.
	sub := core.PushSubscription{
		Endpoint: body.Endpoint,
		P256DH:   strings.TrimRight(body.Keys.P256DH, "="),
		Auth:     strings.TrimRight(body.Keys.Auth, "="),
	}
	if err := core.ValidatePushSubscription(sub); err != nil {
		l.refuse(w, a, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := l.host.SetPushSubscription(device, sub)
	if err != nil {
		l.refuse(w, a, modelStatus(err), err.Error())
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, deviceOf(stored))
}

// deletePush drops the device's push subscription, for the app's launch
// check: Apple answers 201 to a subscription the phone has dropped, and drops
// the push, so the sender never learns of it (spec, "Push"). The model
// records the state as expired, as for a 410, which the app shows as
// notifications to turn on again; a device with no subscription is left as it
// is. It answers 204. The result names the endpoint the host holds for the
// device now (a PushResult for any other changes nothing), read with
// PushEndpoint rather than PushTargets, which leaves out a subscription that
// fails its check (a refused one loaded from web.json), though it is still
// the device's to drop. A subscription
// posted after the app decided to drop its copy but before this read is
// dropped with it, which the app's next launch check finds and posts again;
// one posted after the read keeps its new endpoint, which the result does not
// name.
func (l *Layer) deletePush(w http.ResponseWriter, _ *http.Request, device string) {
	a := actionLog{device: device, name: "unsubscribe"}
	if endpoint := l.host.PushEndpoint(device); endpoint != "" {
		l.host.PushResult(device, endpoint, core.PushExpired)
	}
	l.record(a, http.StatusNoContent, "ok")
	w.WriteHeader(http.StatusNoContent)
}

// deleteDevice unpairs the phone that asks: the model forgets the device
// (which ends its streams and drops its subscription) and the cookie is
// cleared. A device already gone is a device unpaired.
func (l *Layer) deleteDevice(w http.ResponseWriter, _ *http.Request, device string) {
	a := actionLog{device: device, name: "unpair"}
	if err := l.client.RevokeDevice(device); err != nil && !core.IsNoDevice(err, device) {
		l.refuse(w, a, modelStatus(err), err.Error())
		return
	}
	l.record(a, http.StatusNoContent, "ok")
	clearDeviceCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
