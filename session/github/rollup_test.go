package github

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rollup(t *testing.T, js string) []rollupItem {
	t.Helper()
	var items []rollupItem
	require.NoError(t, json.Unmarshal([]byte(js), &items))
	return items
}

func TestFoldChecks(t *testing.T) {
	cases := []struct {
		name string
		js   string
		want Checks
	}{
		{"empty", `[]`, ChecksNone},
		{"all success", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"StatusContext","state":"SUCCESS"}]`, ChecksPassing},
		{"one failing wins", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
		{"error is failing", `[{"state":"ERROR"}]`, ChecksFailing},
		{"timed out is failing", `[{"status":"COMPLETED","conclusion":"TIMED_OUT"}]`, ChecksFailing},
		{"cancelled is failing", `[{"status":"COMPLETED","conclusion":"CANCELLED"}]`, ChecksFailing},
		{"in progress is pending", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPending},
		{"queued is pending", `[{"status":"QUEUED"}]`, ChecksPending},
		{"pending context", `[{"state":"PENDING"}]`, ChecksPending},
		{"skipped and neutral count as passing", `[{"status":"COMPLETED","conclusion":"SKIPPED"},{"status":"COMPLETED","conclusion":"NEUTRAL"}]`, ChecksPassing},
		{"unknown conclusion is pending", `[{"status":"COMPLETED","conclusion":"SOMETHING_NEW"}]`, ChecksPending},
		{"expected is pending, not green", `[{"state":"EXPECTED"}]`, ChecksPending},
		{"failing beats pending", `[{"status":"IN_PROGRESS"},{"state":"FAILURE"}]`, ChecksFailing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, foldChecks(rollup(t, c.js)))
		})
	}
}

func TestFoldPRState(t *testing.T) {
	assert.Equal(t, PRDraft, foldPRState("OPEN", true))
	assert.Equal(t, PROpen, foldPRState("OPEN", false))
	assert.Equal(t, PRMerged, foldPRState("MERGED", false))
	assert.Equal(t, PRClosed, foldPRState("CLOSED", false))
	assert.Equal(t, PROpen, foldPRState("WEIRD", false), "unknown strings fold to Open, never crash")
}

func TestFoldReview(t *testing.T) {
	assert.Equal(t, ReviewApproved, foldReview("APPROVED"))
	assert.Equal(t, ReviewChangesRequested, foldReview("CHANGES_REQUESTED"))
	assert.Equal(t, ReviewNone, foldReview("REVIEW_REQUIRED"))
	assert.Equal(t, ReviewNone, foldReview(""))
}

func TestPRPriority_OpenBeatsMergedBeatsClosed(t *testing.T) {
	assert.True(t, prOutranks(PR{State: PROpen}, PR{State: PRMerged}))
	assert.True(t, prOutranks(PR{State: PRDraft}, PR{State: PRMerged}))
	assert.True(t, prOutranks(PR{State: PRMerged}, PR{State: PRClosed}))
	assert.False(t, prOutranks(PR{State: PRClosed}, PR{State: PROpen}))
	assert.True(t, prOutranks(PR{Number: 9, State: PROpen}, PR{Number: 3, State: PROpen}), "same state: newer number wins")
}
