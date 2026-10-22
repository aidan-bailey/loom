package web

import (
	"net/url"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// The projection turns the replica's views into what the app reads. The app
// maps nothing itself: the group, the label, the PR words and the Claude
// link are decided here, once, from the model's own facts, so the phone shows
// the status the TUI shows.

// remoteBase is where a session's bridge ID opens in Claude.
const remoteBase = "https://claude.ai/code/"

// The groups the app lists, as the wire spells them.
const (
	groupNeedsYou = "needs_you"
	groupYourTurn = "your_turn"
	groupWorking  = "working"
	groupPaused   = "paused"
)

// workspaceJSON is a served workspace. It carries no path.
type workspaceJSON struct {
	ID    core.WorkspaceID `json:"id"`
	Name  string           `json:"name"`
	Label string           `json:"label"`
}

type waitJSON struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

type diffJSON struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type prJSON struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Checks string `json:"checks"`
}

// agentJSON is one running subagent, as subagent.View has it.
type agentJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Idle        bool   `json:"idle"`
}

// sessionJSON is one session as the app reads it. IDs are numbers: they fit
// in 53 bits, exact in JavaScript. Since is an RFC 3339 time, "" when the
// status was not entered in this process; PR is null without a known PR.
type sessionJSON struct {
	ID          core.InstanceID  `json:"id"`
	Workspace   core.WorkspaceID `json:"workspace"`
	Title       string           `json:"title"`
	Branch      string           `json:"branch"`
	Group       string           `json:"group"`
	Label       string           `json:"label"`
	Status      string           `json:"status"`
	Since       string           `json:"since"`
	Wait        waitJSON         `json:"wait"`
	LastMessage string           `json:"last_message"`
	Diff        diffJSON         `json:"diff"`
	Ahead       int              `json:"ahead"`
	Behind      int              `json:"behind"`
	ParityKnown bool             `json:"parity_known"`
	PR          *prJSON          `json:"pr"`
	Account     string           `json:"account"`
	Agents      []agentJSON      `json:"agents"`
	RemoteURL   string           `json:"remote_url"`
}

// notifyJSON is a device's notification settings. PausedUntil is an RFC 3339
// time, "" when they are not paused.
type notifyJSON struct {
	Waiting     bool   `json:"waiting"`
	Finished    bool   `json:"finished"`
	PausedUntil string `json:"paused_until"`
}

// deviceJSON is a device as its own app reads it: no token, no pairing or
// last-seen time. It is comparable, so a stream can tell when it changed.
type deviceJSON struct {
	Name   string     `json:"name"`
	Notify notifyJSON `json:"notify"`
	Push   string     `json:"push"`
}

// noticeJSON is a line for the app's toast and its list of recent notices.
type noticeJSON struct {
	Text  string    `json:"text"`
	Error bool      `json:"error"`
	At    time.Time `json:"at"`
}

// snapshotJSON is the first event of every stream: everything the app shows,
// so nothing is replayed. Sessions is keyed by workspace ID, and Badge is the
// number of sessions in needs_you across the workspaces.
type snapshotJSON struct {
	Build      string                             `json:"build"`
	Host       string                             `json:"host"`
	Workspaces []workspaceJSON                    `json:"workspaces"`
	Sessions   map[core.WorkspaceID][]sessionJSON `json:"sessions"`
	Device     deviceJSON                         `json:"device"`
	Badge      int                                `json:"badge"`
	Notices    []noticeJSON                       `json:"notices"`
}

// workspacesJSON is the `workspaces` event.
type workspacesJSON struct {
	Workspaces []workspaceJSON `json:"workspaces"`
}

// sessionsJSON is the `sessions` event: one workspace's whole list.
type sessionsJSON struct {
	Workspace core.WorkspaceID `json:"workspace"`
	Sessions  []sessionJSON    `json:"sessions"`
}

// groupOf says which of the app's four groups a session is listed under and
// what it is called there. Prompting needs the person; a started Ready is
// their turn; Running, Loading and Deleting are the agent's; the rest wait.
// A status this build does not know is listed last under its own name, never
// as one it might be.
func groupOf(v core.InstanceView) (group, label string) {
	switch v.Status {
	case session.Prompting:
		return groupNeedsYou, "Needs you"
	case session.Ready:
		if v.Started {
			return groupYourTurn, "Your turn"
		}
		return groupPaused, "Not started"
	case session.Running:
		return groupWorking, "Working"
	case session.Loading:
		return groupWorking, "Loading"
	case session.Deleting:
		return groupWorking, "Deleting"
	case session.Paused:
		return groupPaused, "Paused"
	case session.Recoverable:
		return groupPaused, "Recoverable"
	}
	return groupPaused, v.Status.String()
}

