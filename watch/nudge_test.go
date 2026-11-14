package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/session/github"
)

var nfNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func nfHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func nfIssue() github.WatchIssue {
	return github.WatchIssue{Number: 96, Title: "drift", Body: "body v1"}
}

func TestAction_JSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  Action
		want string
	}{
		{"wait keeps issue even at zero", Action{Kind: ActWait}, `{"kind":"wait","issue":0}`},
		{"kill", Action{Kind: ActKill, Issue: 96, Session: 7}, `{"kind":"kill","issue":96,"session":7}`},
		{
			"send",
			Action{Kind: ActSend, Issue: 96, Session: 7, PR: 123, Nudge: Nudge{Kind: "ci", Key: "head1", Text: "CI failed"}, N: 2},
			`{"kind":"send","issue":96,"session":7,"pr":123,"nudge":{"kind":"ci","key":"head1","text":"CI failed"},"n":2}`,
		},
		{
			"resume start",
			Action{Kind: ActStart, Issue: 96, PR: 123, Branch: "aidanb/heal-96", Resume: true},
			`{"kind":"start","issue":96,"pr":123,"branch":"aidanb/heal-96","resume":true}`,
		},
		{
			"escalate with settled PRs",
			Action{Kind: ActEscalate, Issue: 96, Reason: "r", Settle: []int{120, 121}},
			`{"kind":"escalate","issue":96,"reason":"r","settle":[120,121]}`,
		},
		{"close", Action{Kind: ActClosePR, Issue: 96, PR: 123, Reason: "why"}, `{"kind":"close_pr","issue":96,"pr":123,"reason":"why"}`},
	} {
		b, err := json.Marshal(tc.act)
		assert.NoError(t, err, tc.name)
		assert.JSONEq(t, tc.want, string(b), tc.name)

		var back Action
		assert.NoError(t, json.Unmarshal(b, &back), tc.name)
		assert.Equal(t, tc.act, back, tc.name)
	}
}

func TestNudge_JSON(t *testing.T) {
	b, err := json.Marshal(Nudge{Kind: "redrift", Key: "fp", Text: "redo"})
	assert.NoError(t, err)
	assert.JSONEq(t, `{"kind":"redrift","key":"fp","text":"redo"}`, string(b))
}

func TestIssueSeen_BodyHashAndNewestTrustedComment(t *testing.T) {
	is := nfIssue()
	is.Comments = []github.IssueComment{
		{ID: "c1", Association: "OWNER", CreatedAt: nfNow.Add(-3 * time.Hour)},
		{ID: "c2", Association: "MEMBER", CreatedAt: nfNow.Add(-2 * time.Hour)},
		{ID: "c3", Association: "NONE", CreatedAt: nfNow.Add(-time.Hour)},
		{ID: "c0", Association: "COLLABORATOR", CreatedAt: nfNow.Add(-4 * time.Hour)},
	}

	assert.Equal(t, nfHash("body v1")+":c2", IssueSeen(is))
}

func TestIssueSeen_OnlyOwnerMemberCollaboratorAreTrusted(t *testing.T) {
	for _, tc := range []struct {
		assoc   string
		trusted bool
	}{
		{"OWNER", true},
		{"MEMBER", true},
		{"COLLABORATOR", true},
		{"CONTRIBUTOR", false},
		{"FIRST_TIME_CONTRIBUTOR", false},
		{"NONE", false},
		{"", false},
	} {
		is := nfIssue()
		is.Comments = []github.IssueComment{{ID: "c9", Association: tc.assoc, CreatedAt: nfNow}}

		want := nfHash("body v1") + ":"
		if tc.trusted {
			want += "c9"
		}
		assert.Equal(t, want, IssueSeen(is), tc.assoc)
	}
}

func TestIssueSeen_ChangesWithTheBody(t *testing.T) {
	a := nfIssue()
	b := nfIssue()
	b.Body = "body v2"

	assert.NotEqual(t, IssueSeen(a), IssueSeen(b))
	assert.Equal(t, nfHash("body v2")+":", IssueSeen(b))
}

func nfCfg() Config {
	return Config{NudgeLimit: 3, NudgeGap: time.Hour, IssueChanged: true}
}

