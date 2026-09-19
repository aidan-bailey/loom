// Package github reads GitHub state through the gh CLI for loom's
// session cards and issue picker. It is standalone: no app, ui, or
// session imports. Every entry point fails closed — when gh is
// missing, unauthenticated, or offline the caller gets an error and
// renders nothing, never a guess.
package github

import "time"

// PRState is the folded pull-request lifecycle state.
type PRState int

const (
	PRNone PRState = iota
	PRDraft
	PROpen
	PRMerged
	PRClosed
)

// Review is the folded review decision on a pull request.
type Review int

const (
	ReviewNone Review = iota
	ReviewApproved
	ReviewChangesRequested
)

// Checks is the folded CI status of a pull request's head commit.
type Checks int

const (
	ChecksNone Checks = iota
	ChecksPending
	ChecksPassing
	ChecksFailing
)

// PR is one pull request keyed by its head branch in Snapshot.PRs.
type PR struct {
	Number int
	State  PRState
	Review Review
	Checks Checks
}

// Issue is one GitHub issue. Body is empty in list results and
// populated by View.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string
	Closed bool
}

// Snapshot is one repo's GitHub state at FetchedAt.
type Snapshot struct {
	PRs       map[string]PR // keyed by head branch name
	Issues    map[int]Issue // keyed by issue number
	FetchedAt time.Time
}

// State is the per-session join of a Snapshot, ready for rendering.
// Known=false means the repo was not (successfully) queried and the UI
// must render nothing; Known=true with HasPR=false means "no PR" —
// the two must never be conflated.
type State struct {
	Known       bool
	IssueNumber int
	// IssueTitle is empty until a snapshot resolves the number, so ""
	// means "not resolved yet", not "an issue with a blank title".
	// IssueClosed is likewise only meaningful once the title is set.
	IssueTitle  string
	IssueClosed bool
	HasPR       bool
	PRNumber    int
	// PRState, Review and Checks are meaningless unless HasPR: they sit
	// at their None zero values for a session with no PR, which is not
	// a state any real PR can be in. Always branch on HasPR first.
	PRState PRState
	Review  Review
	Checks  Checks
}

// StateFor joins snap onto one session: its PR by branch, its issue by
// the linked number (0 = none). known reports whether snap is real.
func StateFor(snap Snapshot, known bool, branch string, issue int) State {
	if !known {
		return State{}
	}
	s := State{Known: true, IssueNumber: issue}
	if pr, ok := snap.PRs[branch]; ok {
		s.HasPR = true
		s.PRNumber = pr.Number
		s.PRState = pr.State
		s.Review = pr.Review
		s.Checks = pr.Checks
	}
	if issue != 0 {
		if is, ok := snap.Issues[issue]; ok {
			s.IssueTitle = is.Title
			s.IssueClosed = is.Closed
		}
	}
	return s
}
