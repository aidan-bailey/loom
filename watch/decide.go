package watch

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/session/github"
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

// Decide is a stub until Task 5.2.
func Decide(s Snapshot, mem Memory, cfg Config, tmpl TitleTemplate) []Action { return nil }
