package watch

import (
	"encoding/json"
	"fmt"
	"os"
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

	two := []Session{dcSess(14, 96, "Ready", time.Hour), dcSess(11, 96, "Paused", time.Hour)}
	got = dcDecide(t, Memory{}, issues, []github.WatchPR{dcPR(120, 96, "CLOSED")}, two)
	require.Len(t, got, 3)
	assert.Equal(t, ActEscalate, got[0].Kind)
	assert.Equal(t, uint64(11), got[0].Session, "the escalation names the lowest session ID")

	notWatchers := []github.WatchPR{dcWith(dcPR(120, 96, "CLOSED"), func(p *github.WatchPR) { p.Author = "alice" })}
	assert.Empty(t, dcDecide(t, Memory{}, issues, notWatchers, sessions), "a closed PR that is not the watcher's is not rule 5's")
}

func TestDecide_Rule5_APRTheWatcherClosedNeverMeetsIt(t *testing.T) {
	// Rules 2 and 5 close a PR and settle it in memory; the issue then loses
	// L (the owner removed it, or it was never on), and the closed PR is
	// read again: it must not escalate the issue a second time.
	closed := []github.WatchPR{dcPR(120, 96, "CLOSED")}
	settled := Memory{PRs: map[int]PRMemory{120: {Settled: true}}}

	open := []github.WatchIssue{dcIssue(96, "drift")}
	got := dcDecide(t, settled, open, closed, nil)
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}}, got, "the issue is the watcher's again: it starts, it does not escalate")

	got = dcDecide(t, settled, []github.WatchIssue{dcIssue(96, "drift", "needs-human")}, closed, nil)
	assert.Empty(t, got, "and with L on, rule 4 holds as ever")

	assert.Equal(t, ActEscalate, dcDecide(t, Memory{}, open, closed, nil)[0].Kind, "an unsettled one still meets rule 5")
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

func TestDecide_Rule6_AnOpenPRWithoutASessionIsResumed(t *testing.T) {
	labelled := []github.WatchIssue{dcIssue(96, "drift")}
	open := []github.WatchPR{dcPR(125, 96, "OPEN"), dcPR(123, 96, "OPEN")}

	got := dcDecide(t, Memory{}, labelled, open, nil)
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96, PR: 123, Branch: dcBranch(96), Resume: true}}, got,
		"the lowest-numbered open PR's branch")

	unlabelled := []github.WatchIssue{dcIssue(96)}
	got = dcDecide(t, Memory{}, unlabelled, open, nil)
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96, PR: 123, Branch: dcBranch(96), Resume: true}}, got,
		"rule 6 does not read the issue's label")

	for _, status := range []string{"Ready", "Paused", "Running", "Loading", "Deleting", "Prompting", "Recoverable", "Status(9)"} {
		got = dcDecide(t, Memory{}, labelled, open[:1], []Session{dcSess(11, 96, status, time.Minute)})
		for _, a := range got {
			assert.NotEqual(t, ActStart, a.Kind, "a session in status %q already exists", status)
		}
	}

	foreign := Session{ID: 30, Title: "my notes", Status: "Running", Issue: 96}
	got = dcDecide(t, Memory{}, labelled, open, []Session{foreign})
	assert.Equal(t, []Action{{
		Kind: ActWait, Issue: 96, Reason: "a session the watcher did not start already works on issue #96",
	}}, got)

	full := []Session{dcSess(21, 97, "Paused", time.Hour), dcSess(22, 98, "Ready", time.Minute)}
	got = dcDecide(t, Memory{}, labelled, open, full)
	assert.Equal(t, []Action{{Kind: ActWait, Issue: 96, Reason: "the cap of 2 watcher sessions is full"}}, got)

	elsewhere := Session{ID: 31, Title: "my notes", Status: "Running", Issue: 55}
	got = dcDecide(t, Memory{}, labelled, open, []Session{elsewhere})
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96, PR: 123, Branch: dcBranch(96), Resume: true}}, got,
		"a foreign session of another issue blocks nothing")
}

