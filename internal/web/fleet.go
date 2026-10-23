package web

import (
	"net/url"
	"reflect"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
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

// workspaceJSON is a served workspace. It carries no path. Shown is whether
// the fleet lists it (layout).
type workspaceJSON struct {
	ID    core.WorkspaceID `json:"id"`
	Name  string           `json:"name"`
	Label string           `json:"label"`
	Shown bool             `json:"shown"`
}

type waitJSON struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

type diffJSON struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// prJSON is a session's pull request. URL is its page on GitHub, left out
// unless gh gave an https one.
type prJSON struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Checks string `json:"checks"`
	URL    string `json:"url,omitempty"`
}

// agentJSON is one running subagent, as subagent.View has it.
type agentJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Idle        bool   `json:"idle"`
}

// windowJSON is one plan window: the share used, 0 to 100, and when it resets
// as an RFC 3339 time, "" when the server gave none.
type windowJSON struct {
	Pct      float64 `json:"pct"`
	ResetsAt string  `json:"resets_at"`
}

// usageJSON is one account's plan usage as the app reads it. A window the
// server did not report is null; At is when the sample was taken, which the
// app compares with usage_stale_after_s.
type usageJSON struct {
	Account  string      `json:"account"`
	Plan     string      `json:"plan"`
	FiveHour *windowJSON `json:"five_hour"`
	SevenDay *windowJSON `json:"seven_day"`
	At       string      `json:"at"`
}