// sessionOf is the view of one instance of workspace ws as the app reads it.
// A fact the view does not vouch for (a last message without HasLastMessage,
// a diff without HasDiff, ahead and behind without ParityKnown) is left at
// its zero value rather than shown stale.
func sessionOf(ws core.WorkspaceID, v core.InstanceView) sessionJSON {
	group, label := groupOf(v)
	s := sessionJSON{
		ID:          v.ID,
		Workspace:   ws,
		Title:       v.Title,
		Branch:      v.Branch,
		Group:       group,
		Label:       label,
		Status:      v.Status.String(),
		Wait:        waitJSON{Reason: v.WaitReason, Detail: v.WaitDetail},
		PR:          prOf(v.GitHub),
		Account:     v.Account,
		Agents:      make([]agentJSON, 0, len(v.Subagents)),
		ParityKnown: v.ParityKnown,
	}
	if !v.StatusSince.IsZero() {
		s.Since = v.StatusSince.UTC().Format(time.RFC3339)
	}
	if v.HasLastMessage {
		s.LastMessage = v.LastMessage
	}
	if v.HasDiff {
		s.Diff = diffJSON{Added: v.Diff.Added, Removed: v.Diff.Removed}
	}
	if v.ParityKnown {
		s.Ahead, s.Behind = v.Ahead, v.Behind
	}
	for _, a := range v.Subagents {
		s.Agents = append(s.Agents, agentJSON{Name: a.Name, Description: a.Description, Idle: a.Idle})
	}
	if v.RemoteSession != "" {
		s.RemoteURL = remoteBase + url.PathEscape(v.RemoteSession)
	}
	return s
}

// prOf is the session's pull request, nil unless GitHub's answer for its
// repository is known, names a PR, and has a state and checks this build
// knows. The app shows no PR rather than a guessed one.
func prOf(s github.State) *prJSON {
	if !s.Known || !s.HasPR {
		return nil
	}
	var state, checks string
	switch s.PRState {
	case github.PRDraft:
		state = "draft"
	case github.PROpen:
		state = "open"
	case github.PRMerged:
		state = "merged"
	case github.PRClosed:
		state = "closed"
	default:
		return nil
	}
	switch s.Checks {
	case github.ChecksNone:
		checks = "none"
	case github.ChecksPending:
		checks = "pending"
	case github.ChecksPassing:
		checks = "passing"
	case github.ChecksFailing:
		checks = "failing"
	default:
		return nil
	}
	return &prJSON{Number: s.PRNumber, State: state, Checks: checks}
}

// workspaceOf is a served workspace as the app reads it.
func workspaceOf(w core.WorkspaceView) workspaceJSON {
	return workspaceJSON{ID: w.ID, Name: w.Name, Label: w.Label}
}

// deviceOf is a device record as its own app reads it.
func deviceOf(d core.WebDevice) deviceJSON {
	n := notifyJSON{Waiting: d.Notify.Waiting, Finished: d.Notify.Finished}
	if !d.Notify.PausedUntil.IsZero() {
		n.PausedUntil = d.Notify.PausedUntil.UTC().Format(time.RFC3339)
	}
	return deviceJSON{Name: d.Name, Notify: n, Push: string(d.Push)}
}

// noticeOf is a model notice as the app reads it, stamped with the time the
// web layer saw it, in UTC like the other times: Err is shown as an error,
// else Info as a line.
func noticeOf(n core.Notice, at time.Time) noticeJSON {
	at = at.UTC()
	if n.Err != nil {
		return noticeJSON{Text: n.Err.Error(), Error: true, At: at}
	}
	return noticeJSON{Text: n.Info, At: at}
}

// projection is the fleet as the app reads it: the served workspaces in
// serve order, and each one's sessions in the model's display order. It takes
// no lock; the hub's mutex guards it. Its slices are replaced, never edited,
// so a copy of one stays valid.
type projection struct {
	workspaces []workspaceJSON
	sessions   map[core.WorkspaceID][]sessionJSON
}

func newProjection() *projection {
	return &projection{
		workspaces: []workspaceJSON{},
		sessions:   map[core.WorkspaceID][]sessionJSON{},
	}
}

// setWorkspaces replaces the served set. A workspace that stays keeps its
// sessions, a new one starts with none until its views arrive, and one that
// went loses them.
func (p *projection) setWorkspaces(views []core.WorkspaceView) {
	workspaces := make([]workspaceJSON, 0, len(views))
	sessions := make(map[core.WorkspaceID][]sessionJSON, len(views))
	for _, v := range views {
		workspaces = append(workspaces, workspaceOf(v))
		if kept, ok := p.sessions[v.ID]; ok {
			sessions[v.ID] = kept
		} else {
			sessions[v.ID] = []sessionJSON{}
		}
	}
	p.workspaces, p.sessions = workspaces, sessions
}

// setViews replaces one workspace's sessions, leaving workspace terminals
// out. It reports false, and changes nothing, for a workspace that is not
// served: the replica drops such views too.
func (p *projection) setViews(ws core.WorkspaceID, views []core.InstanceView) bool {
	if _, ok := p.sessions[ws]; !ok {
		return false
	}
	out := make([]sessionJSON, 0, len(views))
	for _, v := range views {
		if v.IsWorkspaceTerminal {
			continue
		}
		out = append(out, sessionOf(ws, v))
	}
	p.sessions[ws] = out
	return true
}

// served reports whether ws is in the served set.
func (p *projection) served(ws core.WorkspaceID) bool {
	_, ok := p.sessions[ws]
	return ok
}

// badge is the number of sessions that need the person, across the served
// workspaces: the number the app puts on its icon.
func (p *projection) badge() int {
	n := 0
	for _, list := range p.sessions {
		for _, s := range list {
			if s.Group == groupNeedsYou {
				n++
			}
		}
	}
	return n
}
