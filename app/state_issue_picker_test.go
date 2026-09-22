package app

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunNewFromIssue_OpensPickerFromSnapshot(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.ghState = map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{
		12: {Number: 12, Title: "Fix"}, 13: {Number: 13, Title: "Closed one", Closed: true},
	}}}
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	p := m.issuePicker()
	require.NotNil(t, p)
	assert.Equal(t, []int{12}, p.VisibleNumbers(), "closed issues are not offered")
}

func TestRunNewFromIssue_NoSnapshotShowsLoadingAndForcesPoll(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.gate(gateGH).last = time.Now()
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	assert.True(t, m.gate(gateGH).due(time.Now()))
	assert.Contains(t, m.issuePicker().Render(), "loading")
}

func TestRunNewFromIssue_ShowsPollErrorInsteadOfLoading(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true, checkedAt: time.Now()}
	m.ghErrs = map[string]error{m.repoPath(): errors.New("no GitHub remote")}
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	assert.Contains(t, m.issuePicker().Render(), "no GitHub remote")
}

func TestRunNewFromIssue_UnavailableGHErrors(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh"}
	_, cmd := runNewFromIssue(m)
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd, "error surfaces via handleError")
}

func TestGHReadyRefreshesOpenPicker(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	_, _ = runNewFromIssue(m)
	m.Update(ghReadyMsg{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{7: {Number: 7, Title: "New"}}}},
	})
	assert.Equal(t, []int{7}, m.issuePicker().VisibleNumbers())
}

func TestIssuePickerEnter_DispatchesView(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	m.ghState = map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}}}}
	_, _ = runNewFromIssue(m)
	_, cmd := handleStateIssuePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.activeOverlay)
	assert.NotNil(t, cmd)
}

func TestIssuePickedMsg_CreatesLinkedInstanceAndOpensLaunchOptions(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	before := m.list.NumInstances()
	m.gate(gateGH).last = time.Now()
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "Fix flaky test", URL: "https://x/12", Body: "do it"}})
	require.Equal(t, before+1, m.list.NumInstances())
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	assert.Equal(t, "gh-12-fix-flaky-test", inst.Title)
	assert.Equal(t, 12, inst.IssueNumber())
	assert.Contains(t, inst.Prompt, "# Fix flaky test")
	assert.Equal(t, stateLaunchOptions, m.state)
	_, ok := m.activeOverlay.(*overlay.SessionLaunchOptions)
	assert.True(t, ok)
	assert.True(t, m.gate(gateGH).due(time.Now()), "an issue-born session forces the next poll")
}

func TestIssuePickedMsg_ErrorCreatesNothing(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{err: errors.New("boom")})
	assert.Equal(t, before, m.list.NumInstances())
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd)
}

// The issue picker names the session itself (SlugTitle), so it needs the
// same guard as typed titles: never create over a preserved record's title.
func TestIssuePickedMsg_RejectsTitleOfPreservedRecord(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.storage = preservedTitleStorage(t, "gh-12-fix")
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "Fix"}})
	assert.Equal(t, before, m.list.NumInstances(), "no session is created under a preserved title")
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd, "and the user is told why")
}

func TestIssuePickedMsg_RespectsInstanceLimit(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	for i := 0; i < GlobalInstanceLimit; i++ {
		inst, err := session.NewInstance(session.InstanceOptions{Title: "x", Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		m.list.AddInstance(inst)
	}
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 1, Title: "t"}})
	assert.Equal(t, GlobalInstanceLimit, m.list.NumInstances())
}

// The fetch is async and the user can switch workspaces during it. A
// result that lands under a different repo must not create a session
// there: its title, prompt and issue link all describe another repo.
func TestIssuePickedMsg_WrongRepoCreatesNothing(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{repo: "/somewhere/else", issue: github.Issue{Number: 12, Title: "Fix"}})
	assert.Equal(t, before, m.list.NumInstances(), "a result from another workspace creates nothing")
	assert.NotNil(t, cmd, "and says so")
}

// handleIssuePicked is reachable from any state, and opening the launch
// options modal would replace whatever overlay and pending closure are
// already there — stranding that instance unstarted.
func TestIssuePickedMsg_DoesNotClobberAnotherFlow(t *testing.T) {
	m := newTestHomeWithActiveCtx(t)
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "First"}})
	require.Equal(t, stateLaunchOptions, m.state)
	first := m.list.NumInstances()

	_, cmd := m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 13, Title: "Second"}})
	assert.Equal(t, first, m.list.NumInstances(), "a second result must not create a rival instance")
	assert.NotNil(t, cmd, "and says so")
}
