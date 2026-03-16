package cmd

import (
	"claude-squad/config"
	"claude-squad/session/git"
	"fmt"
	"os"
	"path/filepath"
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

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceName, "name", "", "Override workspace name (defaults to directory basename)")
	WorkspaceCmd.AddCommand(workspaceAddCmd)
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceRemoveCmd)
}
