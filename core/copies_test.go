package core

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
)

// The model's queries hand out copies: a caller that changes what it got
// back changes nothing the model holds (daemon stage 1D, decision 9). Each
// test changes a result and queries again.

func TestGitHubSnapshot_IsACopy(t *testing.T) {
	m := NewForTest(Options{})
	m.ghState = map[string]github.Snapshot{"/repo": {
		PRs:    map[string]github.PR{"me/x": {Number: 1}},
		Issues: map[int]github.Issue{7: {Number: 7, Labels: []string{"bug"}}},
	}}
	snap, ok := m.GitHubSnapshot("/repo")
	assert.True(t, ok)
	snap.PRs["me/y"] = github.PR{Number: 2}
	snap.Issues[7].Labels[0] = "changed"
	snap.Issues[8] = github.Issue{Number: 8}

	again, _ := m.GitHubSnapshot("/repo")
	assert.Len(t, again.PRs, 1)
	assert.Len(t, again.Issues, 1)
	assert.Equal(t, []string{"bug"}, again.Issues[7].Labels)
}

func TestAccountUsage_IsACopy(t *testing.T) {
	m := NewForTest(Options{})
	m.SetAccountUsageForTest("work", account.Usage{Available: true, FiveHour: &account.Window{Pct: 10}}, nil)
	u, _ := m.AccountUsage("work")
	u.FiveHour.Pct = 99

	again, _ := m.AccountUsage("work")
	assert.InDelta(t, 10.0, again.FiveHour.Pct, 0)
}

func TestAccountSync_IsACopy(t *testing.T) {
	m := NewForTest(Options{})
	m.SetAccountSyncForTest("work", account.SyncReport{Linked: []string{"a"}, Diverged: []string{"b"}})
	rep, _ := m.AccountSync("work")
	rep.Linked[0] = "changed"
	rep.Diverged[0] = "changed"

	again, _ := m.AccountSync("work")
	assert.Equal(t, []string{"a"}, again.Linked)
	assert.Equal(t, []string{"b"}, again.Diverged)
}

func TestWorkspaces_IsACopy(t *testing.T) {
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(a, b)
	got := m.Workspaces()
	got[0].Name = "changed"
	got[0].PreservedTitles = append(got[0].PreservedTitles, "x")
	again := m.Workspaces()
	assert.Equal(t, "a", again[0].Name)
	assert.Empty(t, again[0].PreservedTitles)
	assert.Equal(t, []*Workspace{a, b}, m.workspaces)
}
