package watch

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/session/github"
)

// PR states besides OPEN, as gh reports them.
const (
	prMerged = "MERGED"
	prClosed = "CLOSED"
)

// The session statuses the rules name besides Ready, spelled as
// session.Status.String() spells them. Loading and Deleting are skipped for
// the tick, and any other string is never acted on.
const (
	statusRunning     = "Running"
	statusPaused      = "Paused"
	statusPrompting   = "Prompting"
	statusRecoverable = "Recoverable"
)

// OwnPR reports whether pr is the watcher's PR for issue (spec §2): its head
// branch matches pattern (the title template's branch pattern for the issue),
// it closes the issue, it is not from a fork, and its author is viewer, the
// watcher's own gh identity. All four: a PR the owner opened by hand on their
// own branch is never adopted. An empty viewer owns nothing.
func OwnPR(pr github.WatchPR, issue int, viewer string, pattern *regexp.Regexp) bool {
	if pattern == nil || !pattern.MatchString(pr.HeadRefName) {
		return false
	}
	if !slices.Contains(pr.Closes, issue) {
		return false
	}
	if pr.CrossRepo {
		return false
	}
	return viewer != "" && strings.EqualFold(pr.Author, viewer)
}

// OwnSession reports whether s is a watcher session: its title parses with
// the title template to an issue, and the session's issue field is that
// issue. Any other session of the issue is a foreign one.
func OwnSession(s Session, tmpl TitleTemplate) bool {
	issue, _, ok := tmpl.Parse(s.Title)
	return ok && issue > 0 && issue == s.Issue
}

// Decide is the rule table of spec §4.1: for each issue in the snapshot, in
// number order, the first rule that matches decides, and its actions come
// back in the order the rule lists them. Decide does no I/O and changes
// nothing it is given.
func Decide(s Snapshot, mem Memory, cfg Config, tmpl TitleTemplate) []Action {
	issues := slices.Clone(s.Issues)
	slices.SortFunc(issues, func(a, b github.WatchIssue) int { return cmp.Compare(a.Number, b.Number) })

	r := &ruleRun{snap: s, mem: mem, cfg: cfg, tmpl: tmpl}
	for _, sess := range s.Sessions {
		if OwnSession(sess, tmpl) {
			r.owned++
		}
	}

	var out []Action
	for _, is := range issues {
		out = append(out, r.issue(is)...)
	}
	return out
}

// ruleRun is one Decide call's state: what it reads, how many watcher
// sessions the cap counts, and whether the call's one start has gone out.
type ruleRun struct {
	snap    Snapshot
	mem     Memory
	cfg     Config
	tmpl    TitleTemplate
	owned   int
	started bool
}

// issue applies the rules to one issue.
func (r *ruleRun) issue(is github.WatchIssue) []Action {
	n := is.Number
	pattern := r.tmpl.BranchPattern(r.snap.Prefix, n)

	var prs []github.WatchPR
	for _, pr := range r.snap.PRs {
		if OwnPR(pr, n, r.snap.Viewer, pattern) {
			prs = append(prs, pr)
		}
	}
	slices.SortFunc(prs, func(a, b github.WatchPR) int { return cmp.Compare(a.Number, b.Number) })

	var open, unsettled []github.WatchPR
	merged := false
	for _, pr := range prs {
		switch pr.State {
		case prOpen:
			open = append(open, pr)
		case prMerged:
			merged = true
		case prClosed:
			if !r.mem.PRs[pr.Number].Settled {
				unsettled = append(unsettled, pr)
			}
		}
	}

	var sessions []Session
	for _, sess := range r.snap.Sessions {
		if sess.Issue == n && OwnSession(sess, r.tmpl) {
			sessions = append(sessions, sess)
		}
	}
	slices.SortFunc(sessions, func(a, b Session) int { return cmp.Compare(a.ID, b.ID) })

	switch {
	case merged && len(open) == 0: // rule 1
		return kills(n, sessions)
	case is.Closed && len(open) > 0: // rule 2
		reason := fmt.Sprintf("The watcher is closing this PR because issue #%d is closed.", n)
		return append(closePRs(n, open, reason), kills(n, sessions)...)
	case is.Closed: // rule 3
		return kills(n, sessions)
	case hasLabel(is, r.cfg.EscalateLabel): // rule 4
		return nil
	case len(unsettled) > 0: // rule 5
		first := unsettled[0].Number
		esc := Action{
			Kind:   ActEscalate,
			Issue:  n,
			PR:     first,
			Reason: fmt.Sprintf("PR #%d was closed unmerged; the watcher will not take this issue again until %s is removed", first, r.cfg.EscalateLabel),
		}
		for _, pr := range unsettled {
			esc.Settle = append(esc.Settle, pr.Number)
		}
		if len(sessions) > 0 {
			esc.Session = sessions[0].ID
		}
		closeReason := fmt.Sprintf("The watcher is closing this PR: PR #%d was closed unmerged, so the watcher will not take issue #%d again until %s is removed.", first, n, r.cfg.EscalateLabel)
		out := append([]Action{esc}, closePRs(n, open, closeReason)...)
		return append(out, kills(n, sessions)...)
	}
	return nil // rules 6 to 9 follow in Tasks 5.3 and 5.4
}

// actionable reports whether the rules may act on a session of this status.
// Loading and Deleting sessions are skipped for the tick, and an unknown
// status is never acted on: fail closed.
func actionable(status string) bool {
	switch status {
	case statusRunning, statusReady, statusPaused, statusPrompting, statusRecoverable:
		return true
	}
	return false
}

// kills is one ActKill for each of the sessions the rules may act on.
func kills(n int, sessions []Session) []Action {
	var out []Action
	for _, sess := range sessions {
		if actionable(sess.Status) {
			out = append(out, Action{Kind: ActKill, Issue: n, Session: sess.ID})
		}
	}
	return out
}

// closePRs is one ActClosePR, with the same comment, for each PR.
func closePRs(n int, prs []github.WatchPR, reason string) []Action {
	var out []Action
	for _, pr := range prs {
		out = append(out, Action{Kind: ActClosePR, Issue: n, PR: pr.Number, Reason: reason})
	}
	return out
}

// hasLabel reports whether the issue carries the label, compared without
// case as GitHub does.
func hasLabel(is github.WatchIssue, label string) bool {
	return slices.ContainsFunc(is.Labels, func(l string) bool { return strings.EqualFold(l, label) })
}
