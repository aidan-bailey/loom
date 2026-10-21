package app

import (
	"os"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
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

// TestSelectedResumable_AllowsRecoverable confirms the 'r' key
// gate admits a Recoverable orphan (the entry point to the recover action).
func TestSelectedResumable_AllowsRecoverable(t *testing.T) {
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
	list := fixtureList(t)

	h := wireCore(t, &home{workspaceSlot: slotWith(ws, &workspaceSlot{list: list})})
	selectIn(h, list, inst)
	assert.True(t, selectedResumable(h), "'r' must be enabled for a Recoverable orphan")
}

// TestSelectedResumable_APausedWorkspaceTerminal: 'r' resumes a workspace
// terminal left Paused (a relaunch that failed, a tripped breaker), the
// only way to bring one back before the daemon restarts, and stays off for
// a running one. 'R' stays off for both: a terminal's launch follows its
// workspace's config.
func TestSelectedResumable_APausedWorkspaceTerminal(t *testing.T) {
	for _, st := range []session.Status{session.Paused, session.Running} {
		t.Run(st.String(), func(t *testing.T) {
			term, err := session.FromInstanceData(session.InstanceData{
				Title: "main", Path: t.TempDir(), Status: session.Paused, Program: "claude", IsWorkspaceTerminal: true,
			}, t.TempDir())
			require.NoError(t, err)
			require.NoError(t, term.TransitionTo(st))
			ws := testWS(core.WorkspaceParts{}, term)
			list := fixtureList(t)
			h := wireCore(t, &home{workspaceSlot: slotWith(ws, &workspaceSlot{list: list})})
			selectIn(h, list, term)

			assert.Equal(t, st == session.Paused, selectedResumable(h), "'r' only for a Paused terminal")
			assert.False(t, selectedPausedNotWorkspace(h), "never 'R'")
		})
	}
}
