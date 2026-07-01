package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	_ = m.list.AddInstance(target)
	m.list.SelectInstance(target)

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
	_ = m.list.AddInstance(target)
	m.list.SelectInstance(target)

	_, cmd := runMergeSelected(m)
	require.NotNil(t, cmd, "expected an error Cmd when there are no other sessions")
	assert.Equal(t, stateDefault, m.state)
}

func TestRunMergeSelected_OpensPickerWithEligibleSources(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	_ = m.list.AddInstance(target)
	_ = m.list.AddInstance(source)
	m.list.SelectInstance(target)

	_, cmd := runMergeSelected(m)
	assert.Nil(t, cmd)
	assert.Equal(t, stateMergePicker, m.state)

	mp := m.mergePicker()
	require.NotNil(t, mp)
	row := mp.SelectedRow()
	require.NotNil(t, row)
	assert.Equal(t, "source", row.Title, "the only eligible source should be pre-selected")
}

func TestMergeActionFor_MergesBranchIntoTarget(t *testing.T) {
	repoDir := setupMergeRepo(t)
	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	cmd := mergeActionFor(target, source)
	msg := cmd()
	assert.Nil(t, msg, "successful merge returns nil, matching pushActionFor's convention")

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
	_ = m.list.AddInstance(target)
	_ = m.list.AddInstance(source)
	m.list.SelectInstance(target)

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
	_ = m.list.AddInstance(target)
	_ = m.list.AddInstance(source)
	m.list.SelectInstance(target)

	sourceWT, err := source.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sourceWT.GetWorktreePath(), "new.txt"), []byte("new"), 0o644))
	runGit(t, sourceWT.GetWorktreePath(), "add", ".")
	runGit(t, sourceWT.GetWorktreePath(), "commit", "-qm", "add new.txt")

	_, cmd := runMergeSelected(m)
	require.Nil(t, cmd)
	require.Equal(t, stateMergePicker, m.state)

	_, cmd = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "enter must return the merge Cmd")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingMergeTarget)
	assert.Nil(t, m.pendingMergeSourceItems)

	msg := cmd()
	assert.Nil(t, msg, "successful merge returns nil")

	targetWT, err := target.GetGitWorktree()
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(targetWT.GetWorktreePath(), "new.txt"))
	assert.NoError(t, statErr, "target worktree should now contain source's new file")
}

// TestRunMergeSelected_TargetSurvivesConcurrentSelectionChange is the
// regression test for the stale-target bug: once the picker is open,
// changing m.list's selection out from under it (simulating a
// background message like recoverDoneMsg reassigning selection) must
// NOT change which instance Enter merges into — it must still act on
// the instance that was selected when the picker opened.
func TestRunMergeSelected_TargetSurvivesConcurrentSelectionChange(t *testing.T) {
	repoDir := setupMergeRepo(t)
	m := newTestHome(t)

	target := pausedInstanceWithRealWorktree(t, repoDir, "target", "target-branch")
	source := pausedInstanceWithRealWorktree(t, repoDir, "source", "source-branch")
	other := pausedInstanceWithRealWorktree(t, repoDir, "other", "other-branch")
	_ = m.list.AddInstance(target)
	_ = m.list.AddInstance(source)
	_ = m.list.AddInstance(other)
	m.list.SelectInstance(target)

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
	m.list.SelectInstance(other)

	_, cmd = handleStateMergePickerKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := cmd()
	assert.Nil(t, msg, "merge should still succeed")

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
