package web

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
)

// The decisions behind a push: which transition of which session is one, and
// what it says. push.go owns the queue, the sender and the route.

const (
	pushWaiting  = "waiting"
	pushFinished = "finished"
	pushDone     = "done"
)

// pushFinishedBody is what a finished push says: the session's last message
// stays out of it, since a lock screen shows the body to whoever holds the
// phone. A done push shows the stop classifier's one-line gist instead
// (pushDoneBody), the owner's choice (stop verdicts spec §2).
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
	// Mockup: the push opens the session's mockup tab (opensMockup).
	Mockup bool
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
	url := fmt.Sprintf("/#s/%d", j.Session)
	if j.Mockup {
		url += "/mockup"
	}
	return json.Marshal(pushPayload{
		Kind:    j.Kind,
		Session: j.Session,
		Title:   j.Title,
		Body:    j.Body,
		Badge:   j.Badge,
		URL:     url,
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

// pushSeen is what the last projection showed of one session, and what its
// current wait has pushed: pushedReason and pushedDetail are the last
// non-empty reason and tool summary a Prompting stretch pushed (or began
// with, after a baseline), which a later projection is compared with.
type pushSeen struct {
	status       session.Status
	pushedReason string
	pushedDetail string
	// owed is set while a Running→Ready edge waits on its turn's verdict
	// (VerdictPending); judged is the last turn (VerdictStop) whose verdict
	// the watch acted on, or saw in a baseline.
	owed   bool
	judged time.Time
}

// pushWatch works out the pushes a projection implies by comparing each
// session with the last one it saw. The first projection of a workspace is a
// baseline and implies nothing, so a daemon restart sends no flood. It is
// touched by one goroutine, the drain's.
type pushWatch struct {
	seen  map[core.WorkspaceID]map[core.InstanceID]pushSeen
	needs map[core.WorkspaceID]int
	// since is when each workspace's baseline was taken: a turn that ended
	// before it never pushes, since a restart replays every session's last
	// turn and its verdict comes back after the baseline.
	since map[core.WorkspaceID]time.Time
	now   func() time.Time
}

func newPushWatch() *pushWatch {
	return &pushWatch{
		seen:  map[core.WorkspaceID]map[core.InstanceID]pushSeen{},
		needs: map[core.WorkspaceID]int{},
		since: map[core.WorkspaceID]time.Time{},
		now:   time.Now,
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
			delete(w.since, ws)
		}
	}
}

// observe takes a workspace's whole projection and returns the pushes it
// implies, in the projection's order:
//   - waiting: a session enters Prompting, or stays in it with a non-empty
//     reason or tool summary that differs from the last non-empty one the
//     stretch pushed, or appears already Prompting after the workspace's
//     baseline. A reason or summary that is cleared, or returns to the one
//     already pushed, is the same wait; leaving Prompting ends the stretch.
//     A needs_you verdict on a turn pushes waiting too;
//   - done: a done verdict on a turn;
//   - finished: a started session goes from Running to Ready. While the stop
//     classifier judges the turn (VerdictPending) the push is owed instead,
//     and the verdict decides it: finished for stopped or for no verdict at
//     all, none for waiting, and dropped when the session leaves Ready first.
//
// A turn's verdict (VerdictStop) pushes at most once, and a turn that ended
// before the workspace's baseline never pushes. Workspace terminals never
// push and are not counted.
func (w *pushWatch) observe(ws core.WorkspaceID, views []core.InstanceView) []pushJob {
	prev, baselined := w.seen[ws]
	if !baselined {
		w.since[ws] = w.now()
	}
	since := w.since[ws]
	next := make(map[core.InstanceID]pushSeen, len(views))
	needs := 0
	var jobs []pushJob
	for _, v := range views {
		if v.IsWorkspaceTerminal {
			continue
		}
		before, known := prev[v.ID]
		now := pushSeen{status: v.Status, owed: before.owed, judged: before.judged}
		entering := v.Status == session.Prompting && (!known || before.status != session.Prompting)
		changed := false
		switch {
		case entering:
			now.pushedReason, now.pushedDetail = v.WaitReason, v.WaitDetail
		case v.Status == session.Prompting:
			now.pushedReason, now.pushedDetail = before.pushedReason, before.pushedDetail
			if v.WaitReason != "" && v.WaitReason != before.pushedReason {
				now.pushedReason, changed = v.WaitReason, true
			}
			if v.WaitDetail != "" && v.WaitDetail != before.pushedDetail {
				now.pushedDetail, changed = v.WaitDetail, true
			}
		}
		if group, _ := groupOf(v); group == groupNeedsYou {
			needs++
		}
		if !baselined {
			if v.VerdictStop.After(now.judged) {
				now.judged = v.VerdictStop
			}
			next[v.ID] = now
			continue
		}
		finished := known && before.status == session.Running && v.Status == session.Ready && v.Started
		switch {
		case entering || changed:
			jobs = append(jobs, pushJob{Kind: pushWaiting, Session: v.ID, Title: pushTitle(v), Body: pushWaitBody(v), Mockup: opensMockup(v)})
		case finished && (v.VerdictPending || v.Verdict != ""):
			now.owed = true
		case finished:
			jobs = append(jobs, finishedJob(v))
		}
		if v.Status != session.Ready {
			now.owed = false
		}
		if job, ok := verdictPush(v, &now, since); ok {
			jobs = append(jobs, job)
		}
		next[v.ID] = now
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

// finishedJob is v's finished push.
func finishedJob(v core.InstanceView) pushJob {
	return pushJob{Kind: pushFinished, Session: v.ID, Title: pushTitle(v), Body: pushFinishedBody, Mockup: opensMockup(v)}
}

// verdictPush is the push v's verdict implies, at most one per turn. A
// verdict on a Ready session's turn the watch has not acted on (VerdictStop
// after seen.judged) pushes as its kind says (needs_you waiting, done done,
// stopped finished when the turn owed a push, waiting nothing), unless the
// turn ended before the workspace's baseline (since). A pending call that
// ended with no verdict pays an owed push as finished. It updates seen.
func verdictPush(v core.InstanceView, seen *pushSeen, since time.Time) (pushJob, bool) {
	switch {
	case v.Status == session.Ready && v.Verdict != "" && v.VerdictStop.After(seen.judged):
		owed := seen.owed
		seen.judged, seen.owed = v.VerdictStop, false
		if v.VerdictStop.Before(since) {
			return pushJob{}, false
		}
		switch v.Verdict {
		case attention.NeedsYou:
			return pushJob{Kind: pushWaiting, Session: v.ID, Title: pushTitle(v), Body: pushWaitBody(v), Mockup: opensMockup(v)}, true
		case attention.Done:
			return pushJob{Kind: pushDone, Session: v.ID, Title: pushTitle(v), Body: pushDoneBody(v), Mockup: opensMockup(v)}, true
		case attention.Stopped:
			if owed {
				return finishedJob(v), true
			}
		}
	case seen.owed && !v.VerdictPending && v.Verdict == "":
		seen.owed = false
		return finishedJob(v), true
	}
	return pushJob{}, false
}

// opensMockup reports whether a push for v opens its mockup tab: its
// brainstorm companion's screen changed in the session's latest turn and
// no dialog waits on the session view, where it is answered. The app's
// fleet row follows the same rule (opensMockup in static/js/fleet.mjs).
func opensMockup(v core.InstanceView) bool {
	return v.Mockup.Screen != "" && v.Mockup.Fresh && len(v.Asks) == 0
}

// pushTitle is the notification's title: the session's.
func pushTitle(v core.InstanceView) string {
	if title := pushPlain(v.Title, 80); title != "" {
		return title
	}
	return "loom"
}

// pushWaitBody says why the session waits: what a stopped turn asks (the
// stop classifier's gist), else Claude's reason, then the tool summary of a
// permission request.
func pushWaitBody(v core.InstanceView) string {
	if v.Status == session.Ready && v.Verdict == attention.NeedsYou {
		if gist := pushPlain(v.VerdictGist, attention.MaxGist); gist != "" {
			return "asks: " + gist
		}
		return "Asks you"
	}
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

// pushDoneBody says what a done session finished: the stop classifier's
// gist, which a lock screen shows (stop verdicts spec §2).
func pushDoneBody(v core.InstanceView) string {
	if gist := pushPlain(v.VerdictGist, attention.MaxGist); gist != "" {
		return "Done: " + gist
	}
	return "Done"
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
