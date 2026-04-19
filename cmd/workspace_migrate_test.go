package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/aidan-bailey/loom/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// writeGlobalState writes a state.json into dir with the given
// migrationInstance records serialized as its "instances" field.
func writeGlobalState(t *testing.T, dir string, insts []migrationInstance) {
	t.Helper()

	instancesJSON, err := json.Marshal(insts)
	assert.NoError(t, err)

	state := struct {
		HelpScreensSeen uint32          `json:"help_screens_seen"`
		InstancesData   json.RawMessage `json:"instances"`
	}{
		InstancesData: instancesJSON,
	}
	data, err := json.Marshal(state)
	assert.NoError(t, err)
	assert.NoError(t, config.AtomicWriteFile(filepath.Join(dir, config.StateFileName), data, 0644))
}

func TestPlanMigration_EmptyState(t *testing.T) {
	globalDir := t.TempDir()
	// Write a state.json containing the empty-array sentinel.
	writeGlobalState(t, globalDir, []migrationInstance{})

	reg := &config.WorkspaceRegistry{}
	plan, err := planMigration(globalDir, reg)
	assert.NoError(t, err)
	assert.Nil(t, plan, "empty global state produces no plan")
}

func TestPlanMigration_GroupsByWorkspace(t *testing.T) {
	tempHome := t.TempDir()
	globalDir := filepath.Join(tempHome, "global")
	assert.NoError(t, os.MkdirAll(globalDir, 0755))

	repoA := filepath.Join(tempHome, "repoA")
	repoB := filepath.Join(tempHome, "repoB")
	assert.NoError(t, os.MkdirAll(repoA, 0755))
	assert.NoError(t, os.MkdirAll(repoB, 0755))

	writeGlobalState(t, globalDir, []migrationInstance{
		{Title: "a1", Worktree: migrationWorktreeData{RepoPath: repoA, WorktreePath: filepath.Join(globalDir, "worktrees", "a1")}},
		{Title: "b1", Worktree: migrationWorktreeData{RepoPath: repoB, WorktreePath: filepath.Join(globalDir, "worktrees", "b1")}},
		{Title: "orphan", Worktree: migrationWorktreeData{RepoPath: filepath.Join(tempHome, "missing-repo")}},
	})

	reg := &config.WorkspaceRegistry{
		Workspaces: []config.Workspace{
			{Name: "alpha", Path: repoA},
			{Name: "beta", Path: repoB},
		},
	}

	plan, err := planMigration(globalDir, reg)
	assert.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Len(t, plan.byWorkspace["alpha"], 1)
	assert.Len(t, plan.byWorkspace["beta"], 1)
	assert.Len(t, plan.unmatchedData, 1)
	assert.Equal(t, "orphan", plan.unmatchedData[0].Title)
	assert.Len(t, plan.moves, 2, "both matched instances have worktree paths under globalDir")
}

func TestPlanMigration_RewritesWorktreePath(t *testing.T) {
	tempHome := t.TempDir()
	globalDir := filepath.Join(tempHome, "global")
	assert.NoError(t, os.MkdirAll(globalDir, 0755))

	repoA := filepath.Join(tempHome, "repoA")
	assert.NoError(t, os.MkdirAll(repoA, 0755))

	oldPath := filepath.Join(globalDir, "worktrees", "session-xyz")
	writeGlobalState(t, globalDir, []migrationInstance{
		{Title: "sess", Worktree: migrationWorktreeData{RepoPath: repoA, WorktreePath: oldPath}},
	})
	reg := &config.WorkspaceRegistry{Workspaces: []config.Workspace{{Name: "alpha", Path: repoA}}}

	plan, err := planMigration(globalDir, reg)
	assert.NoError(t, err)
	assert.NotNil(t, plan)

	expectedNew := filepath.Join(config.WorkspaceConfigDir(&reg.Workspaces[0]), "worktrees", "session-xyz")
	assert.Equal(t, expectedNew, plan.byWorkspace["alpha"][0].Worktree.WorktreePath)
	assert.Len(t, plan.moves, 1)
	assert.Equal(t, oldPath, plan.moves[0].from)
	assert.Equal(t, expectedNew, plan.moves[0].to)
}

func TestPrintPlan_Output(t *testing.T) {
	plan := &workspaceMigration{
		byWorkspace: map[string][]migrationInstance{
			"alpha": {{Title: "a1"}},
		},
		moves: []migrationMove{{instanceTitle: "a1", from: "/old", to: "/new"}},
	}
	var buf bytes.Buffer
	plan.printPlan(&buf)
	out := buf.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "a1")
	assert.Contains(t, out, "/old -> /new")
}

func TestApply_BackupCreatesBak(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, config.StateFileName)
	assert.NoError(t, os.WriteFile(stateFile, []byte(`{"help_screens_seen":0,"instances":[]}`), 0644))

	assert.NoError(t, backupStateIfRequested(dir, true))
	bak, err := os.ReadFile(stateFile + ".bak")
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(bak), "help_screens_seen"))
}

func TestApply_MissingStateFileNoBackup(t *testing.T) {
	dir := t.TempDir()
	// No state.json in dir.
	assert.NoError(t, backupStateIfRequested(dir, true))
	_, err := os.Stat(filepath.Join(dir, config.StateFileName+".bak"))
	assert.True(t, os.IsNotExist(err))
}
