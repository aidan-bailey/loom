package core

import (
	"time"

	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/subagent"
)

// InstanceID names one record: a hash of its workspace's canonical config
// dir, its title and its creation time (idOf), so every daemon over one
// disk gives it the same ID and a client that rejoins another daemon keeps
// naming the same sessions. A request naming an ID can't reach another
// instance: a same-titled one in another workspace differs by config dir,
// and one created under the title of an instance killed meanwhile by
// creation time (a collision between two live records probes to the next
// free value). 0 is never assigned; the TUI uses it for a draft row.
type InstanceID uint64

// InstanceView is an instance as a client sees it: a value copied out of the
// model (viewOf) carrying everything the TUI renders or gates a key on, and
// nothing that acts. Clients change an instance only through requests that
// name its ID. The model publishes each workspace's views when they change
// (Sync, ViewsChanged).
type InstanceView struct {
	ID    InstanceID
	Title string
	// RepoPath is the repository the session works in (Instance.Path).
	RepoPath     string
	WorktreePath string
	// WorktreeRepoPath is the repository root its worktree belongs to, as
	// git resolved it when the worktree was made (GitWorktree.GetRepoPath):
	// RepoPath through symlinks, or the root above a subdirectory. "" until
	// the instance has started, and for a workspace terminal. Lua's
	// worktree handle is built on it.
	WorktreeRepoPath string
	Branch           string
	// TmuxSession is the agent's tmux session name ("" before it has one);
	// pane clients attach by it. SessionProgram is the program that session
	// was launched with, which picks the client's adapter (its
	// pending-prompt pattern); Program can already name a new one after R.
	TmuxSession    string
	SessionProgram string
	Program        string
	Status         session.Status
	// StatusSince is when Status was entered (zero: not this process).
	StatusSince time.Time
	// StatusReported is set when Claude's hooks or roster have an opinion
	// on the status (Instance.ClaudeStatus). Without one the TUI may show its
	// own pane-scraped status instead (its ladder overlay).
	StatusReported bool
	WaitReason     string
	// WaitDetail is what the permission request behind a Prompting session
	// asks to do, in one line (Instance.WaitDetail): a Bash command's first
	// line, a file path or a URL. Empty for any other wait.
	WaitDetail string
	// Asks are the dialogs the session's Claude holds open that the phone
	// can answer (core/asks.go), oldest first; nil for none.
	Asks []AskView
	// Mockup is the session's brainstorm companion as the model last found
	// it (core/mockup.go); Screen "" means none.
	Mockup MockupView
	// Flows are the flows of the session's worktree as the model last
	// found them (core/flows.go), newest first; nil for none.
	Flows []FlowSummary
	// RemoteSession is the ID in the session's Remote Control link,
	// https://claude.ai/code/<RemoteSession>. The model takes it from the
	// roster's entry for the worktree (session.RosterEntry.RemoteSession),
	// so it is empty whenever the roster has none and clears when the roster
	// does, as the roster's status does.
	RemoteSession  string
	LastMessage    string
	HasLastMessage bool
	// Verdict is the stop classifier's verdict on the turn LastMessage
	// ended (attention.NeedsYou, Done, Waiting or Stopped), "" for none;
	// VerdictGist is its one line. VerdictPending is set while a call for
	// that turn waits or runs, and VerdictStop names the turn (its Stop's
	// time), so a client acts once per turn. All are zero unless
	// HasLastMessage.
	Verdict        string
	VerdictGist    string
	VerdictPending bool
	VerdictStop    time.Time
	Subagents      []subagent.View
	// Diff is the latest diff stats, valid when HasDiff.
	Diff                git.DiffStats
	HasDiff             bool
	GitHub              github.State
	Ahead, Behind       int
	ParityKnown         bool
	Issue               int
	Account             string
	HeadroomProxy       bool
	CacheTTL1h          bool
	IsWorkspaceTerminal bool
	Started             bool
	// Bell is the TUI's own overlay (a bell rang in the pane since it was
	// last focused). The model never sets it.
	Bell bool
}

// FlowSummary is one of a session's flows (flowview.Summary) with what
// only the model knows: Lost when its run says it runs while its engine
// job has ended or left the queue, and Spent, the run's last priced total
// (valid when Priced).
type FlowSummary struct {
	flowview.Summary
	Lost   bool
	Spent  float64
	Priced bool
}

// FlowView is ReadFlow's answer: a flow's board, its cost box, and the
// times of its steps' jobs that the queue still holds.
type FlowView struct {
	Detail flowview.FlowDetail
	Costs  flowview.Costs
	Times  map[sched.JobID]JobTimes
}

// JobTimes is a job's start and end, zero until each happens, and why it
// waits while it is queued or held (the queue view's JobView.Waiting), so a
// waiting step can say why (usage-limit hold spec §2).
type JobTimes struct {
	Started time.Time
	Ended   time.Time
	Waiting string
}

// FlowsView is ReadFlows' answer: every served session's flows and the
// host's past runs, newest first.
type FlowsView struct {
	Live    []LiveFlow
	History []flowview.PastRun
}

// LiveFlow is one session's flow, with the session it belongs to.
type LiveFlow struct {
	Instance InstanceID
	Title    string
	Flow     FlowSummary
}

// MockupView is the screen a session's brainstorm companion serves and its
// mtime. Fresh: the screen changed at or after the start of the session's
// latest turn (Instance.TurnStart), so a new turn leaves it behind; never
// fresh when this process has seen no turn start.
type MockupView struct {
	Screen  string
	Changed time.Time
	Fresh   bool
}

// Paused reports whether the view's status is Paused.
func (v InstanceView) Paused() bool { return v.Status == session.Paused }

// StatusAge is how long the instance has been in its status, 0 when no
// transition was observed this process (as Instance.StatusAge).
func (v InstanceView) StatusAge() time.Duration {
	if v.StatusSince.IsZero() {
		return 0
	}
	return time.Since(v.StatusSince)
}

// Active reports whether the instance is running for the purposes of the
// background jobs and the TUI's pane clients: started, and not Paused,
// Deleting, Recoverable or Loading. It is activeInstance's and
// statusEligible's rule, for a view.
func (v InstanceView) Active() bool {
	switch v.Status {
	case session.Paused, session.Deleting, session.Recoverable, session.Loading:
		return false
	}
	return v.Started
}

// NeedsYou reports whether v waits on the person: a dialog Claude holds
// open (Prompting), or a stopped turn whose last message asks for them (the
// stop classifier's needs_you). The TUI's attention, the overview's jump,
// the tab bar, the PWA's group and its pushes all read it.
func (v InstanceView) NeedsYou() bool {
	return v.Status == session.Prompting || (v.Status == session.Ready && v.Verdict == attention.NeedsYou)
}
