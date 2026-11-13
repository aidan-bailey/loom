package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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
