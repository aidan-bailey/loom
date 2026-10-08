package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunNewFromIssue_OpensPickerFromSnapshot(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "", map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{
		12: {Number: 12, Title: "Fix"}, 13: {Number: 13, Title: "Closed one", Closed: true},
	}}}, nil))
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	p := m.issuePicker()
	require.NotNil(t, p)
	assert.Equal(t, []int{12}, p.VisibleNumbers(), "closed issues are not offered")
}

// TestView_DrawsTheIssuePicker: View places the open picker over the
// screen, as it does every other modal's overlay. It was left out of that
// list, so the picker took keys while nothing showed it.
func TestView_DrawsTheIssuePicker(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	deliver(t, m, core.GitHubResultForTest(true, "", map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{
		12: {Number: 12, Title: "Fix the frobnicator"},
	}}}, nil))
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)

	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "New session from GitHub issue")
	assert.Contains(t, screen, "Fix the frobnicator")
	assert.Len(t, strings.Split(screen, "\n"), 40, "drawn over the screen, not below it")
}

func TestRunNewFromIssue_NoSnapshotShowsLoadingAndForcesPoll(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "", nil, nil))
	testModel(m).SetGateForTest("github", false, time.Now())
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	_, _, due := testModel(m).GateForTest("github", time.Now())
	assert.True(t, due)
	assert.Contains(t, m.issuePicker().Render(), "loading")
}

func TestRunNewFromIssue_ShowsPollErrorInsteadOfLoading(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "", nil, map[string]error{m.repoPath(): errors.New("no GitHub remote")}))
	_, _ = runNewFromIssue(m)
	require.Equal(t, stateIssuePicker, m.state)
	assert.Contains(t, m.issuePicker().Render(), "no GitHub remote")
}

func TestRunNewFromIssue_UnavailableGHErrors(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(false, "no gh", nil, nil))
	_, cmd := runNewFromIssue(m)
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd, "error surfaces via handleError")
}

func TestGHResultRefreshesOpenPicker(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "", nil, nil))
	_, _ = runNewFromIssue(m)
	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{7: {Number: 7, Title: "New"}}}}, nil))
	assert.Equal(t, []int{7}, m.issuePicker().VisibleNumbers())
}

func TestIssuePickerEnter_DispatchesView(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}}}}, nil))
	_, _ = runNewFromIssue(m)
	_, _ = handleStateIssuePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.activeOverlay)
	assert.NotNil(t, requestJob(t, m), "the fetch is a request: the model queued its job")
}

func TestIssuePickedMsg_CreatesLinkedInstanceAndOpensLaunchOptions(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	before := m.list.NumInstances()
	testModel(m).SetGateForTest("github", false, time.Now())
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "Fix flaky test", URL: "https://x/12", Body: "do it"}})
	require.Equal(t, before+1, m.list.NumInstances())
	d := m.draft // the session-to-be, until Launch Options confirms
	require.NotNil(t, d)
	assert.Equal(t, "gh-12-fix-flaky-test", d.title)
	assert.Equal(t, 12, d.issue)
	assert.Contains(t, d.prompt, "# Fix flaky test")
	assert.Equal(t, stateLaunchOptions, m.state)
	_, ok := m.activeOverlay.(*overlay.SessionLaunchOptions)
	assert.True(t, ok)
	_, _, due := testModel(m).GateForTest("github", time.Now())
	assert.True(t, due, "an issue-born session forces the next poll")
}

func TestIssuePickedMsg_ErrorCreatesNothing(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{err: errors.New("boom")})
	assert.Equal(t, before, m.list.NumInstances())
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd)
}

// The issue picker names the session itself (SlugTitle), so it needs the
// same guard as typed titles: never create over a preserved record's title.
func TestIssuePickedMsg_RejectsTitleOfPreservedRecord(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	reworkspace(t, m, m.workspaceSlot, func(p *core.WorkspaceParts) { p.Storage = preservedTitleStorage(t, "gh-12-fix") })
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "Fix"}})
	assert.Equal(t, before, m.list.NumInstances(), "no session is created under a preserved title")
	assert.Equal(t, stateDefault, m.state)
	assert.NotNil(t, cmd, "and the user is told why")
}

