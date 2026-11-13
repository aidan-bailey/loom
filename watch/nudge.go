package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/github"
)

// statusReady and prOpen are the strings loom and gh use for the one session
// status and the one PR state NudgeFor acts on.
const (
	statusReady = "Ready"
	prOpen      = "OPEN"
)

// The built-in nudge kinds. A hook may not use these names.
const (
	kindConflict     = "conflict"
	kindCI           = "ci"
	kindIssueChanged = "issue-changed"
)

// IssueSeen fingerprints what an issue's author and maintainers have said:
// the hex SHA-256 of the body, a colon, and the ID of the newest comment by
// an OWNER, MEMBER or COLLABORATOR (empty when there is none). A comment by
// anyone else never changes it, so a stranger cannot make the watcher nudge.
func IssueSeen(is github.WatchIssue) string {
	sum := sha256.Sum256([]byte(is.Body))
	var (
		id     string
		newest time.Time
		found  bool
	)
	for _, c := range is.Comments {
		if !trustedAssociation(c.Association) {
			continue
		}
		if !found || c.CreatedAt.After(newest) {
			id, newest, found = c.ID, c.CreatedAt, true
		}
	}
	return hex.EncodeToString(sum[:]) + ":" + id
}

func trustedAssociation(a string) bool {
	switch a {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

// NudgeFor is rule 8 of spec §4.1: for a Ready session with an open PR, the
// first nudge that applies, in the order the hook's, conflict, ci,
// issue-changed. A kind with a recorded nudge on this PR is skipped while its
// key is unchanged and NudgeGap has not passed since it (the repeat guard of
// spec §4.3), and the next kind is tried. The nudge that would be the
// NudgeLimit-th of its kind on the PR comes back as an ActEscalate instead of
// an ActSend; an escalation records no count, so it is decided again until it
// has been carried out. Counts are per PR and kind.
//
// The text never holds the nudge-count suffix or a ci nudge's log tails: the
// action that sends it appends them.
func NudgeFor(is github.WatchIssue, pr github.WatchPR, sess Session, hook []Nudge, mem Memory, cfg Config, now time.Time) (Action, bool) {
	if sess.Status != statusReady || pr.State != prOpen {
		return Action{}, false
	}
	recorded := mem.PRs[pr.Number].Nudges
	for _, n := range nudgeCandidates(is, pr, hook, mem, cfg) {
		rec := recorded[n.Kind]
		if rec.N > 0 && rec.Key == n.Key && now.Sub(rec.At) < cfg.NudgeGap {
			continue
		}
		act := Action{Issue: is.Number, Session: sess.ID, PR: pr.Number, Nudge: n}
		if rec.N+1 >= cfg.NudgeLimit {
			act.Kind = ActEscalate
			act.Nudge.Text = ""
			act.N = cfg.NudgeLimit
			act.Reason = fmt.Sprintf("PR #%d: %s nudge limit (%d) reached", pr.Number, n.Kind, cfg.NudgeLimit)
			return act, true
		}
		act.Kind = ActSend
		act.N = rec.N + 1
		return act, true
	}
	return Action{}, false
}

// nudgeCandidates lists, in order, the nudges whose condition holds now.
func nudgeCandidates(is github.WatchIssue, pr github.WatchPR, hook []Nudge, mem Memory, cfg Config) []Nudge {
	out := append([]Nudge(nil), hook...)
	if pr.Mergeable == "CONFLICTING" {
		out = append(out, Nudge{
			Kind: kindConflict,
			Key:  pr.HeadSHA + ":" + pr.BaseSHA,
			Text: fmt.Sprintf("PR #%d conflicts with its base. Merge origin/%s into this branch (never rebase), resolve the conflicts, re-run the gates, and push.", pr.Number, pr.BaseRefName),
		})
	}
	if len(pr.Failing) > 0 {
		names := make([]string, 0, len(pr.Failing))
		for _, c := range pr.Failing {
			names = append(names, c.Name)
		}
		out = append(out, Nudge{
			Kind: kindCI,
			Key:  pr.HeadSHA,
			Text: fmt.Sprintf("CI failed on PR #%d. Failing checks:\n%s", pr.Number, strings.Join(names, "\n")),
		})
	}
	if cfg.IssueChanged {
		seen := mem.Issues[is.Number].Seen
		if cur := IssueSeen(is); seen != "" && cur != seen {
			out = append(out, Nudge{
				Kind: kindIssueChanged,
				Key:  cur,
				Text: fmt.Sprintf("Issue #%d changed since you last read it (its body, or a new comment from a maintainer). Re-read it with `gh issue view %d --comments` and adjust the work on PR #%d if it matters.", is.Number, is.Number, pr.Number),
			})
		}
	}
	return out
}