// sessionJSON is one session as the app reads it. IDs are numbers: they fit
// in 53 bits, exact in JavaScript. Since is an RFC 3339 time, "" when the
// status was not entered in this process; PR is null without a known PR.
// Worktree and Tmux name what a kill removes, and are left out until the
// instance has them.
type sessionJSON struct {
	ID          core.InstanceID  `json:"id"`
	Workspace   core.WorkspaceID `json:"workspace"`
	Title       string           `json:"title"`
	Branch      string           `json:"branch"`
	Worktree    string           `json:"worktree,omitempty"`
	Tmux        string           `json:"tmux,omitempty"`
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
// number of sessions in needs_you across the workspaces. Usage is each
// account's plan usage, UsageStaleAfterS the age in seconds past which the
// app dims a sample (core.UsageStaleAfter), and QuickReplies the list the app
// offers above its reply box.
type snapshotJSON struct {
	Build            string                             `json:"build"`
	Host             string                             `json:"host"`
	Workspaces       []workspaceJSON                    `json:"workspaces"`
	Sessions         map[core.WorkspaceID][]sessionJSON `json:"sessions"`
	Device           deviceJSON                         `json:"device"`
	Badge            int                                `json:"badge"`
	Notices          []noticeJSON                       `json:"notices"`
	Usage            []usageJSON                        `json:"usage"`
	UsageStaleAfterS int                                `json:"usage_stale_after_s"`
	QuickReplies     []string                           `json:"quick_replies"`
}

// workspacesJSON is the `workspaces` event. Badge is the snapshot's count
// (the sessions in needs_you across the workspaces), sent even at 0 so the
// app can clear its icon: a workspace leaving takes its waiting sessions
// from the count without a `sessions` event.
type workspacesJSON struct {
	Workspaces []workspaceJSON `json:"workspaces"`
	Badge      int             `json:"badge"`
}

// sessionsJSON is the `sessions` event: one workspace's whole list, and the
// same fleet-wide Badge count as the snapshot's, sent even at 0.
type sessionsJSON struct {
	Workspace core.WorkspaceID `json:"workspace"`
	Sessions  []sessionJSON    `json:"sessions"`
	Badge     int              `json:"badge"`
}

// usageEventJSON is the `usage` event: every account's usage, as the snapshot
// has it.
type usageEventJSON struct {
	Usage []usageJSON `json:"usage"`
}

// quickRepliesJSON is the `quick_replies` event, and the answer of
// PUT /api/v1/quick-replies: the list the app offers.
type quickRepliesJSON struct {
	QuickReplies []string `json:"quick_replies"`
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
		Worktree:    v.WorktreePath,
		Tmux:        v.TmuxSession,
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
	return &prJSON{Number: s.PRNumber, State: state, Checks: checks, URL: httpsURL(s.PRURL)}
}

// httpsURL is u when it is an absolute https URL with a host, else "".
func httpsURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host == "" {
		return ""
	}
	return u
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

// usageOf is every account's plan usage as the app reads it, in the
// registry's order (default first): an account whose sample has no plan
// limits, or that has no sample yet, is left out. It is never nil, so an
// empty list compares equal to the one the projection starts with.
func usageOf(v core.AccountsView) []usageJSON {
	out := []usageJSON{}
	for _, name := range v.Names.Names {
		u, ok := v.Usage[name]
		if !ok || !u.Available || u.At.IsZero() {
			continue
		}
		out = append(out, usageJSON{
			Account:  name,
			Plan:     u.Plan,
			FiveHour: windowOf(u.FiveHour),
			SevenDay: windowOf(u.SevenDay),
			At:       u.At.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// windowOf is one window as the app reads it, nil for none.
func windowOf(w *account.Window) *windowJSON {
	if w == nil {
		return nil
	}
	out := &windowJSON{Pct: w.Pct}
	if !w.ResetsAt.IsZero() {
		out.ResetsAt = w.ResetsAt.UTC().Format(time.RFC3339)
	}
	return out
}

// resolvedQuickReplies is the list the app offers: the user's, or the
// defaults while they have set none. It is never nil, so the app always reads
// a list, empty when the user wants no chips.
func resolvedQuickReplies(stored []string) []string {
	if stored == nil {
		return core.DefaultQuickReplies()
	}
	return append([]string{}, stored...)
}

// openNames is the open list's names, in tab order.
func openNames(open []config.Workspace) []string {
	names := make([]string, 0, len(open))
	for _, w := range open {
		names = append(names, w.Name)
	}
	return names
}

// projection is the fleet as the app reads it: the served workspaces, the
// open tabs first in tab order and the rest in serve order, each flagged
// shown or not (layout); each workspace's sessions in the model's display
// order; the accounts' usage; and the quick replies. It takes no lock; the
// hub's mutex guards it. Its slices are replaced, never edited, so a copy of
// one stays valid.
type projection struct {
	workspaces   []workspaceJSON
	sessions     map[core.WorkspaceID][]sessionJSON
	usage        []usageJSON
	quickReplies []string
	// serveOrder is the workspaces as the model serves them, and open the
	// open list's names in tab order: layout builds workspaces from them.
	serveOrder []workspaceJSON
	open       []string
}

func newProjection() *projection {
	return &projection{
		workspaces:   []workspaceJSON{},
		sessions:     map[core.WorkspaceID][]sessionJSON{},
		usage:        []usageJSON{},
		quickReplies: core.DefaultQuickReplies(),
		serveOrder:   []workspaceJSON{},
	}
}

// setWorkspaces replaces the served set. A workspace that stays keeps its
// sessions, a new one starts with none until its views arrive, and one that
// went loses them.
func (p *projection) setWorkspaces(views []core.WorkspaceView) {
	order := make([]workspaceJSON, 0, len(views))
	sessions := make(map[core.WorkspaceID][]sessionJSON, len(views))
	for _, v := range views {
		order = append(order, workspaceOf(v))
		if kept, ok := p.sessions[v.ID]; ok {
			sessions[v.ID] = kept
		} else {
			sessions[v.ID] = []sessionJSON{}
		}
	}
	p.serveOrder, p.sessions = order, sessions
	p.layout()
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
	p.layout()
	return true
}

// setOpen takes the open list's names, in tab order, and reports whether
// they changed.
func (p *projection) setOpen(names []string) bool {
	if slices.Equal(names, p.open) {
		return false
	}
	p.open = slices.Clone(names)
	p.layout()
	return true
}

// setUsage takes the accounts' view and reports whether the usage the app
// reads changed.
func (p *projection) setUsage(v core.AccountsView) bool {
	u := usageOf(v)
	if reflect.DeepEqual(u, p.usage) {
		return false
	}
	p.usage = u
	return true
}

// setQuickReplies takes the stored quick replies (nil while unset) and
// reports whether the list the app offers changed.
func (p *projection) setQuickReplies(stored []string) bool {
	q := resolvedQuickReplies(stored)
	if slices.Equal(q, p.quickReplies) {
		return false
	}
	p.quickReplies = q
	return true
}

// layout puts the workspaces in the order the app lists them, the open tabs
// first in tab order and the rest in serve order, and flags each one the
// fleet shows: an open tab, or a workspace with a session that isn't paused.
// With no open tab the model serves (global mode, or a list never saved)
// every workspace is shown. Names match as the TUI keys its tabs; the global
// workspace, named "", is never a tab.
func (p *projection) layout() {
	at := make(map[string]int, len(p.serveOrder))
	for i, w := range p.serveOrder {
		if w.Name != "" {
			at[w.Name] = i
		}
	}
	placed := make([]bool, len(p.serveOrder))
	out := make([]workspaceJSON, 0, len(p.serveOrder))
	for _, name := range p.open {
		i, ok := at[name]
		if !ok || placed[i] {
			continue
		}
		placed[i] = true
		w := p.serveOrder[i]
		w.Shown = true
		out = append(out, w)
	}
	all := len(out) == 0
	for i, w := range p.serveOrder {
		if placed[i] {
			continue
		}
		w.Shown = all || p.live(w.ID)
		out = append(out, w)
	}
	p.workspaces = out
}

// live reports whether ws holds a session that isn't paused: one in Needs
// you, Your turn or Working.
func (p *projection) live(ws core.WorkspaceID) bool {
	for _, s := range p.sessions[ws] {
		if s.Group != groupPaused {
			return true
		}
	}
	return false
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
