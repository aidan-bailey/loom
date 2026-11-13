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

func TestDecide_Rule1_AMergedPRKillsItsSession(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift")}
	merged := []github.WatchPR{dcPR(123, 96, "MERGED")}

	for _, status := range []string{"Ready", "Paused", "Running", "Prompting", "Recoverable"} {
		got := dcDecide(t, Memory{}, issues, merged, []Session{dcSess(11, 96, status, time.Hour)})
		assert.Equal(t, []Action{{Kind: ActKill, Issue: 96, Session: 11}}, got, status)
	}

	two := []Session{dcSess(11, 96, "Ready", time.Hour), dcSess(12, 96, "Paused", time.Hour)}
	assert.Equal(t,
		[]Action{{Kind: ActKill, Issue: 96, Session: 11}, {Kind: ActKill, Issue: 96, Session: 12}},
		dcDecide(t, Memory{}, issues, merged, two), "one kill per own session")

	assert.Empty(t, dcDecide(t, Memory{}, issues, merged, nil), "no session: nothing to kill")

	withOpen := append(append([]github.WatchPR(nil), merged...), dcPR(124, 96, "OPEN"))
	assert.Empty(t, dcDecide(t, Memory{}, issues, withOpen, []Session{dcSess(11, 96, "Ready", time.Hour)}),
		"an open PR beside the merged one keeps the session")

	byAlice := []github.WatchPR{dcWith(merged[0], func(p *github.WatchPR) { p.Author = "alice" })}
	assert.Empty(t, dcDecide(t, Memory{}, issues, byAlice, []Session{dcSess(11, 96, "Ready", time.Hour)}),
		"a merged PR that is not the watcher's kills nothing")
}

func TestDecide_Rule2_ClosedIssueClosesItsOpenPRThenKills(t *testing.T) {
	issues := []github.WatchIssue{dcClosed(96, "drift")}
	sessions := []Session{dcSess(11, 96, "Ready", time.Hour)}
	open := []github.WatchPR{dcPR(123, 96, "OPEN")}
	closeReason := "The watcher is closing this PR because issue #96 is closed."

	got := dcDecide(t, Memory{}, issues, open, sessions)
	assert.Equal(t, []Action{
		{Kind: ActClosePR, Issue: 96, PR: 123, Reason: closeReason},
		{Kind: ActKill, Issue: 96, Session: 11},
	}, got)

	withMerged := []github.WatchPR{dcPR(122, 96, "MERGED"), dcPR(123, 96, "OPEN")}
	got = dcDecide(t, Memory{}, issues, withMerged, sessions)
	assert.Equal(t, []Action{
		{Kind: ActClosePR, Issue: 96, PR: 123, Reason: closeReason},
		{Kind: ActKill, Issue: 96, Session: 11},
	}, got, "a merged PR does not stop the open one being closed")

	byAlice := []github.WatchPR{dcWith(open[0], func(p *github.WatchPR) { p.Author = "alice" })}
	got = dcDecide(t, Memory{}, issues, byAlice, sessions)
	assert.Equal(t, []Action{{Kind: ActKill, Issue: 96, Session: 11}}, got, "only its own PRs are closed")
}

func TestDecide_Rule3_ClosedIssueWithoutAnOpenPRKillsItsSession(t *testing.T) {
	issues := []github.WatchIssue{dcClosed(96, "drift")}

	got := dcDecide(t, Memory{}, issues, nil, []Session{dcSess(11, 96, "Paused", time.Hour)})
	assert.Equal(t, []Action{{Kind: ActKill, Issue: 96, Session: 11}}, got)

	assert.Empty(t, dcDecide(t, Memory{}, issues, nil, nil), "no session, nothing to do")
}

func TestDecide_Rule4_TheEscalateLabelIsHandsOff(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift", "Needs-Human")}
	closedUnmerged := []github.WatchPR{dcPR(123, 96, "CLOSED")}

	assert.Empty(t, dcDecide(t, Memory{}, issues, closedUnmerged, []Session{dcSess(11, 96, "Ready", time.Hour)}),
		"the label is matched without case and holds rule 5 back")
}

func TestDecide_Rule5_APRClosedUnmergedEscalatesOnceAndSettles(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift")}
	sessions := []Session{dcSess(11, 96, "Ready", time.Hour)}
	prs := []github.WatchPR{dcPR(120, 96, "CLOSED"), dcPR(123, 96, "OPEN")}

	got := dcDecide(t, Memory{}, issues, prs, sessions)

	assert.Equal(t, []Action{
		{
			Kind: ActEscalate, Issue: 96, Session: 11, PR: 120, Settle: []int{120},
			Reason: "PR #120 was closed unmerged; the watcher will not take this issue again until needs-human is removed",
		},
		{
			Kind: ActClosePR, Issue: 96, PR: 123,
			Reason: "The watcher is closing this PR: PR #120 was closed unmerged, so the watcher will not take issue #96 again until needs-human is removed.",
		},
		{Kind: ActKill, Issue: 96, Session: 11},
	}, got)

	several := []github.WatchPR{dcPR(121, 96, "CLOSED"), dcPR(119, 96, "CLOSED"), dcPR(120, 96, "CLOSED")}
	settled := Memory{PRs: map[int]PRMemory{119: {Settled: true}}}
	got = dcDecide(t, settled, issues, several, nil)
	require.Len(t, got, 1)
	assert.Equal(t, ActEscalate, got[0].Kind)
	assert.Equal(t, []int{120, 121}, got[0].Settle, "every unsettled closed PR, in number order, and no settled one")
	assert.Equal(t, 120, got[0].PR)
	assert.Zero(t, got[0].Session, "no session to name")

	notWatchers := []github.WatchPR{dcWith(dcPR(120, 96, "CLOSED"), func(p *github.WatchPR) { p.Author = "alice" })}
	assert.Empty(t, dcDecide(t, Memory{}, issues, notWatchers, sessions), "a closed PR that is not the watcher's is not rule 5's")
}

func TestDecide_RulesOnTheSnapshotIssuesInNumberOrder(t *testing.T) {
	issues := []github.WatchIssue{dcClosed(97, "drift"), dcClosed(96, "drift")}
	sessions := []Session{
		dcSess(12, 97, "Ready", time.Hour),
		dcSess(11, 96, "Ready", time.Hour),
		dcSess(15, 55, "Ready", time.Hour),
	}

	got := dcDecide(t, Memory{}, issues, nil, sessions)

	assert.Equal(t, []Action{
		{Kind: ActKill, Issue: 96, Session: 11},
		{Kind: ActKill, Issue: 97, Session: 12},
	}, got, "issue 55 is not in the snapshot, so its session is left alone")
	assert.Equal(t, 97, issues[0].Number, "Decide leaves the snapshot's own order alone")
}
