package app

import (
	"os"
	"os/exec"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/aidan-bailey/loom/core"
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

	ws := testWS(core.WorkspaceParts{}, inst)
	sp := spinner.New()
	list := ui.NewList(&sp, ws)
	list.SelectInstance(inst)

	h := wireCore(t, &home{workspaceSlot: &workspaceSlot{ws: ws, list: list}})
	assert.True(t, selectedResumableNotWorkspace(h), "'r' must be enabled for a Recoverable orphan")
}
