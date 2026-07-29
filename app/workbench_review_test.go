package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/review"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
)

// newReviewWorkbenchHome builds a workbench home whose selected instance
// reports a real worktree root (openDocReview refuses instances without
// one), sitting in workbench mode on the markdown tab with a document
// loaded and follow mode on — the state the `c` key expects.
func newReviewWorkbenchHome(t *testing.T) (*home, string) {
	t.Helper()
	m := newWorkbenchTestHome(t)

	root := t.TempDir()
	doc := filepath.Join(root, "plan.md")
	require.NoError(t, os.WriteFile(doc, []byte("# Plan\n\nfirst line\n"), 0o644))

	inst, err := session.FromInstanceData(session.InstanceData{
		Title:   "a",
		Path:    root,
		Program: "claude",
		Status:  session.Ready,
		Worktree: session.GitWorktreeData{
			RepoPath:     root,
			WorktreePath: root,
			SessionName:  "a",
			BranchName:   "test/a",
		},
	}, t.TempDir())
	require.NoError(t, err)
	m.list.AddInstance(inst)
	m.list.SetSelectedInstance(0)

	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	require.Equal(t, root, sel.GetWorktreePath(), "test instance must expose a worktree root")

	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	m.workbench.SetTab(ui.WbTabMarkdown)
	m.workbench.Markdown.SetDocument(doc, "# Plan\n\nfirst line\n", time.Now())
	m.workbench.Markdown.SetFollowing(true)
	return m, doc
}

// enterReview drives the `c` entry key and delivers the load result so
// the pane is in its normal, loaded state.
func enterReview(t *testing.T, m *home) {
	t.Helper()
	_, cmd, handled := handleWorkbenchKey(m, wbKey("c"))
	require.True(t, handled)
	require.NotNil(t, cmd, "c must dispatch the pane's load Cmd")
	require.NotNil(t, m.wbReview)
	m.Update(cmd())
	require.Equal(t, ui.WbTabReview, m.workbench.Tab())
}

// TestWorkbenchReview_CFreezesAndOpens pins the entry contract: `c` on a
// markdown doc opens the review tab and freezes follow mode so line
// anchors can't rot under a live agent.
func TestWorkbenchReview_CFreezesAndOpens(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	require.True(t, m.workbench.Markdown.Following())

	_, cmd, handled := handleWorkbenchKey(m, wbKey("c"))
	require.True(t, handled)
	assert.NotNil(t, cmd, "entry must dispatch the review load")
	assert.Equal(t, ui.WbTabReview, m.workbench.Tab())
	assert.NotNil(t, m.wbReview)
	assert.NotNil(t, m.workbench.Review())
	assert.False(t, m.workbench.Markdown.Following(),
		"opening a review must freeze follow mode")
}

// TestWorkbenchReview_CIgnoredWhileEditing pins the edit guard: the
// editor owns every key, so `c` types a character rather than opening a
// review.
func TestWorkbenchReview_CIgnoredWhileEditing(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	require.True(t, m.workbench.Markdown.StartEdit())

	_, _, handled := handleWorkbenchKey(m, wbKey("c"))
	require.True(t, handled)
	assert.Equal(t, ui.WbTabMarkdown, m.workbench.Tab())
	assert.Nil(t, m.wbReview, "c must not open a review while editing")
}

// TestWorkbenchReview_QReturnsToMarkdownAndResumesFollow pins the exit
// counterpart: q leaves the review and un-freezes the markdown pane.
func TestWorkbenchReview_QReturnsToMarkdownAndResumesFollow(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	enterReview(t, m)

	_, _, handled := handleWorkbenchKey(m, wbKey("q"))
	require.True(t, handled)
	assert.Equal(t, ui.WbTabMarkdown, m.workbench.Tab())
	assert.Nil(t, m.wbReview)
	assert.Nil(t, m.workbench.Review())
	assert.True(t, m.workbench.Markdown.Following(),
		"leaving the review must resume follow mode")
}

