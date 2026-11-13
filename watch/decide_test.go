package watch

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
)

var dcNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

const dcPrefix = "aidanb/"

func dcTitleTmpl(t *testing.T) TitleTemplate {
	t.Helper()
	tmpl, err := ParseTitleTemplate("heal {issue}")
	require.NoError(t, err)
	return tmpl
}

func dcCfg() Config {
	return Config{
		Label: "drift", EscalateLabel: "needs-human", Cap: 2, Stuck: 2 * time.Hour,
		NudgeLimit: 3, NudgeGap: time.Hour, IssueChanged: true,
	}
}

func dcIssue(n int, labels ...string) github.WatchIssue {
	return github.WatchIssue{Number: n, Title: fmt.Sprintf("issue %d", n), Body: "body", Labels: labels}
}

func dcClosed(n int, labels ...string) github.WatchIssue {
	is := dcIssue(n, labels...)
	is.Closed = true
	return is
}

// dcBranch is the branch a watcher session for issue n gets: the real
// sanitizer applied to the prefix and the rendered title.
func dcBranch(n int) string {
	return git.SanitizeBranchName(fmt.Sprintf("%sheal %d", dcPrefix, n))
}

// dcPR is a PR the watcher (viewer "bot") opened for issue n.
func dcPR(number, issue int, state string) github.WatchPR {
	return github.WatchPR{
		Number: number, State: state, HeadRefName: dcBranch(issue), BaseRefName: "main",
		HeadSHA: "head", BaseSHA: "base", Mergeable: "MERGEABLE", Author: "bot", Closes: []int{issue},
	}
}

func dcWith(pr github.WatchPR, mutate func(*github.WatchPR)) github.WatchPR {
	mutate(&pr)
	return pr
}

// dcSess is a watcher session for issue n whose status began d ago.
func dcSess(id uint64, issue int, status string, d time.Duration) Session {
	return Session{
		ID: id, Title: fmt.Sprintf("heal %d", issue), Status: status,
		Since: dcNow.Add(-d), Branch: dcBranch(issue), Issue: issue,
	}
}

func dcSnap(issues []github.WatchIssue, prs []github.WatchPR, sessions []Session) Snapshot {
	return Snapshot{Now: dcNow, Viewer: "bot", Prefix: dcPrefix, Issues: issues, PRs: prs, Sessions: sessions}
}

func dcDecide(t *testing.T, mem Memory, issues []github.WatchIssue, prs []github.WatchPR, sessions []Session) []Action {
	t.Helper()
	return Decide(dcSnap(issues, prs, sessions), mem, dcCfg(), dcTitleTmpl(t))
}

func TestOwnPR_FourTerms(t *testing.T) {
	pattern := dcTitleTmpl(t).BranchPattern(dcPrefix, 96)
	good := dcPR(123, 96, "OPEN")
	require.True(t, OwnPR(good, 96, "bot", pattern), "a PR that passes all four terms is the watcher's")

	for name, pr := range map[string]github.WatchPR{
		"another issue's branch":   dcWith(good, func(p *github.WatchPR) { p.HeadRefName = dcBranch(97) }),
		"a branch opened by hand":  dcWith(good, func(p *github.WatchPR) { p.HeadRefName = "aidanb/my-fix" }),
		"closes another issue":     dcWith(good, func(p *github.WatchPR) { p.Closes = []int{97} }),
		"closes nothing":           dcWith(good, func(p *github.WatchPR) { p.Closes = nil }),
		"opened from a fork":       dcWith(good, func(p *github.WatchPR) { p.CrossRepo = true }),
		"opened by someone else":   dcWith(good, func(p *github.WatchPR) { p.Author = "alice" }),
		"opened by nobody we know": dcWith(good, func(p *github.WatchPR) { p.Author = "" }),
	} {
		assert.False(t, OwnPR(pr, 96, "bot", pattern), name)
	}

	assert.False(t, OwnPR(dcWith(good, func(p *github.WatchPR) { p.Author = "" }), 96, "", pattern),
		"an unknown viewer owns nothing, even a PR with no author")
	assert.False(t, OwnPR(good, 96, "bot", nil), "no pattern, no ownership")
	assert.True(t, OwnPR(dcWith(good, func(p *github.WatchPR) { p.Closes = []int{95, 96} }), 96, "bot", pattern),
		"closing several issues is fine when one is N")
	assert.True(t, OwnPR(dcWith(good, func(p *github.WatchPR) { p.Author = "BOT" }), 96, "bot", pattern),
		"logins compare without case")
}

func TestOwnSession(t *testing.T) {
	tmpl := dcTitleTmpl(t)
	for _, tc := range []struct {
		name string
		s    Session
		want bool
	}{
		{"its title and its issue agree", Session{Title: "heal 96", Issue: 96}, true},
		{"the title names another issue than the field", Session{Title: "heal 96", Issue: 97}, false},
		{"unlinked to any issue", Session{Title: "heal 96", Issue: 0}, false},
		{"a title the template does not parse", Session{Title: "my notes", Issue: 96}, false},
		{"issue zero in the title and the field", Session{Title: "heal 0", Issue: 0}, false},
	} {
		assert.Equal(t, tc.want, OwnSession(tc.s, tmpl), tc.name)
	}
}