func nfPR() github.WatchPR {
	return github.WatchPR{
		Number: 123, State: "OPEN", HeadRefName: "aidanb/heal-96", BaseRefName: "main",
		HeadSHA: "head1", BaseSHA: "base1", Mergeable: "MERGEABLE",
	}
}

func nfSess() Session {
	return Session{ID: 7, Title: "heal 96", Status: "Ready", Issue: 96}
}

// nfMem is memory holding one nudge record on PR 123.
func nfMem(kind string, rec NudgeRecord) Memory {
	return Memory{Version: 1, PRs: map[int]PRMemory{123: {Nudges: map[string]NudgeRecord{kind: rec}}}}
}

func nfRed(pr github.WatchPR) github.WatchPR {
	pr.Failing = []github.FailedCheck{{Name: "build"}, {Name: "lint"}}
	return pr
}

func TestNudgeFor_FirstThatAppliesWins(t *testing.T) {
	is := nfIssue()
	pr := nfRed(nfPR())
	pr.Mergeable = "CONFLICTING"
	mem := Memory{Issues: map[int]IssueMemory{96: {Seen: "old"}}}
	hook := []Nudge{{Kind: "redrift", Key: "fp1", Text: "redo the drift"}}

	act, ok := NudgeFor(is, pr, nfSess(), hook, mem, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind)
	assert.Equal(t, "redrift", act.Nudge.Kind)
	assert.Equal(t, "redo the drift", act.Nudge.Text)

	act, ok = NudgeFor(is, pr, nfSess(), nil, mem, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "conflict", act.Nudge.Kind)

	pr.Mergeable = "MERGEABLE"
	act, ok = NudgeFor(is, pr, nfSess(), nil, mem, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "ci", act.Nudge.Kind)

	pr.Failing = nil
	act, ok = NudgeFor(is, pr, nfSess(), nil, mem, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "issue-changed", act.Nudge.Kind)

	_, ok = NudgeFor(is, pr, nfSess(), nil, Memory{Issues: map[int]IssueMemory{96: {Seen: IssueSeen(is)}}}, nfCfg(), nfNow)
	assert.False(t, ok, "nothing applies to a clean PR on an unchanged issue")
}

func TestNudgeFor_HooksKeepTheirOrder(t *testing.T) {
	hook := []Nudge{
		{Kind: "first", Key: "k1", Text: "one"},
		{Kind: "second", Key: "k2", Text: "two"},
	}

	act, ok := NudgeFor(nfIssue(), nfPR(), nfSess(), hook, Memory{}, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "first", act.Nudge.Kind)

	recent := nfMem("first", NudgeRecord{N: 1, Key: "k1", At: nfNow.Add(-time.Minute)})
	act, ok = NudgeFor(nfIssue(), nfPR(), nfSess(), hook, recent, nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "second", act.Nudge.Kind, "a suppressed hook nudge lets the next one through")
}

func TestNudgeFor_OnlyAReadySessionWithAnOpenPR(t *testing.T) {
	pr := nfRed(nfPR())
	for _, status := range []string{"Running", "Paused", "Prompting", "Loading", "Deleting", "Recoverable", "Status(9)", ""} {
		sess := nfSess()
		sess.Status = status
		_, ok := NudgeFor(nfIssue(), pr, sess, nil, Memory{}, nfCfg(), nfNow)
		assert.False(t, ok, "status %q", status)
	}
	for _, state := range []string{"CLOSED", "MERGED", ""} {
		closed := pr
		closed.State = state
		_, ok := NudgeFor(nfIssue(), closed, nfSess(), nil, Memory{}, nfCfg(), nfNow)
		assert.False(t, ok, "state %q", state)
	}

	_, ok := NudgeFor(nfIssue(), pr, nfSess(), nil, Memory{}, nfCfg(), nfNow)
	assert.True(t, ok, "a Ready session with an open red PR is nudged")
}