func TestDecide_Rule9_ALabelledIssueWithNothingIsStarted(t *testing.T) {
	labelled := []github.WatchIssue{dcIssue(96, "drift")}

	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}}, dcDecide(t, Memory{}, labelled, nil, nil))

	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}},
		dcDecide(t, Memory{}, []github.WatchIssue{dcIssue(96, "Drift")}, nil, nil), "the label is matched without case")

	assert.Empty(t, dcDecide(t, Memory{}, []github.WatchIssue{dcIssue(96)}, nil, nil), "no label, no start")
	assert.Empty(t, dcDecide(t, Memory{}, []github.WatchIssue{dcIssue(96, "bug")}, nil, nil), "another label, no start")

	byAlice := []github.WatchPR{dcWith(dcPR(123, 96, "MERGED"), func(p *github.WatchPR) { p.Author = "alice" })}
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}}, dcDecide(t, Memory{}, labelled, byAlice, nil),
		"a merged PR that is not the watcher's does not stop a start")

	settled := Memory{PRs: map[int]PRMemory{120: {Settled: true}}}
	closed := []github.WatchPR{dcPR(120, 96, "CLOSED")}
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}}, dcDecide(t, settled, labelled, closed, nil),
		"a settled PR is past: with the escalate label gone, the issue is taken again")

	foreign := Session{ID: 30, Title: "my notes", Status: "Ready", Issue: 96}
	assert.Equal(t, []Action{{
		Kind: ActWait, Issue: 96, Reason: "a session the watcher did not start already works on issue #96",
	}}, dcDecide(t, Memory{}, labelled, nil, []Session{foreign}))
}

func TestDecide_TheCapCountsEveryWatcherSession(t *testing.T) {
	labelled := []github.WatchIssue{dcIssue(96, "drift")}
	capWait := []Action{{Kind: ActWait, Issue: 96, Reason: "the cap of 2 watcher sessions is full"}}

	twoForOne := []Session{dcSess(21, 97, "Paused", time.Hour), dcSess(22, 97, "Ready", time.Minute)}
	assert.Equal(t, capWait, dcDecide(t, Memory{}, labelled, nil, twoForOne),
		"a Paused session and a second one for the same issue both count")

	loading := []Session{dcSess(21, 97, "Loading", time.Minute), dcSess(22, 98, "Deleting", time.Minute)}
	assert.Equal(t, capWait, dcDecide(t, Memory{}, labelled, nil, loading), "Loading and Deleting sessions count")

	oneAndForeign := []Session{
		dcSess(21, 97, "Paused", time.Hour),
		{ID: 40, Title: "my notes", Status: "Running", Issue: 55},
		{ID: 41, Title: "heal 98", Status: "Running", Issue: 0},
	}
	assert.Equal(t, []Action{{Kind: ActStart, Issue: 96}}, dcDecide(t, Memory{}, labelled, nil, oneAndForeign),
		"foreign sessions and unlinked ones are not counted")
}

func TestDecide_OneStartPerCall(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(98, "drift"), dcIssue(96, "drift"), dcIssue(97, "drift")}
	oneTick := "another session start already went out this tick"

	got := dcDecide(t, Memory{}, issues, nil, nil)
	assert.Equal(t, []Action{
		{Kind: ActStart, Issue: 96},
		{Kind: ActWait, Issue: 97, Reason: oneTick},
		{Kind: ActWait, Issue: 98, Reason: oneTick},
	}, got, "the lowest-numbered issue starts, the others wait")

	both := []github.WatchIssue{dcIssue(96, "drift"), dcIssue(97, "drift")}
	got = dcDecide(t, Memory{}, both, []github.WatchPR{dcPR(124, 97, "OPEN")}, nil)
	assert.Equal(t, []Action{
		{Kind: ActStart, Issue: 96},
		{Kind: ActWait, Issue: 97, Reason: oneTick},
	}, got, "a resume and a fresh start share the one start")
}

func TestDecide_ClassifiesTheSessionListFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/sessions.json")
	require.NoError(t, err)
	var rows []struct {
		ID          uint64    `json:"id"`
		Title       string    `json:"title"`
		Status      string    `json:"status"`
		StatusSince time.Time `json:"status_since"`
		Branch      string    `json:"branch"`
		Issue       int       `json:"issue"`
	}
	require.NoError(t, json.Unmarshal(data, &rows))
	require.Len(t, rows, 6)
	var sessions []Session
	for _, row := range rows {
		sessions = append(sessions, Session{
			ID: row.ID, Title: row.Title, Status: row.Status, Since: row.StatusSince,
			Branch: row.Branch, Issue: row.Issue,
		})
	}

	issues := []github.WatchIssue{
		dcIssue(96, "drift"), dcIssue(97, "drift"), dcIssue(98, "drift"), dcIssue(99, "drift"), dcIssue(100, "drift"),
	}
	cfg := dcCfg()
	cfg.Cap = 4
	got := Decide(dcSnap(issues, nil, sessions), Memory{}, cfg, dcTitleTmpl(t))

	assert.Equal(t, []Action{
		{Kind: ActWait, Issue: 98, Reason: "a session the watcher did not start already works on issue #98"},
		{Kind: ActStart, Issue: 99},
	}, got, "96 and 97 have their own sessions and 100's is loading, so three of the cap of 4 are used; 98 has a foreign one; 99's title parses but its session is unlinked, so it is not the watcher's, is not counted, and 99 starts")
}

func TestDecide_Rule7_AStuckSessionEscalates(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift")}
	open := []github.WatchPR{dcPR(123, 96, "OPEN")}
	noTime := dcSess(11, 96, "Prompting", 0)
	noTime.Since = time.Time{}

	for _, tc := range []struct {
		name string
		sess Session
		prs  []github.WatchPR
		want bool
	}{
		{"prompting over the limit", dcSess(11, 96, "Prompting", 3*time.Hour), nil, true},
		{"recoverable over the limit", dcSess(11, 96, "Recoverable", 3*time.Hour), nil, true},
		{"ready with no open PR over the limit", dcSess(11, 96, "Ready", 3*time.Hour), nil, true},
		{"prompting one second over", dcSess(11, 96, "Prompting", 2*time.Hour+time.Second), nil, true},
		{"prompting exactly at the limit", dcSess(11, 96, "Prompting", 2*time.Hour), nil, false},
		{"prompting under the limit", dcSess(11, 96, "Prompting", time.Hour), nil, false},
		{"ready with an open PR over the limit", dcSess(11, 96, "Ready", 3*time.Hour), open, false},
		{"paused for days", dcSess(11, 96, "Paused", 72*time.Hour), nil, false},
		{"running for days", dcSess(11, 96, "Running", 72*time.Hour), nil, false},
		{"loading for days", dcSess(11, 96, "Loading", 72*time.Hour), nil, false},
		{"deleting for days", dcSess(11, 96, "Deleting", 72*time.Hour), nil, false},
		{"an unknown status", dcSess(11, 96, "Status(9)", 72*time.Hour), nil, false},
		{"no status time recorded", noTime, nil, false},
	} {
		got := dcDecide(t, Memory{}, issues, tc.prs, []Session{tc.sess})
		if !tc.want {
			assert.Empty(t, got, tc.name)
			continue
		}
		require.Len(t, got, 1, tc.name)
		assert.Equal(t, ActEscalate, got[0].Kind, tc.name)
	}

	got := dcDecide(t, Memory{}, issues, nil, []Session{dcSess(11, 96, "Prompting", 3*time.Hour)})
	assert.Equal(t, []Action{{
		Kind: ActEscalate, Issue: 96, Session: 11,
		Reason: "session heal 96 has been Prompting since 2026-11-13T09:00:00Z",
	}}, got)

	got = dcDecide(t, Memory{}, issues, open, []Session{dcSess(11, 96, "Recoverable", 3*time.Hour)})
	assert.Equal(t, []Action{{
		Kind: ActEscalate, Issue: 96, Session: 11, PR: 123,
		Reason: "session heal 96 has been Recoverable since 2026-11-13T09:00:00Z",
	}}, got, "the open PR is named when there is one")
}