// TestWorkbenchReview_EscExitsWorkbenchWhenIdle pins the fall-through:
// the pane declines idle esc, so workbench exit still works from the
// review tab (and cleanup drops the pane).
func TestWorkbenchReview_EscExitsWorkbenchWhenIdle(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	enterReview(t, m)

	_, _, handled := handleWorkbenchKey(m, wbKey("esc"))
	require.True(t, handled)
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Nil(t, m.wbReview, "workbench teardown must drop the review pane")
}

// TestWorkbenchReview_CleanupClearsPane pins the teardown choke point
// directly: cleanupWorkbench alone restores the invariant.
func TestWorkbenchReview_CleanupClearsPane(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	enterReview(t, m)

	m.cleanupWorkbench()
	assert.Nil(t, m.wbReview)
	assert.Nil(t, m.workbench.Review())
	assert.True(t, m.workbench.Markdown.Following())
}

// TestWorkbenchReview_5WithoutReviewNotifies pins the tab key's guard:
// with no active review, `5` must not land on an empty review tab.
func TestWorkbenchReview_5WithoutReviewNotifies(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	require.Nil(t, m.wbReview)

	_, _, handled := handleWorkbenchKey(m, wbKey("5"))
	require.True(t, handled)
	assert.NotEqual(t, ui.WbTabReview, m.workbench.Tab())
	assert.Nil(t, m.wbReview)
}

// aliveTmuxSessionForTest builds a TmuxSession whose DoesSessionExist
// (and thus Instance.TmuxAlive) reports true without touching a real
// tmux server — mirrors addReadyInstance's fixture in
// app_scripts_dispatch_test.go.
func aliveTmuxSessionForTest(t *testing.T, name string) *tmux.TmuxSession {
	t.Helper()
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	return tmux.NewTmuxSessionWithDeps(name, "true", fakePtyFactory{t: t}, cmdExec)
}

// TestWorkbenchReview_SendNoComments pins the zero-comment guard: `S`
// with an empty review must not open the confirm overlay, and must
// surface an info notice via errBox. The harness instance has no live
// tmux session (TmuxAlive()==false), so the liveness guard actually
// fires first — that is asserted directly, and the zero-comment intent
// is covered separately via a direct ComposePrompt assertion below.
func TestWorkbenchReview_SendNoComments(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	enterReview(t, m)
	require.NotNil(t, m.wbReview)
	require.Equal(t, 0, m.wbReview.CommentCount())

	cmd := m.sendReviewCmd()
	assert.Nil(t, cmd, "guard path returns no confirm cmd")
	assert.NotEqual(t, stateConfirm, m.state, "must not open the confirm overlay")
	assert.Contains(t, m.errBox.String(), "agent is not running",
		"harness instance has no live tmux session, so the liveness guard fires")

	// Direct unit coverage of the zero-comment intent itself, independent
	// of which guard the harness happens to trip first.
	assert.Equal(t, "", review.ComposePrompt(m.wbReview.Root(), m.wbReview.States()))
}

// TestWorkbenchReview_SendOpensConfirm pins the happy path: with
// comments loaded and a live tmux session attached, `S` opens the
// confirm overlay rather than sending immediately.
func TestWorkbenchReview_SendOpensConfirm(t *testing.T) {
	m, doc := newReviewWorkbenchHome(t)
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)
	sel.SetTmuxSession(aliveTmuxSessionForTest(t, "a"))
	require.True(t, sel.TmuxAlive(), "fixture precondition")

	root := sel.GetWorktreePath()
	require.NoError(t, review.Save(root, &review.ReviewState{
		File: doc,
		Comments: []review.Comment{
			{ID: "a", Line: 1, Body: "fix"},
			{ID: "b", Line: 2, Body: "also"},
		},
	}))

	enterReview(t, m)
	require.NotNil(t, m.wbReview)
	require.Equal(t, 2, m.wbReview.CommentCount(), "seeded comments must load into the pane")

	// confirmTask itself always returns nil (the overlay drives dispatch
	// from here on) — the real signal is the state flip below.
	_ = m.sendReviewCmd()
	assert.Equal(t, stateConfirm, m.state, "S must open the confirm overlay")
	assert.NotNil(t, m.pendingConfirmation.Sync, "pending task must carry the send side-effect")
}
