package web

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
)

// The decisions behind a push: which transition of which session is one, and
// what it says. push.go owns the queue, the sender and the route.

const (
	pushWaiting  = "waiting"
	pushFinished = "finished"
)

// pushFinishedBody is what a finished push says: the session's last message
// stays out of it, since a lock screen shows the body to whoever holds the
// phone.
const pushFinishedBody = "Finished; ready for your next message"

// pushJob is one push worked out from a transition, before it is addressed to
// any device. Badge counts the sessions that need you across every workspace
// once the transition is applied.
type pushJob struct {
	Kind    string
	Session core.InstanceID
	Title   string
	Body    string
	Badge   int
}

// pushPayload is the JSON the service worker reads (sw.js: data.<key>).
type pushPayload struct {
	Kind    string          `json:"kind"`
	Session core.InstanceID `json:"session"`
	Title   string          `json:"title"`
	Body    string          `json:"body"`
	Badge   int             `json:"badge"`
	URL     string          `json:"url"`
}

// payload is the job as the service worker receives it, before encryption.
func (j pushJob) payload() ([]byte, error) {
	return json.Marshal(pushPayload{
		Kind:    j.Kind,
		Session: j.Session,
		Title:   j.Title,
		Body:    j.Body,
		Badge:   j.Badge,
		URL:     fmt.Sprintf("/#s/%d", j.Session),
	})
}

// urgency is the push's Urgency header: a session waiting for you is worth
// waking the phone, a finished one can wait for its next wake.
func (j pushJob) urgency() string {
	if j.Kind == pushWaiting {
		return "high"
	}
	return "normal"
}

// topic is the push's Topic header, one per session, so a phone that was
// offline gets only each session's latest push: the session ID's 8 bytes,
// big-endian, as unpadded base64url, always 11 characters. Apple refuses a
// Topic whose length is 1 mod 4 (spec, "Push"), so "s<id>" would fail for
// every 4- and 8-digit ID.
func (j pushJob) topic() string {
	return base64.RawURLEncoding.EncodeToString(binary.BigEndian.AppendUint64(nil, uint64(j.Session)))
}

// pushSeen is what the last projection showed of one session.
type pushSeen struct {
	status session.Status
	reason string
	detail string
}

// pushWatch works out the pushes a projection implies by comparing each
// session with the last one it saw. The first projection of a workspace is a
// baseline and implies nothing, so a daemon restart sends no flood. It is
// touched by one goroutine, the drain's.
type pushWatch struct {
	seen  map[core.WorkspaceID]map[core.InstanceID]pushSeen
	needs map[core.WorkspaceID]int
}

func newPushWatch() *pushWatch {
	return &pushWatch{
		seen:  map[core.WorkspaceID]map[core.InstanceID]pushSeen{},
		needs: map[core.WorkspaceID]int{},
	}
}

// forget drops what it knows of every workspace the model no longer serves,
// so one served again starts over with a baseline.
func (w *pushWatch) forget(served []core.WorkspaceView) {
	keep := make(map[core.WorkspaceID]bool, len(served))
	for _, v := range served {
		keep[v.ID] = true
	}
	for ws := range w.seen {
		if !keep[ws] {
			delete(w.seen, ws)
			delete(w.needs, ws)
		}
	}
}

// observe takes a workspace's whole projection and returns the pushes it
// implies, in the projection's order:
//   - waiting: a session enters Prompting, or stays in it with a new reason
//     or a new tool summary, or appears already Prompting after the
//     workspace's baseline;
//   - finished: a started session goes from Running to Ready.
//
// Workspace terminals never push and are not counted.
func (w *pushWatch) observe(ws core.WorkspaceID, views []core.InstanceView) []pushJob {
	prev, baselined := w.seen[ws]
	next := make(map[core.InstanceID]pushSeen, len(views))
	needs := 0
	var jobs []pushJob
	for _, v := range views {
		if v.IsWorkspaceTerminal {
			continue
		}
		now := pushSeen{status: v.Status, reason: v.WaitReason, detail: v.WaitDetail}
		next[v.ID] = now
		if group, _ := groupOf(v); group == "needs_you" {
			needs++
		}
		if !baselined {
			continue
		}
		before, known := prev[v.ID]
		switch {
		case now.status == session.Prompting && (!known || before != now):
			jobs = append(jobs, pushJob{Kind: pushWaiting, Session: v.ID, Title: pushTitle(v), Body: pushWaitBody(v)})
		case known && before.status == session.Running && now.status == session.Ready && v.Started:
			jobs = append(jobs, pushJob{Kind: pushFinished, Session: v.ID, Title: pushTitle(v), Body: pushFinishedBody})
		}
	}
	w.seen[ws], w.needs[ws] = next, needs
	badge := 0
	for _, n := range w.needs {
		badge += n
	}
	for i := range jobs {
		jobs[i].Badge = badge
	}
	return jobs
}

// pushTitle is the notification's title: the session's.
func pushTitle(v core.InstanceView) string {
	if title := pushPlain(v.Title, 80); title != "" {
		return title
	}
	return "loom"
}

// pushWaitBody says why the session waits: Claude's reason, then the tool
// summary of a permission request.
func pushWaitBody(v core.InstanceView) string {
	reason := pushPlain(v.WaitReason, 80)
	detail := pushPlain(v.WaitDetail, 200)
	switch {
	case reason != "" && detail != "":
		return reason + " · " + detail
	case reason != "":
		return reason
	case detail != "":
		return detail
	}
	return "Waiting for you"
}

// pushPlain makes s one line of at most limit runes: control characters and
// runs of white space become one space, and a longer text is cut with "…".
// It keeps a payload far under the 4096-byte record a push may carry.
func pushPlain(s string, limit int) string {
	flat := strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
	if utf8.RuneCountInString(flat) <= limit {
		return flat
	}
	return string([]rune(flat)[:limit-1]) + "…"
}
