package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	migrateDryRun bool
	migrateBackup bool
)

// migrationInstance mirrors session.InstanceData's JSON shape with
// typed fields. It is duplicated here (rather than imported) because
// session -> cmd is already an edge in the import graph via cmd.Executor,
// and a back-edge would create a cycle. The two types round-trip
// through JSON; the test TestMigrationInstance_MirrorsInstanceData
// guards against drift.
type migrationInstance struct {
	SchemaVersion int `json:"schema_version,omitempty"`

	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	Status    int       `json:"status"`
	Height    int       `json:"height"`
	Width     int       `json:"width"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	AutoYes   bool      `json:"auto_yes"`

	Program             string                `json:"program"`
	Worktree            migrationWorktreeData `json:"worktree"`
	DiffStats           migrationDiffStats    `json:"diff_stats"`
	IsWorkspaceTerminal bool                  `json:"is_workspace_terminal"`
}

type migrationWorktreeData struct {
	RepoPath         string `json:"repo_path"`
	WorktreePath     string `json:"worktree_path"`
	SessionName      string `json:"session_name"`
	BranchName       string `json:"branch_name"`
	BaseCommitSHA    string `json:"base_commit_sha"`
	IsExistingBranch bool   `json:"is_existing_branch"`
}

type migrationDiffStats struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Content string `json:"content"`
}

// migrationMove is a single planned worktree directory relocation.
type migrationMove struct {
	instanceTitle string
	from, to      string
}

// workspaceMigration is the computed plan for one migrate invocation.
// Kept separate from its execution so --dry-run can print the plan and
// bail before touching the filesystem.
type workspaceMigration struct {
	globalDir     string
	moves         []migrationMove
	byWorkspace   map[string][]migrationInstance
	unmatchedData []migrationInstance
}

// planMigration reads global state, identifies migrate-able instances,
// rewrites their worktree paths for the destination workspace, and
// returns a plan (no side effects on disk).
func planMigration(globalDir string, reg *config.WorkspaceRegistry) (*workspaceMigration, error) {
	globalState := config.LoadStateFrom(globalDir)
	if globalState.InstancesData == nil || string(globalState.InstancesData) == "[]" {
		return nil, nil
	}

	var instances []migrationInstance
	if err := json.Unmarshal(globalState.InstancesData, &instances); err != nil {
		return nil, fmt.Errorf("parse global instances: %w", err)
	}

	plan := &workspaceMigration{
		globalDir:   globalDir,
		byWorkspace: map[string][]migrationInstance{},
	}

	for _, inst := range instances {
		ws := reg.FindByPath(inst.Worktree.RepoPath)
		if ws == nil {
			plan.unmatchedData = append(plan.unmatchedData, inst)
			continue
		}
		wsDir := config.WorkspaceConfigDir(ws)
		if inst.Worktree.WorktreePath != "" && strings.HasPrefix(inst.Worktree.WorktreePath, globalDir) {
			newDir := filepath.Join(wsDir, "worktrees")
			newPath := filepath.Join(newDir, filepath.Base(inst.Worktree.WorktreePath))
			plan.moves = append(plan.moves, migrationMove{
				instanceTitle: inst.Title,
				from:          inst.Worktree.WorktreePath,
				to:            newPath,
			})
			inst.Worktree.WorktreePath = newPath
		}
		plan.byWorkspace[ws.Name] = append(plan.byWorkspace[ws.Name], inst)
	}
	return plan, nil
}

// printPlan writes a human-readable summary of the migration to w.
func (m *workspaceMigration) printPlan(w io.Writer) {
	if m == nil {
		fmt.Fprintln(w, "No instances to migrate.")
		return
	}
	if len(m.byWorkspace) == 0 {
		fmt.Fprintln(w, "No instances matched any registered workspace.")
		return
	}
	total := 0
	for ws, items := range m.byWorkspace {
		fmt.Fprintf(w, "Workspace %q: %d instance(s)\n", ws, len(items))
		for _, it := range items {
			fmt.Fprintf(w, "  - %s\n", it.Title)
		}
		total += len(items)
	}
	if len(m.moves) > 0 {
		fmt.Fprintln(w, "Worktree relocations:")
		for _, mv := range m.moves {
			fmt.Fprintf(w, "  %s: %s -> %s\n", mv.instanceTitle, mv.from, mv.to)
		}
	}
	fmt.Fprintf(w, "Total: %d instance(s) to migrate, %d remaining in global storage.\n", total, len(m.unmatchedData))
}

// apply executes the plan: performs filesystem moves, rewrites workspace
// state files, and rewrites the global state file. With backup=true,
// every state file is copied to <file>.bak before being overwritten.
func (m *workspaceMigration) apply(reg *config.WorkspaceRegistry, backup bool) (int, error) {
	if m == nil {
		return 0, nil
	}

	for _, mv := range m.moves {
		if _, err := os.Stat(mv.from); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("stat %s: %w", mv.from, err)
		}
		if err := os.MkdirAll(filepath.Dir(mv.to), 0755); err != nil {
			return 0, fmt.Errorf("mkdir for %s: %w", mv.to, err)
		}
		if err := os.Rename(mv.from, mv.to); err != nil {
			return 0, fmt.Errorf("move %s -> %s: %w", mv.from, mv.to, err)
		}
	}

	migrated := 0
	for wsName, items := range m.byWorkspace {
		ws := reg.Get(wsName)
		if ws == nil {
			continue
		}
		wsDir := config.WorkspaceConfigDir(ws)
		if err := backupStateIfRequested(wsDir, backup); err != nil {
			return migrated, err
		}
		wsState := config.LoadStateFrom(wsDir)

		raws := make([]json.RawMessage, 0, len(items))
		for _, it := range items {
			r, err := json.Marshal(it)
			if err != nil {
				return migrated, fmt.Errorf("marshal %q: %w", it.Title, err)
			}
			raws = append(raws, r)
		}
		mergedData, added, err := mergeWorkspaceInstances(wsState.InstancesData, raws)
		if err != nil {
			return migrated, fmt.Errorf("workspace %s: %w", wsName, err)
		}
		if added > 0 {
			wsState.InstancesData = mergedData
			if err := config.SaveStateTo(wsState, wsDir); err != nil {
				return migrated, fmt.Errorf("save state for %s: %w", wsName, err)
			}
			migrated += added
		}
	}

	// Rewrite global state with only the unmatched entries.
	if err := backupStateIfRequested(m.globalDir, backup); err != nil {
		return migrated, err
	}
	globalState := config.LoadStateFrom(m.globalDir)
	unmatchedData, err := json.Marshal(m.unmatchedData)
	if err != nil {
		return migrated, fmt.Errorf("marshal unmatched set: %w", err)
	}
	globalState.InstancesData = unmatchedData
	if err := config.SaveStateTo(globalState, m.globalDir); err != nil {
		return migrated, fmt.Errorf("save global state: %w", err)
	}
	return migrated, nil
}

// backupStateIfRequested copies <dir>/state.json to <dir>/state.json.bak
// via AtomicWriteFile when backup is true. No-op when the source file
// does not yet exist (fresh workspace dir).
func backupStateIfRequested(dir string, backup bool) error {
	if !backup {
		return nil
	}
	src := filepath.Join(dir, config.StateFileName)
	raw, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", src, err)
	}
	dst := src + ".bak"
	if err := config.AtomicWriteFile(dst, raw, 0644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

var workspaceMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move global instances to their matching workspace directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		globalDir, err := config.GetGlobalConfigDir()
		if err != nil {
			return err
		}

		reg, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return err
		}
		if len(reg.Workspaces) == 0 {
			return fmt.Errorf("no workspaces registered — register workspaces first with 'workspace add'")
		}

		plan, err := planMigration(globalDir, reg)
		if err != nil {
			return err
		}
		if plan == nil {
			fmt.Println("No instances to migrate.")
			return nil
		}

		if migrateDryRun {
			fmt.Println("Dry run — no files will be modified.")
			plan.printPlan(os.Stdout)
			return nil
		}

		plan.printPlan(os.Stdout)
		migrated, err := plan.apply(reg, migrateBackup)
		if err != nil {
			return fmt.Errorf("migration failed after migrating %d instance(s): %w", migrated, err)
		}
		fmt.Printf("Migration complete. %d instance(s) migrated, %d remain in global storage.\n", migrated, len(plan.unmatchedData))
		return nil
	},
}

func init() {
	workspaceMigrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Print migration plan without modifying files")
	workspaceMigrateCmd.Flags().BoolVar(&migrateBackup, "backup", true, "Copy state.json files to .bak before overwriting")
}
