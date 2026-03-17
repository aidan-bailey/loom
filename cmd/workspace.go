package cmd

import (
	"claude-squad/config"
	"claude-squad/session/git"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var workspaceName string

// WorkspaceCmd is the parent command for workspace management.
var WorkspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Register a git repository as a workspace",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		if !git.IsGitRepo(absPath) {
			return fmt.Errorf("%s is not a git repository", absPath)
		}

		name := workspaceName
		if name == "" {
			name = filepath.Base(absPath)
		}

		reg, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return err
		}

		if err := reg.Add(name, absPath); err != nil {
			return err
		}

		fmt.Printf("Workspace %q added at %s\n", name, absPath)
		return nil
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return err
		}

		if len(reg.Workspaces) == 0 {
			fmt.Println("No workspaces registered. Use 'claude-squad workspace add' to register one.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPATH\tSTATUS")
		for _, ws := range reg.Workspaces {
			status := ""
			if ws.Name == reg.LastUsed {
				status = "[last used]"
			}
			if _, err := os.Stat(ws.Path); os.IsNotExist(err) {
				status = "[missing]"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", ws.Name, ws.Path, status)
		}
		return w.Flush()
	},
}

var workspaceRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Unregister a workspace (does not delete data)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return err
		}

		if err := reg.Remove(args[0]); err != nil {
			return err
		}

		fmt.Printf("Workspace %q removed\n", args[0])
		return nil
	},
}

// migrateInstanceData is a minimal struct for reading instance JSON during migration.
type migrateInstanceData struct {
	Title    string `json:"title"`
	Worktree struct {
		RepoPath     string `json:"repo_path"`
		WorktreePath string `json:"worktree_path"`
	} `json:"worktree"`
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

		// Load global state
		globalState := config.LoadStateFrom(globalDir)
		if globalState.InstancesData == nil || string(globalState.InstancesData) == "[]" {
			fmt.Println("No instances to migrate.")
			return nil
		}

		var instances []migrateInstanceData
		if err := json.Unmarshal(globalState.InstancesData, &instances); err != nil {
			return fmt.Errorf("failed to parse global instances: %w", err)
		}

		// Group instances by workspace
		type matchedInstance struct {
			raw json.RawMessage
			ws  *config.Workspace
		}

		// Re-parse as raw messages to preserve full data
		var rawInstances []json.RawMessage
		if err := json.Unmarshal(globalState.InstancesData, &rawInstances); err != nil {
			return fmt.Errorf("failed to parse global instances: %w", err)
		}

		var matched []matchedInstance
		var unmatched []json.RawMessage

		for i, inst := range instances {
			ws := reg.FindByPath(inst.Worktree.RepoPath)
			if ws != nil {
				matched = append(matched, matchedInstance{raw: rawInstances[i], ws: ws})
			} else {
				unmatched = append(unmatched, rawInstances[i])
			}
		}

		if len(matched) == 0 {
			fmt.Println("No instances matched any registered workspace.")
			return nil
		}

		// Group matched by workspace
		byWorkspace := make(map[string][]json.RawMessage)
		for _, m := range matched {
			byWorkspace[m.ws.Name] = append(byWorkspace[m.ws.Name], m.raw)
		}

		// Migrate each group
		for wsName, rawInsts := range byWorkspace {
			ws := reg.Get(wsName)
			if ws == nil {
				continue
			}
			wsDir := config.WorkspaceConfigDir(ws)

			// Load existing workspace state (or create new)
			wsState := config.LoadStateFrom(wsDir)

			// Parse existing workspace instances
			var existingRaw []json.RawMessage
			if wsState.InstancesData != nil && string(wsState.InstancesData) != "[]" {
				_ = json.Unmarshal(wsState.InstancesData, &existingRaw)
			}

			// Parse existing titles to skip duplicates
			var existingInstances []migrateInstanceData
			_ = json.Unmarshal(wsState.InstancesData, &existingInstances)
			existingTitles := make(map[string]bool)
			for _, ei := range existingInstances {
				existingTitles[ei.Title] = true
			}

			added := 0
			for j, raw := range rawInsts {
				if !existingTitles[instances[j].Title] {
					// Update worktree path if it's under the global dir
					var instMap map[string]interface{}
					if err := json.Unmarshal(raw, &instMap); err == nil {
						if wt, ok := instMap["worktree"].(map[string]interface{}); ok {
							if wtPath, ok := wt["worktree_path"].(string); ok && strings.HasPrefix(wtPath, globalDir) {
								newWtDir := filepath.Join(wsDir, "worktrees")
								_ = os.MkdirAll(newWtDir, 0755)
								newPath := filepath.Join(newWtDir, filepath.Base(wtPath))
								// Move the worktree directory if it exists
								if _, err := os.Stat(wtPath); err == nil {
									_ = os.Rename(wtPath, newPath)
								}
								wt["worktree_path"] = newPath
								updated, _ := json.Marshal(instMap)
								raw = updated
							}
						}
					}
					existingRaw = append(existingRaw, raw)
					added++
				}
			}

			if added > 0 {
				mergedData, err := json.Marshal(existingRaw)
				if err != nil {
					return fmt.Errorf("failed to marshal instances for workspace %s: %w", wsName, err)
				}
				wsState.InstancesData = mergedData
				if err := config.SaveStateTo(wsState, wsDir); err != nil {
					return fmt.Errorf("failed to save state for workspace %s: %w", wsName, err)
				}
				fmt.Printf("Migrated %d instance(s) to workspace %q\n", added, wsName)
			}
		}

		// Update global state with unmatched instances only
		unmatchedData, err := json.Marshal(unmatched)
		if err != nil {
			return fmt.Errorf("failed to marshal remaining instances: %w", err)
		}
		globalState.InstancesData = unmatchedData
		if err := config.SaveStateTo(globalState, globalDir); err != nil {
			return fmt.Errorf("failed to save global state: %w", err)
		}

		fmt.Printf("Migration complete. %d instance(s) remain in global storage.\n", len(unmatched))
		return nil
	},
}

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceName, "name", "", "Override workspace name (defaults to directory basename)")
	WorkspaceCmd.AddCommand(workspaceAddCmd)
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceRemoveCmd)
	WorkspaceCmd.AddCommand(workspaceMigrateCmd)
}
