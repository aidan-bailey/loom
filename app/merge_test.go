package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pausedInstanceWithRealWorktree builds a Paused instance backed by a
// real, on-disk git worktree. FromInstanceData sets started=true only
// for Paused instances (session/instance.go:283-286), so this is the
// lightest fixture that gives GetGitWorktree() a resolvable target
// without spinning up tmux (see the design doc's eligibility-rules
// verification note).
func pausedInstanceWithRealWorktree(t *testing.T, repoDir, title, branch string) *session.Instance {
	t.Helper()
	worktreePath := filepath.Join(t.TempDir(), title)
	runGit(t, repoDir, "worktree", "add", "-b", branch, worktreePath)

	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         title,
		Path:          repoDir,
		Branch:        branch,
		Status:        session.Paused,
		Worktree: session.GitWorktreeData{
			RepoPath:         repoDir,
			WorktreePath:     worktreePath,
			SessionName:      title,
			BranchName:       branch,
			IsExistingBranch: true,
		},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	return inst
}

func setupMergeRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "f"), []byte("x"), 0o644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-qm", "init")
	return repoDir
}

func TestRunMergeSelected_BlocksOnIneligibleTarget(t *testing.T) {
	m := newTestHome(t)
	// No selection at all — GetSelectedInstance returns nil, which fails
	// selectedNotBusyNotWorkspace immediately.
	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd")
	assert.Equal(t, stateDefault, m.state, "picker must not open for an ineligible target")
}

func TestRunMergeSelected_BlocksOnDirtyTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	m.ws().AddForTest(target)
	m.syncViews()
	selectIn(m, m.list, target)

	// Make the target worktree dirty.
	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(targetWT.GetWorktreePath(), "dirty.txt"), []byte("wip"), 0o644))

	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd for a dirty target")
	assert.Equal(t, stateDefault, m.state, "picker must not open for a dirty target")
}

func TestRunMergeSelected_BlocksWhenNoEligibleSources(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	m.ws().AddForTest(target)
	m.syncViews()
	selectIn(m, m.list, target)

	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd when there are no other sessions")
	assert.Equal(t, stateDefault, m.state)
}

func TestRunMergeSelected_OpensPickerWithEligibleSources(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	m.ws().AddForTest(target)
	m.ws().AddForTest(source)
	m.syncViews()
	selectIn(m, m.list, target)

	_, cmd := runMergeSelected(m)
	assert.Nil(t, cmd)
	assert.Equal(t, stateMergePicker, m.state)

	mp := m.mergePicker()
	require.NotNil(t, mp)
	row := mp.SelectedRow()
	require.NotNil(t, row)
	assert.Equal(t, "source", row.Title, "the only eligible source should be pre-selected")
}

func TestMerge_MergesBranchIntoTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	m := core.NewForTest(core.Options{})
	ws := testWS(core.WorkspaceParts{}, target, source)
	m.SetWorkspacesForTest(ws)
	m.Merge(m.IDOfForTest(target), m.IDOfForTest(source), 0)
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1, "the merge request's job")
	msg := core.UntrackedForTest(jobs[0]())
	assert.Equal(t, core.MergeResult{}, msg, "successful merge returns no error")

	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(targetWT.GetWorktreePath(), "new.txt"))
	assert.NoError(t, statErr, "target worktree should now contain source's new file")
}

func TestHandleStateMergePickerKey_EscCancelsWithoutMerging(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	m.ws().AddForTest(target)
	m.ws().AddForTest(source)
	m.syncViews()
	selectIn(m, m.list, target)

	_, cmd := runMergeSelected(m)
	require.Nil(t, cmd)
	require.Equal(t, stateMergePicker, m.state)

	_, cmd = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.Nil(t, cmd, "esc must not return a merge Cmd")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.mergePicker())
	assert.Nil(t, m.pendingMergeTarget, "pending target must be cleared on cancel")
	assert.Nil(t, m.pendingMergeSourceItems, "pending source snapshot must be cleared on cancel")

	// Confirm no merge commit happened in target's worktree.
	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	dirty, err := targetWT.IsDirty()
	require.NoError(t, err)
	assert.False(t, dirty, "canceling must not touch the target worktree")
}

func TestHandleStateMergePickerKey_EnterMergesTheDisplayedTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	m.ws().AddForTest(target)
	m.ws().AddForTest(source)
	m.syncViews()
	selectIn(m, m.list, target)

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	_, cmd := runMergeSelected(m)
	require.Nil(t, cmd)
	require.Equal(t, stateMergePicker, m.state)

	_, _ = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	results := requestResults(t, m)
	require.Len(t, results, 1, "enter must request the merge, whose job the drain runs")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingMergeTarget)
	assert.Nil(t, m.pendingMergeSourceItems)

	assert.Equal(t, core.MergeResult{}, results[0], "successful merge returns no error")

	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(targetWT.GetWorktreePath(), "new.txt"))
	assert.NoError(t, statErr, "target worktree should now contain source's new file")
}

// TestRunMergeSelected_TargetSurvivesConcurrentSelectionChange is the
// regression test for the stale-target bug: once the picker is open,
// changing m.list's selection out from under it (simulating a
// background message like a recover completion reassigning selection) must
// NOT change which instance Enter merges into — it must still act on
// the instance that was selected when the picker opened.
func TestRunMergeSelected_TargetSurvivesConcurrentSelectionChange(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	other := pausedInstanceWithRealWorktree(t, repoDir, "other", "other-branch")
	m.ws().AddForTest(target)
	m.ws().AddForTest(source)
	m.ws().AddForTest(other)
	m.syncViews()
	selectIn(m, m.list, target)

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	_, cmd := runMergeSelected(m)
	require.Nil(t, cmd)
	require.Equal(t, stateMergePicker, m.state)

	// Simulate a background message reassigning the list's selection
	// while the picker is open (m.state gates key routing, not Msg
	// handling in Update()).
	selectIn(m, m.list, other)

	_, _ = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	results := requestResults(t, m)
	require.Len(t, results, 1)
	assert.Equal(t, core.MergeResult{}, results[0], "merge should still succeed")

	// The merge must have landed in the ORIGINAL target ("target"), not
	// the instance the list's selection was reassigned to ("other").
	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(targetWT.GetWorktreePath(), "new.txt"))
	assert.NoError(t, statErr, "the ORIGINAL target must receive the merge, not whatever m.list's selection changed to")

	otherWT, err := other.GetGitWorktree()
	require.NoError(t, err)
	otherDirty, err := otherWT.IsDirty()
	require.NoError(t, err)
	assert.False(t, otherDirty, "the reassigned-to instance must be untouched")
}
