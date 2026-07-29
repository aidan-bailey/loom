package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// initReviewRepo creates a git repo with one commit, optionally leaving
// a working-tree modification behind. Mirrors review/gitdiff's initRepo.
func initReviewRepo(t *testing.T, dirty bool) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	if dirty {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTWO\nthree\nfour\n"), 0o644))
	}
	return dir
}

// newCodeReviewWorkbenchHome is newReviewWorkbenchHome with a real git
// worktree root, so gitdiff.ChangedFiles has something to enumerate.
func newCodeReviewWorkbenchHome(t *testing.T, dirty bool) (*home, string) {
	t.Helper()
	m := newWorkbenchTestHome(t)

	root := initReviewRepo(t, dirty)

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

	_, _ = handleStateDefaultKey(m, wbKey("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	m.workbench.SetSize(120, 40)
	m.workbench.SetTab(ui.WbTabMarkdown)
	return m, root
}

// TestWorkbenchReview_5StartsCodeReview pins the multi-file entry: with
// no active review, `5` builds a review over the worktree diff, loads
// it, and writes the crit interop manifest.
func TestWorkbenchReview_5StartsCodeReview(t *testing.T) {
	m, root := newCodeReviewWorkbenchHome(t, true)
	require.Nil(t, m.wbReview)

	_, cmd, handled := handleWorkbenchKey(m, wbKey("5"))
	require.True(t, handled)
	require.NotNil(t, cmd, "5 must dispatch load + manifest")
	require.NotNil(t, m.wbReview)
	assert.Equal(t, ui.WbTabReview, m.workbench.Tab())

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "expected a batch of load + manifest cmds")
	for _, c := range batch {
		m.Update(c())
	}
	assert.Contains(t, m.workbench.String(), "a.txt", "loaded review must show the changed file")

	manifest := filepath.Join(root, ".crit", "code-review.yaml")
	_, err := os.Stat(manifest)
	assert.NoError(t, err, "manifest must be written for crit interop")
}

// TestWorkbenchReview_5NoChangesNotifies pins the empty-diff guard: a
// clean worktree surfaces a notice rather than an empty review tab.
func TestWorkbenchReview_5NoChangesNotifies(t *testing.T) {
	m, _ := newCodeReviewWorkbenchHome(t, false)

	_, cmd, handled := handleWorkbenchKey(m, wbKey("5"))
	require.True(t, handled)
	assert.Nil(t, cmd)
	assert.Nil(t, m.wbReview)
	assert.NotEqual(t, ui.WbTabReview, m.workbench.Tab())
	assert.Contains(t, m.errBox.String(), "no changes to review")
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
// TestWorkbenchReview_5NonGitWorktreeErrors pins the failure ladder's
// hard rung: starting a code review in a worktree that isn't a git
// repo surfaces the git error and opens nothing. (The soft "no
// changes" rung is TestWorkbenchReview_5NoChangesNotifies.)
func TestWorkbenchReview_5NonGitWorktreeErrors(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t) // worktree is a plain TempDir, not a repo
	require.Nil(t, m.wbReview)

	_, cmd, handled := handleWorkbenchKey(m, wbKey("5"))
	require.True(t, handled)
	assert.NotNil(t, cmd, "handleError returns its timed-clear Cmd")
	assert.Contains(t, m.errBox.String(), "listing changes")
	assert.NotEqual(t, ui.WbTabReview, m.workbench.Tab())
	assert.Nil(t, m.wbReview)
}

// TestWorkbenchReview_RetargetDropsPane pins the invariant across a
// workbench retarget (]/[ waiting-jump, kill-reselect): instanceChanged
// re-points the panel at a different session, which clears the
// workbench's interface field — home.wbReview must go with it, or keys
// route into an invisible pane and `S` sends session A's comments to
// session B. Drives the real path: select another instance, then call
// instanceChanged exactly as the app does.
func TestWorkbenchReview_RetargetDropsPane(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	enterReview(t, m)
	require.NotNil(t, m.wbReview)
	require.NotNil(t, m.workbench.Review())
	require.Equal(t, ui.WbTabReview, m.workbench.Tab())

	other := t.TempDir()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title:   "b",
		Path:    other,
		Program: "claude",
		Status:  session.Ready,
		Worktree: session.GitWorktreeData{
			RepoPath:     other,
			WorktreePath: other,
			SessionName:  "b",
			BranchName:   "test/b",
		},
	}, t.TempDir())
	require.NoError(t, err)
	m.list.AddInstance(inst)
	m.list.SetSelectedInstance(1)
	require.Equal(t, "b", m.list.GetSelectedInstance().Title)

	m.instanceChanged()

	assert.Equal(t, "b", m.workbench.SessionTitle(), "panel must retarget")
	assert.Nil(t, m.wbReview, "retarget must drop the concrete review pane")
	assert.Nil(t, m.workbench.Review())
	assert.NotEqual(t, ui.WbTabReview, m.workbench.Tab(),
		"no review is open, so the review tab must not stay selected")
}

