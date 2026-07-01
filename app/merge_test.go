package app

import (
	"os"
	"path/filepath"
	"testing"

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