func TestNudgeFor_ConflictOnlyWhenConflicting(t *testing.T) {
	for _, mergeable := range []string{"MERGEABLE", "UNKNOWN", ""} {
		pr := nfPR()
		pr.Mergeable = mergeable
		_, ok := NudgeFor(nfIssue(), pr, nfSess(), nil, Memory{}, nfCfg(), nfNow)
		assert.False(t, ok, "mergeable %q", mergeable)
	}

	pr := nfPR()
	pr.Mergeable = "CONFLICTING"
	act, ok := NudgeFor(nfIssue(), pr, nfSess(), nil, Memory{}, nfCfg(), nfNow)

	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind)
	assert.Equal(t, 96, act.Issue)
	assert.Equal(t, uint64(7), act.Session)
	assert.Equal(t, 123, act.PR)
	assert.Equal(t, 1, act.N)
	assert.Equal(t, "conflict", act.Nudge.Kind)
	assert.Equal(t, "head1:base1", act.Nudge.Key)
	assert.Equal(t,
		"PR #123 conflicts with its base. Merge origin/main into this branch (never rebase), resolve the conflicts, re-run the gates, and push.",
		act.Nudge.Text)
}

func TestNudgeFor_CIListsTheFailingChecks(t *testing.T) {
	act, ok := NudgeFor(nfIssue(), nfRed(nfPR()), nfSess(), nil, Memory{}, nfCfg(), nfNow)

	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind)
	assert.Equal(t, "ci", act.Nudge.Kind)
	assert.Equal(t, "head1", act.Nudge.Key)
	assert.Equal(t, "CI failed on PR #123. Failing checks:\nbuild\nlint", act.Nudge.Text)
	assert.Equal(t, 1, act.N)
}

func TestNudgeFor_IssueChanged(t *testing.T) {
	is := nfIssue()
	seen := func(s string) Memory { return Memory{Issues: map[int]IssueMemory{96: {Seen: s}}} }
	off := nfCfg()
	off.IssueChanged = false

	_, ok := NudgeFor(is, nfPR(), nfSess(), nil, seen("old"), off, nfNow)
	assert.False(t, ok, "issue_changed off")

	_, ok = NudgeFor(is, nfPR(), nfSess(), nil, seen(""), nfCfg(), nfNow)
	assert.False(t, ok, "no baseline in memory")

	_, ok = NudgeFor(is, nfPR(), nfSess(), nil, Memory{}, nfCfg(), nfNow)
	assert.False(t, ok, "no record of the issue at all")

	_, ok = NudgeFor(is, nfPR(), nfSess(), nil, seen(IssueSeen(is)), nfCfg(), nfNow)
	assert.False(t, ok, "unchanged since the baseline")

	act, ok := NudgeFor(is, nfPR(), nfSess(), nil, seen("old"), nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, "issue-changed", act.Nudge.Kind)
	assert.Equal(t, IssueSeen(is), act.Nudge.Key)
	assert.Equal(t,
		"Issue #96 changed since you last read it (its body, or a new comment from a maintainer). Re-read it with `gh issue view 96 --comments` and adjust the work on PR #123 if it matters.",
		act.Nudge.Text)
}

func TestNudgeFor_RepeatGuard(t *testing.T) {
	issueKey := IssueSeen(nfIssue())
	cases := []struct {
		name string
		kind string
		key  string
		pr   func(*github.WatchPR)
		hook []Nudge
		seen string
	}{
		{name: "conflict", kind: "conflict", key: "head1:base1", pr: func(p *github.WatchPR) { p.Mergeable = "CONFLICTING" }},
		{name: "ci", kind: "ci", key: "head1", pr: func(p *github.WatchPR) { *p = nfRed(*p) }},
		{name: "issue-changed", kind: "issue-changed", key: issueKey, seen: "old"},
		{name: "hook", kind: "redrift", key: "fp1", hook: []Nudge{{Kind: "redrift", Key: "fp1", Text: "redo"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := nfPR()
			if tc.pr != nil {
				tc.pr(&pr)
			}
			ask := func(rec NudgeRecord) (Action, bool) {
				mem := nfMem(tc.kind, rec)
				mem.Issues = map[int]IssueMemory{96: {Seen: tc.seen}}
				return NudgeFor(nfIssue(), pr, nfSess(), tc.hook, mem, nfCfg(), nfNow)
			}

			_, ok := ask(NudgeRecord{N: 1, Key: tc.key, At: nfNow.Add(-10 * time.Minute)})
			assert.False(t, ok, "same key inside the gap")

			_, ok = ask(NudgeRecord{N: 1, Key: tc.key, At: nfNow.Add(-59 * time.Minute)})
			assert.False(t, ok, "same key one minute short of the gap")

			act, ok := ask(NudgeRecord{N: 1, Key: "other", At: nfNow.Add(-10 * time.Minute)})
			require.True(t, ok, "a different key inside the gap")
			assert.Equal(t, tc.kind, act.Nudge.Kind)
			assert.Equal(t, 2, act.N)

			act, ok = ask(NudgeRecord{N: 1, Key: tc.key, At: nfNow.Add(-time.Hour)})
			require.True(t, ok, "same key exactly at the gap")
			assert.Equal(t, 2, act.N)

			act, ok = ask(NudgeRecord{N: 1, Key: tc.key, At: nfNow.Add(-2 * time.Hour)})
			require.True(t, ok, "same key past the gap")
			assert.Equal(t, 2, act.N)
		})
	}
}

func TestNudgeFor_SuppressedKindFallsThroughAndCountsStayPerKind(t *testing.T) {
	pr := nfRed(nfPR())
	pr.Mergeable = "CONFLICTING"
	mem := nfMem("conflict", NudgeRecord{N: 2, Key: "head1:base1", At: nfNow.Add(-time.Minute)})

	act, ok := NudgeFor(nfIssue(), pr, nfSess(), nil, mem, nfCfg(), nfNow)

	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind, "the conflict count at limit-1 does not escalate a ci nudge")
	assert.Equal(t, "ci", act.Nudge.Kind)
	assert.Equal(t, 1, act.N)
}