func TestDecide_Rule8_ANudgeForAReadySessionWithAnOpenPR(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift")}
	conflicting := []github.WatchPR{dcWith(dcPR(123, 96, "OPEN"), func(p *github.WatchPR) { p.Mergeable = "CONFLICTING" })}

	got := dcDecide(t, Memory{}, issues, conflicting, []Session{dcSess(11, 96, "Ready", time.Hour)})
	require.Len(t, got, 1)
	assert.Equal(t, ActSend, got[0].Kind)
	assert.Equal(t, 96, got[0].Issue)
	assert.Equal(t, uint64(11), got[0].Session)
	assert.Equal(t, 123, got[0].PR)
	assert.Equal(t, "conflict", got[0].Nudge.Kind)
	assert.Equal(t, 1, got[0].N)

	for _, status := range []string{"Running", "Paused", "Prompting", "Loading", "Deleting", "Recoverable"} {
		got = dcDecide(t, Memory{}, issues, conflicting, []Session{dcSess(11, 96, status, time.Hour)})
		assert.Empty(t, got, "a %s session is not nudged", status)
	}

	clean := []github.WatchPR{dcPR(123, 96, "OPEN")}
	assert.Empty(t, dcDecide(t, Memory{}, issues, clean, []Session{dcSess(11, 96, "Ready", time.Hour)}), "nothing to nudge about")

	atLimit := Memory{PRs: map[int]PRMemory{
		123: {Nudges: map[string]NudgeRecord{"conflict": {N: 2, Key: "old", At: dcNow.Add(-time.Minute)}}},
	}}
	got = dcDecide(t, atLimit, issues, conflicting, []Session{dcSess(11, 96, "Ready", time.Hour)})
	require.Len(t, got, 1)
	assert.Equal(t, ActEscalate, got[0].Kind)
	assert.Equal(t, "PR #123: conflict nudge limit (3) reached", got[0].Reason)

	twoReady := []Session{dcSess(12, 96, "Ready", time.Hour), dcSess(11, 96, "Ready", time.Hour)}
	got = dcDecide(t, Memory{}, issues, conflicting, twoReady)
	require.Len(t, got, 1, "one nudge for the issue, not one per session")
	assert.Equal(t, uint64(11), got[0].Session, "the lowest session ID")
}

func TestDecide_Rule8_HookNudgesComeFirstAndAHookFailureSuppressesOnlyItsIssue(t *testing.T) {
	issues := []github.WatchIssue{dcIssue(96, "drift"), dcIssue(97, "drift")}
	red := func(issue int) github.WatchPR {
		return dcWith(dcPR(100+issue, issue, "OPEN"), func(p *github.WatchPR) {
			p.Failing = []github.FailedCheck{{Name: "build"}}
		})
	}
	snap := dcSnap(issues, []github.WatchPR{red(96), red(97)},
		[]Session{dcSess(11, 96, "Ready", time.Hour), dcSess(12, 97, "Ready", time.Hour)})
	snap.HookNudges = map[int][]Nudge{97: {{Kind: "redrift", Key: "fp", Text: "redo"}}}
	snap.HookFailed = map[int]string{96: "hook exited 1"}

	got := Decide(snap, Memory{}, dcCfg(), dcTitleTmpl(t))

	require.Len(t, got, 2)
	assert.Equal(t, 96, got[0].Issue)
	assert.Equal(t, "ci", got[0].Nudge.Kind, "issue 96's hook failed: it gets the built-in nudges and no hook one")
	assert.Equal(t, 97, got[1].Issue)
	assert.Equal(t, "redrift", got[1].Nudge.Kind, "issue 97's hook nudge comes before its ci nudge")
}

