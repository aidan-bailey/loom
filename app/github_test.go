package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
)

// A repo with no GitHub remote fails every poll while CheckCLI keeps
// succeeding (it is not repo-scoped), so the picker must show why
// instead of waiting forever on a result that will never come.
func TestIssuePickerStatus_ReportsPollError(t *testing.T) {
	m := homeWithAppState(t)
	repo := m.repoPath()
	assert.Equal(t, "loading…", m.issuePickerStatus(), "before any poll")

	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{},
		map[string]error{repo: errors.New("gh pr list: exit status 1")}))
	assert.Contains(t, m.issuePickerStatus(), "gh pr list: exit status 1",
		"a failed poll must say so, not sit on loading")

	// A later success clears it.
	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{repo: {Issues: map[int]github.Issue{}}},
		map[string]error{}))
	assert.Equal(t, "", m.issuePickerStatus(), "a later success clears the error")
}
