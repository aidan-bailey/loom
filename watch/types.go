package watch

import (
	"time"

	"github.com/aidan-bailey/loom/session/github"
)

// Session is one loom session as the watcher sees it, the fields of a
// `loom session list --json` row the rules read. Status is exactly
// session.Status.String(): Running, Ready, Loading, Paused, Prompting,
// Deleting or Recoverable. Decide skips any other string.
type Session struct {
	ID     uint64
	Title  string
	Status string
	Since  time.Time
	Branch string
	Issue  int
}

// Nudge is a message for a session whose PR needs something. Kind selects
// the repeat guard's slot and the count's, Key is the fingerprint of what the
// nudge reports, and Text is what the session is told. Text never holds the
// nudge-count suffix or, for a ci nudge, the log tails: the action that sends
// it appends both.
type Nudge struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Text string `json:"text"`
}

// ActionKind names what the watcher does for an issue.
type ActionKind string

// The kinds of Action.
const (
	ActStart    ActionKind = "start"
	ActKill     ActionKind = "kill"
	ActClosePR  ActionKind = "close_pr"
	ActSend     ActionKind = "send"
	ActEscalate ActionKind = "escalate"
	ActWait     ActionKind = "wait"
)

// Action is one thing Decide wants done. Which fields are set depends on
// Kind:
//   - ActStart: Issue; a resume of an open PR's branch adds Resume, Branch
//     and PR.
//   - ActKill: Issue and Session.
//   - ActClosePR: Issue, PR and Reason, the comment to close it with.
//   - ActSend: Issue, Session, PR, Nudge and N, the count after this send.
//   - ActEscalate: Issue and Reason, and Session and PR when known. One
//     from NudgeFor also carries Nudge (Kind and Key) and N, the limit.
//     One for a PR closed unmerged carries Settle, those PRs' numbers.
//   - ActWait: Issue and Reason, why the issue waits.
type Action struct {
	Kind    ActionKind `json:"kind"`
	Issue   int        `json:"issue"`
	Session uint64     `json:"session,omitempty"`
	PR      int        `json:"pr,omitempty"`
	Branch  string     `json:"branch,omitempty"`
	Resume  bool       `json:"resume,omitempty"`
	Nudge   Nudge      `json:"nudge,omitzero"`
	N       int        `json:"n,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	Settle  []int      `json:"settle,omitempty"`
}

// Snapshot is everything Decide reads for one workspace in one tick.
// Issues holds every issue Decide rules on (the labelled open ones and the
// closed ones a session or PR names); an issue a session or PR names that is
// absent is skipped, never guessed. HookNudges and HookFailed are keyed by
// issue number; Decide reads HookNudges only, and HookFailed is for status.
// Considered and Unconsidered are for status too.
type Snapshot struct {
	Now          time.Time
	Viewer       string
	Prefix       string
	Issues       []github.WatchIssue
	PRs          []github.WatchPR
	Sessions     []Session
	HookNudges   map[int][]Nudge
	HookFailed   map[int]string
	Considered   int
	Unconsidered int
}