func TestDecide_LoadingAndDeletingSessionsAreSkipped(t *testing.T) {
	for _, status := range []string{"Loading", "Deleting", "Status(9)"} {
		sess := []Session{dcSess(11, 96, status, 3*time.Hour)}
		open := []github.WatchPR{dcPR(123, 96, "OPEN")}
		conflicting := []github.WatchPR{dcWith(open[0], func(p *github.WatchPR) { p.Mergeable = "CONFLICTING" })}

		assert.Empty(t, dcDecide(t, Memory{}, []github.WatchIssue{dcIssue(96, "drift")}, []github.WatchPR{dcPR(123, 96, "MERGED")}, sess),
			"%s: a merged PR kills nothing", status)
		assert.Equal(t,
			[]Action{{Kind: ActClosePR, Issue: 96, PR: 123, Reason: "The watcher is closing this PR because issue #96 is closed."}},
			dcDecide(t, Memory{}, []github.WatchIssue{dcClosed(96, "drift")}, open, sess),
			"%s: a closed issue closes the PR and kills nothing", status)
		assert.Empty(t, dcDecide(t, Memory{}, []github.WatchIssue{dcIssue(96, "drift")}, conflicting, sess),
			"%s: not stuck, not nudged, and its existence blocks a resume", status)
	}
}

func TestDecide_RuleOrder(t *testing.T) {
	drift := []github.WatchIssue{dcIssue(96, "drift")}
	handsOff := []github.WatchIssue{dcIssue(96, "drift", "needs-human")}
	ready := []Session{dcSess(11, 96, "Ready", time.Hour)}
	killIt := []Action{{Kind: ActKill, Issue: 96, Session: 11}}

	assert.Equal(t, killIt, dcDecide(t, Memory{}, []github.WatchIssue{dcClosed(96, "drift")},
		[]github.WatchPR{dcPR(123, 96, "MERGED")}, ready), "a merged PR on a closed issue: its session is killed and no PR is closed")

	assert.Equal(t, killIt, dcDecide(t, Memory{}, []github.WatchIssue{dcClosed(96, "drift", "needs-human")}, nil, ready),
		"a closed issue carrying the escalate label still frees its session")

	assert.Equal(t, killIt, dcDecide(t, Memory{}, drift,
		[]github.WatchPR{dcPR(122, 96, "MERGED"), dcPR(123, 96, "CLOSED")}, ready),
		"a merged PR ends the issue before a closed one escalates it")

	assert.Equal(t, killIt, dcDecide(t, Memory{}, []github.WatchIssue{dcClosed(96, "drift")},
		[]github.WatchPR{dcPR(123, 96, "CLOSED")}, ready),
		"a PR closed on a closed issue is not rule 5's: the issue is closed, the session is killed, nothing escalates")

	assert.Empty(t, dcDecide(t, Memory{}, handsOff, nil, nil), "the escalate label stops rule 9")
	assert.Empty(t, dcDecide(t, Memory{}, handsOff, nil, []Session{dcSess(11, 96, "Prompting", 3*time.Hour)}),
		"the escalate label stops rule 7")
	assert.Empty(t, dcDecide(t, Memory{}, handsOff,
		[]github.WatchPR{dcWith(dcPR(123, 96, "OPEN"), func(p *github.WatchPR) { p.Mergeable = "CONFLICTING" })}, ready),
		"the escalate label stops rule 8")
	assert.Empty(t, dcDecide(t, Memory{}, handsOff, []github.WatchPR{dcPR(123, 96, "OPEN")}, nil),
		"the escalate label stops rule 6")

	got := dcDecide(t, Memory{}, drift, []github.WatchPR{dcPR(123, 96, "CLOSED")}, nil)
	require.Len(t, got, 1, "a closed-unmerged PR escalates and does not also start a session")
	assert.Equal(t, ActEscalate, got[0].Kind)

	red := []github.WatchPR{dcWith(dcPR(123, 96, "OPEN"), func(p *github.WatchPR) {
		p.Failing = []github.FailedCheck{{Name: "build"}}
	})}
	got = dcDecide(t, Memory{}, drift, red, []Session{dcSess(11, 96, "Prompting", 3*time.Hour)})
	require.Len(t, got, 1, "a stuck session with a red PR escalates once and is not nudged")
	assert.Equal(t, ActEscalate, got[0].Kind)
	assert.Equal(t, 123, got[0].PR)

	assert.Empty(t, dcDecide(t, Memory{}, []github.WatchIssue{dcClosed(96, "drift")}, nil, nil), "rule 3 stops rule 9")
}
