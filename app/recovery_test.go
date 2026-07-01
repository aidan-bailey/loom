package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"charm.land/bubbles/v2/spinner"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// TestReconcileOrphans_CleanAutoRemoved_DirtyBecomesRecoverable exercises the
// full reconcile path against real git worktrees: a clean orphan is auto-removed
// (branch preserved), a dirty orphan surfaces as an inline Recoverable entry.
func TestReconcileOrphans_CleanAutoRemoved_DirtyBecomesRecoverable(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")

	cfgDir := t.TempDir()
	userDir := filepath.Join(cfgDir, "worktrees", "u")
	require.NoError(t, os.MkdirAll(userDir, 0o755))

	cleanWT := filepath.Join(userDir, "clean_dead0001")
	dirtyWT := filepath.Join(userDir, "dirty_dead0002")
	runGit(t, repo, "worktree", "add", "-b", "u/clean", cleanWT)
	runGit(t, repo, "worktree", "add", "-b", "u/dirty", dirtyWT)
	// Make the dirty one dirty (uncommitted change).
	require.NoError(t, os.WriteFile(filepath.Join(dirtyWT, "UNSAVED.txt"), []byte("wip"), 0o644))

	sp := spinner.New()
	list := ui.NewList(&sp)
	h := &home{}

	summary := h.reconcileOrphans(cfgDir, "true", list, nil, cmd2.MakeExecutor())

	assert.Equal(t, 1, summary.cleaned, "clean orphan should be auto-removed")
	assert.Equal(t, 1, summary.review, "dirty orphan should surface for review")
	assert.NoDirExists(t, cleanWT, "clean worktree dir should be removed")
	assert.DirExists(t, dirtyWT, "dirty worktree dir must be preserved")

	insts := list.GetInstances()
	require.Len(t, insts, 1, "exactly the dirty orphan is added inline")
	assert.Equal(t, session.Recoverable, insts[0].GetStatus())
	assert.Equal(t, "dirty", insts[0].Title)

	// The placeholder must round-trip to InstanceData that preserves the
	// worktree + IsExistingBranch — the contract that lets recover adopt the
	// existing worktree in place and discard keep the branch.
	rt := insts[0].ToInstanceData()
	assert.Equal(t, dirtyWT, rt.Worktree.WorktreePath)
	assert.True(t, rt.Worktree.IsExistingBranch)
	assert.Equal(t, "u/dirty", rt.Branch)

	// Branch of the auto-cleaned worktree must survive.
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "u/clean").Output()
	assert.Contains(t, string(out), "u/clean")
}

// TestSelectedResumableNotWorkspace_AllowsRecoverable confirms the 'r' key
// gate admits a Recoverable orphan (the entry point to the recover action).
func TestSelectedResumableNotWorkspace_AllowsRecoverable(t *testing.T) {
	data := session.InstanceData{
		SchemaVersion: session.CurrentSchemaVersion,
		Title:         "orphan",
		Path:          t.TempDir(),
		Branch:        "u/orphan",
		Status:        session.Recoverable,
		Worktree: session.GitWorktreeData{
			RepoPath:         t.TempDir(),
			WorktreePath:     t.TempDir(),
			BranchName:       "u/orphan",
			IsExistingBranch: true,
		},
	}
	inst, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)

	sp := spinner.New()
	list := ui.NewList(&sp)
	list.AddInstance(inst)()
	list.SelectInstance(inst)

	h := &home{list: list}
	assert.True(t, selectedResumableNotWorkspace(h), "'r' must be enabled for a Recoverable orphan")
}