func TestIssuePickedMsg_RespectsInstanceLimit(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	for i := 0; i < GlobalInstanceLimit; i++ {
		inst, err := session.NewInstance(session.InstanceOptions{Title: "x", Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		m.ws().AddForTest(inst)
		m.syncViews()
	}
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 1, Title: "t"}})
	assert.Equal(t, GlobalInstanceLimit, m.list.NumInstances())
}

// The fetch is async and the user can switch workspaces during it. A
// result that lands under a different repo must not create a session
// there: its title, prompt and issue link all describe another repo.
func TestIssuePickedMsg_WrongRepoCreatesNothing(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	before := m.list.NumInstances()
	_, cmd := m.Update(issuePickedMsg{repo: "/somewhere/else", issue: github.Issue{Number: 12, Title: "Fix"}})
	assert.Equal(t, before, m.list.NumInstances(), "a result from another workspace creates nothing")
	assert.NotNil(t, cmd, "and says so")
}

// handleIssuePicked is reachable from any state, and opening the launch
// options modal would replace whatever overlay and pending closure are
// already there — stranding that instance unstarted.
func TestIssuePickedMsg_DoesNotClobberAnotherFlow(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 12, Title: "First"}})
	require.Equal(t, stateLaunchOptions, m.state)
	first := m.list.NumInstances()

	_, cmd := m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 13, Title: "Second"}})
	assert.Equal(t, first, m.list.NumInstances(), "a second result must not create a rival instance")
	assert.NotNil(t, cmd, "and says so")
}

// issueReq is the ReqID of the one issue fetch m is waiting on.
func issueReq(t *testing.T, m *home) core.ReqID {
	t.Helper()
	var reqs []core.ReqID
	for req, p := range m.pending {
		if p.issue != nil {
			reqs = append(reqs, req)
		}
	}
	require.Len(t, reqs, 1, "one issue fetch is in flight")
	return reqs[0]
}

// TestIssuePickerEnter_TheFetchsReplyOpensTheDraft: the pick is a request
// (core.FetchIssue); the Reply carrying the issue reaches
// handleIssuePicked, which opens the draft and Launch Options, as the
// fetch's own message did. Its job is delivered as its result, since its
// gh call can't run here.
func TestIssuePickerEnter_TheFetchsReplyOpensTheDraft(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}}}}, nil))
	_, _ = runNewFromIssue(m)
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, stateDefault, m.state, "the picker closed while the issue is fetched")
	req := issueReq(t, m)

	deliver(t, m, core.FetchedIssueForTest(req, github.Issue{Number: 12, Title: "Fix flaky test", URL: "https://x/12", Body: "do it"}, nil))

	assert.Empty(t, m.pending, "the Reply was handled")
	require.NotNil(t, m.draft)
	assert.Equal(t, "gh-12-fix-flaky-test", m.draft.title)
	assert.Equal(t, 12, m.draft.issue)
	assert.Equal(t, stateLaunchOptions, m.state)
}

// TestIssuePickerEnter_AFailedFetchCreatesNothing: a fetch whose Reply
// carries an error reaches handleIssuePicked's error branch.
func TestIssuePickerEnter_AFailedFetchCreatesNothing(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	m.errBox.SetSize(400, 1)
	deliver(t, m, core.GitHubResultForTest(true, "",
		map[string]github.Snapshot{m.repoPath(): {Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}}}}, nil))
	_, _ = runNewFromIssue(m)
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	req := issueReq(t, m)

	deliver(t, m, core.FetchedIssueForTest(req, github.Issue{}, errors.New("boom")))

	assert.Nil(t, m.draft)
	assert.Equal(t, stateDefault, m.state)
	assert.Contains(t, m.errBox.String(), "fetch issue: boom")
}