func TestNudgeFor_CountsBelongToThePR(t *testing.T) {
	mem := Memory{PRs: map[int]PRMemory{
		999: {Nudges: map[string]NudgeRecord{"ci": {N: 2, Key: "older", At: nfNow.Add(-time.Minute)}}},
	}}

	act, ok := NudgeFor(nfIssue(), nfRed(nfPR()), nfSess(), nil, mem, nfCfg(), nfNow)

	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind, "another PR's count is not this PR's")
	assert.Equal(t, 1, act.N)
}

func TestNudgeFor_TheLimitThNudgeEscalates(t *testing.T) {
	pr := nfRed(nfPR())
	older := func(n int) Memory {
		return nfMem("ci", NudgeRecord{N: n, Key: "older", At: nfNow.Add(-10 * time.Minute)})
	}

	act, ok := NudgeFor(nfIssue(), pr, nfSess(), nil, older(1), nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, ActSend, act.Kind, "the second of three still sends")
	assert.Equal(t, 2, act.N)

	act, ok = NudgeFor(nfIssue(), pr, nfSess(), nil, older(2), nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, ActEscalate, act.Kind, "the third of three escalates")
	assert.Equal(t, 96, act.Issue)
	assert.Equal(t, uint64(7), act.Session)
	assert.Equal(t, 123, act.PR)
	assert.Equal(t, 3, act.N)
	assert.Equal(t, "PR #123: ci nudge limit (3) reached", act.Reason)
	assert.Equal(t, Nudge{Kind: "ci", Key: "head1"}, act.Nudge, "an escalation names the kind and key, not a text")

	act, ok = NudgeFor(nfIssue(), pr, nfSess(), nil, older(5), nfCfg(), nfNow)
	require.True(t, ok)
	assert.Equal(t, ActEscalate, act.Kind, "a count past the limit still escalates")
	assert.Equal(t, 3, act.N)

	one := nfCfg()
	one.NudgeLimit = 1
	act, ok = NudgeFor(nfIssue(), pr, nfSess(), nil, Memory{}, one, nfNow)
	require.True(t, ok)
	assert.Equal(t, ActEscalate, act.Kind, "a limit of 1 escalates the first")
}

func TestNudgeFor_AHookKindEscalatesToo(t *testing.T) {
	hook := []Nudge{{Kind: "redrift", Key: "fp1", Text: "redo"}}
	mem := nfMem("redrift", NudgeRecord{N: 2, Key: "fp0", At: nfNow.Add(-time.Minute)})

	act, ok := NudgeFor(nfIssue(), nfPR(), nfSess(), hook, mem, nfCfg(), nfNow)

	require.True(t, ok)
	assert.Equal(t, ActEscalate, act.Kind)
	assert.Equal(t, "PR #123: redrift nudge limit (3) reached", act.Reason)
	assert.Equal(t, Nudge{Kind: "redrift", Key: "fp1"}, act.Nudge)
}
