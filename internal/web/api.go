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

// pathSession is the session the path names: a decimal ID the model serves.
func (l *Layer) pathSession(r *http.Request) (core.InstanceID, bool) {
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil && n != 0 && n <= maxIDValue {
		if _, ok := l.client.View(core.InstanceID(n)); ok {
			return core.InstanceID(n), true
		}
	}
	return 0, false
}

// sessionFromPath is the session the path names, answered 404 when the path
// holds no decimal ID the model serves.
func (l *Layer) sessionFromPath(w http.ResponseWriter, r *http.Request, a actionLog) (core.InstanceID, bool) {
	if id, ok := l.pathSession(r); ok {
		return id, true
	}
	l.refuse(w, a, http.StatusNotFound, "no such session")
	return 0, false
}

// settle is the outcome rule of an action's request, which act and
// actCreated share: not done is errPending (the outcome follows on the
// device's stream), after the notices it was given, which are those raised
// before the request, go to the device's stream; a Reply error is returned,
// after the notices beside it that do not repeat it go to the device's
// stream; otherwise the notices are kept among the hub's recent ones and
// returned, never nil.
func (l *Layer) settle(device string, reply core.Reply, notes []noticeJSON, done bool) ([]noticeJSON, error) {
	switch {
	case !done:
		for _, n := range notes {
			l.reqs.stream(device, n)
		}
		return nil, errPending
	case reply.Err != nil:
		text := reply.Err.Error()
		for _, n := range notes {
			if n.Text != text {
				l.reqs.stream(device, n)
			}
		}
		return nil, reply.Err
	}
	l.keepNotices(notes)
	return nonNil(notes), nil
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

// sessionOp is the typed handler of a lifecycle request that names only the session: pause, resume, kill and push.
func (l *Layer) sessionOp(op func(core.Core, core.InstanceID, core.ReqID)) func(c *call, _ struct{}) (noticesJSON, error) {
	return func(c *call, _ struct{}) (noticesJSON, error) {
		id, err := c.session()
		if err != nil {
			return noticesJSON{}, err
		}
		return c.act(func(req core.ReqID) {
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
func (l *Layer) sendReply(c *call, body sendBody) (noticesJSON, error) {
	id, err := c.session()
	if err != nil {
		return noticesJSON{}, err
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len(text) > maxReplyBytes {
		return noticesJSON{}, &statusError{http.StatusBadRequest, fmt.Sprintf("text: want 1 to %d bytes", maxReplyBytes)}
	}
	return c.act(func(req core.ReqID) {
		l.client.Send(id, core.Message{Text: text, Origin: core.OriginWeb}, req)
	})
}

// mergeBody is POST /api/v1/sessions/{id}/merge.
type mergeBody struct {
	Source core.InstanceID `json:"source"`
}

// mergeInto merges the source session into the one in the URL, as the TUI's
// picker does; the model checks both (admit).
func (l *Layer) mergeInto(c *call, body mergeBody) (noticesJSON, error) {
	id, err := c.session()
	if err != nil {
		return noticesJSON{}, err
	}
	if body.Source == 0 || body.Source > maxIDValue {
		return noticesJSON{}, &statusError{http.StatusBadRequest, "source: want a session ID"}
	}
	if _, ok := l.client.View(body.Source); !ok {
		return noticesJSON{}, &statusError{http.StatusNotFound, "source: no such session"}
	}
	return c.act(func(req core.ReqID) {
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
func (l *Layer) createSession(c *call, body createBody) (createdJSON, error) {
	title := strings.TrimSpace(body.Title)
	prompt := strings.TrimRight(body.Prompt, " \t\r\n")
	if len(prompt) > maxPromptBytes {
		return createdJSON{}, &statusError{http.StatusBadRequest, fmt.Sprintf("prompt: want at most %d bytes", maxPromptBytes)}
	}
	ws, ok := l.client.Workspace(body.Workspace)
	if !ok {
		return createdJSON{}, &statusError{http.StatusBadRequest, "workspace: no such workspace"}
	}
	if err := core.CheckNewTitle(title, ws); err != nil {
		return createdJSON{}, &statusError{http.StatusBadRequest, err.Error()}
	}
	views := l.client.Views(ws.ID)
	for _, check := range []error{core.CheckRoom(len(views)), core.CheckWritable(ws)} {
		if check != nil {
			return createdJSON{}, &statusError{http.StatusConflict, check.Error()}
		}
	}
	for _, v := range views {
		if v.Title == title {
			return createdJSON{}, &statusError{http.StatusConflict, fmt.Sprintf("%s already has a session titled %q", ws.Label, title)}
		}
	}
	program := ws.Settings.GetProgram()
	l.client.ReloadAccounts()
	opts, notice, refused := l.launchOptions(ws, program, body)
	if refused != nil {
		return createdJSON{}, refused
	}
	if notice != "" {
		c.notice(noticeOf(core.Notice{Info: notice}, l.now()))
	}
	return c.actCreated(func(req core.ReqID) {
		l.client.Create(ws.ID, core.NewInstance{
			Title: title, Path: ws.RepoPath, Program: program, Prompt: prompt, Launch: opts, Start: true,
		}, req)
	})
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

// launchDefaultsParams is GET /api/v1/launch-defaults's query.
type launchDefaultsParams struct {
	Workspace string `query:"workspace"`
}

// launchDefaults answers GET /api/v1/launch-defaults?workspace=<id> as the
// modal would pre-fill a new session in that workspace. The accounts are the
// registry's, for a Claude program only: any other takes no account.
func (l *Layer) launchDefaults(_ *call, p launchDefaultsParams) (launchDefaultsJSON, error) {
	n, err := strconv.ParseUint(p.Workspace, 10, 64)
	ws, ok := l.client.Workspace(core.WorkspaceID(n))
	if err != nil || !ok {
		return launchDefaultsJSON{}, &statusError{http.StatusBadRequest, "workspace: no such workspace"}
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
	return out, nil
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
func (l *Layer) putDevice(c *call, body deviceBody) (deviceJSON, error) {
	change := core.NotifyChange{Waiting: body.Waiting, Finished: body.Finished}
	if body.Pause != nil {
		switch *body.Pause {
		case "1h":
			until := l.now().Add(time.Hour)
			change.PausedUntil = &until
		case "none":
			change.PausedUntil = &time.Time{}
		default:
			return deviceJSON{}, &statusError{http.StatusBadRequest, `pause: want "1h" or "none"`}
		}
	}
	stored, err := l.host.SetNotify(c.device, change)
	if err != nil {
		return deviceJSON{}, err
	}
	return deviceOf(stored), nil
}

// message answers GET /api/v1/sessions/{id}/message: the tree of the
// session's last message (buildTree), from the web client's replica, with the
// text it was built from, so the app can tell it is the message it shows. A
// workspace terminal is no session of the app's.
func (l *Layer) message(c *call, _ struct{}) (messageJSON, error) {
	id, err := c.readSession()
	if err != nil {
		return messageJSON{}, err
	}
	v, ok := l.client.View(id)
	if !ok {
		return messageJSON{}, &statusError{http.StatusNotFound, "no such session"}
	}
	out := messageJSON{Blocks: []*mdNode{}}
	if v.HasLastMessage {
		out.Text = v.LastMessage
		out.Blocks, out.Truncated = buildTree(v.LastMessage)
	}
	return out, nil
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
func (l *Layer) putQuickReplies(_ *call, body quickRepliesBody) (quickRepliesJSON, error) {
	stored, err := l.host.SetQuickReplies(body.Replies)
	if err != nil {
		if errors.Is(err, core.ErrQuickReplies) {
			return quickRepliesJSON{}, &statusError{http.StatusBadRequest, err.Error()}
		}
		return quickRepliesJSON{}, err
	}
	return quickRepliesJSON{QuickReplies: resolvedQuickReplies(stored)}, nil
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
func (l *Layer) postPush(c *call, body subscriptionBody) (deviceJSON, error) {
	// The model takes unpadded base64url only; a browser may give padding.
	sub := core.PushSubscription{
		Endpoint: body.Endpoint,
		P256DH:   strings.TrimRight(body.Keys.P256DH, "="),
		Auth:     strings.TrimRight(body.Keys.Auth, "="),
	}
	if err := core.ValidatePushSubscription(sub); err != nil {
		return deviceJSON{}, &statusError{http.StatusBadRequest, err.Error()}
	}
	stored, err := l.host.SetPushSubscription(c.device, sub)
	if err != nil {
		return deviceJSON{}, err
	}
	return deviceOf(stored), nil
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
func (l *Layer) deletePush(c *call, _ struct{}) (struct{}, error) {
	if endpoint := l.host.PushEndpoint(c.device); endpoint != "" {
		l.host.PushResult(c.device, endpoint, core.PushExpired)
	}
	return struct{}{}, nil
}

// deleteDevice unpairs the phone that asks: the model forgets the device
// (which ends its streams and drops its subscription) and the cookie is
// cleared. A device already gone is a device unpaired.
func (l *Layer) deleteDevice(c *call, _ struct{}) (struct{}, error) {
	if err := l.client.RevokeDevice(c.device); err != nil && !core.IsNoDevice(err, c.device) {
		return struct{}{}, err
	}
	clearDeviceCookie(c.w)
	return struct{}{}, nil
}