// TestWorkbenchReview_PausedSessionNotifies pins the excluded-state
// notice: a paused (or Recoverable) session has no live agent, so the
// review entry keys must say so rather than silently no-op.
func TestWorkbenchReview_PausedSessionNotifies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status session.Status
	}{
		{"paused", session.Paused},
		{"recoverable", session.Recoverable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newWorkbenchTestHome(t)
			root := t.TempDir()
			doc := filepath.Join(root, "plan.md")
			require.NoError(t, os.WriteFile(doc, []byte("# Plan\n"), 0o644))
			inst, err := session.FromInstanceData(session.InstanceData{
				Title:   "a",
				Path:    root,
				Program: "claude",
				Status:  tc.status,
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

			assert.Nil(t, m.openDocReview(doc))
			assert.Nil(t, m.wbReview)
			assert.NotEqual(t, ui.WbTabReview, m.workbench.Tab())
			assert.Contains(t, m.errBox.String(), "resume it before reviewing")

			m.errBox.Clear()
			assert.Nil(t, m.openCodeReview())
			assert.Nil(t, m.wbReview)
			assert.Contains(t, m.errBox.String(), "resume it before reviewing")
		})
	}
}

// TestWorkbenchReview_QReturnsToOriginTab pins the exit target: a code
// review started from the diff tab returns to the diff tab, not to
// markdown.
func TestWorkbenchReview_QReturnsToOriginTab(t *testing.T) {
	m, _ := newCodeReviewWorkbenchHome(t, true)
	_, _, handled := handleWorkbenchKey(m, wbKey("2"))
	require.True(t, handled)
	require.Equal(t, ui.WbTabDiff, m.workbench.Tab())

	_, _, handled = handleWorkbenchKey(m, wbKey("5"))
	require.True(t, handled)
	require.Equal(t, ui.WbTabReview, m.workbench.Tab())

	_, _, handled = handleWorkbenchKey(m, wbKey("q"))
	require.True(t, handled)
	assert.Equal(t, ui.WbTabDiff, m.workbench.Tab(),
		"q must return to the tab the review was opened from")
	assert.Nil(t, m.wbReview)
}

// TestWorkbenchReview_ScrollRoutesToPane pins the wheel/j-k routing on
// the review tab: the pane's cursor moves rather than the tick being
// swallowed.
func TestWorkbenchReview_ScrollRoutesToPane(t *testing.T) {
	m, _ := newReviewWorkbenchHome(t)
	m.workbench.SetSize(120, 40)
	enterReview(t, m)

	before := m.workbench.Review().View()
	m.workbenchScrollDown()
	assert.NotEqual(t, before, m.workbench.Review().View(),
		"scrolling the review tab must move its cursor")
	m.workbenchScrollUp()
	assert.Equal(t, before, m.workbench.Review().View(),
		"scrolling back must restore the original cursor position")
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
