package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateFor_UnknownWhenNoSnapshot(t *testing.T) {
	s := StateFor(Snapshot{}, false, "u/b", 12)
	assert.False(t, s.Known)
	assert.False(t, s.HasPR)
	assert.Equal(t, 0, s.IssueNumber, "nothing is reported when the repo was not queried")
}

func TestStateFor_JoinsPRByBranchAndIssueByNumber(t *testing.T) {
	snap := Snapshot{
		PRs:    map[string]PR{"u/b": {Number: 45, State: PROpen, Review: ReviewApproved, Checks: ChecksPassing}},
		Issues: map[int]Issue{12: {Number: 12, Title: "Fix it", Closed: true}},
	}
	s := StateFor(snap, true, "u/b", 12)
	assert.True(t, s.Known)
	assert.True(t, s.HasPR)
	assert.Equal(t, 45, s.PRNumber)
	assert.Equal(t, PROpen, s.PRState)
	assert.Equal(t, ReviewApproved, s.Review)
	assert.Equal(t, ChecksPassing, s.Checks)
	assert.Equal(t, 12, s.IssueNumber)
	assert.Equal(t, "Fix it", s.IssueTitle)
	assert.True(t, s.IssueClosed)
}

func TestStateFor_KnownWithoutPR(t *testing.T) {
	s := StateFor(Snapshot{PRs: map[string]PR{}}, true, "u/other", 0)
	assert.True(t, s.Known)
	assert.False(t, s.HasPR)
	assert.Equal(t, 0, s.IssueNumber)
}

func TestStateFor_LinkedIssueMissingFromSnapshotKeepsNumber(t *testing.T) {
	// The number is the user's link; a snapshot that lacks the issue
	// (backfill failed) must still show "#12" rather than dropping it.
	s := StateFor(Snapshot{}, true, "u/b", 12)
	assert.Equal(t, 12, s.IssueNumber)
	assert.Equal(t, "", s.IssueTitle)
}
