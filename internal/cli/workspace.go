package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"

	"github.com/spf13/cobra"
)

var workspaceName string

// WorkspaceCmd is the parent command for workspace management. main
// attaches `migrate` (cmd.MigrateCmd) to it.
var WorkspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Register a git repository as a workspace",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		if !git.IsGitRepo(absPath, cmd.Exec{}) {
			return fmt.Errorf("%s is not a git repository", absPath)
		}

		name := workspaceName
		if name == "" {
			name = filepath.Base(absPath)
		}

		return withDaemon(c, func(d *client) error {
			if _, err := d.Register(name, absPath); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Workspace %q added at %s\n", name, absPath)
			return nil
		})
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered workspaces",
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			if err := d.ReloadRegistry(); err != nil {
				return err
			}
			reg := d.Registry()
			out := c.OutOrStdout()
			if len(reg.Workspaces) == 0 {
				fmt.Fprintln(out, "No workspaces registered. Use 'loom workspace add' to register one.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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
		})
	},
}

var workspaceRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Unregister a workspace (does not delete data; its sessions keep running)",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			if err := d.Unregister(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Workspace %q removed\n", args[0])
			return nil
		})
	},
}

var workspaceUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the default workspace for future invocations",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			if err := d.SetLastUsed(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Default workspace set to %q\n", args[0])
			return nil
		})
	},
}

var workspaceRenameCmd = &cobra.Command{
	Use:   "rename <old-name> <new-name>",
	Short: "Rename a workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			if err := d.RenameWorkspace(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Workspace %q renamed to %q\n", args[0], args[1])
			return nil
		})
	},
}

var workspaceStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show instance counts for a workspace",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			ws, v, err := d.resolveWorkspace(name)
			if err != nil {
				return err
			}
			// The daemon's sessions, as state.json keeps them: a
			// Recoverable entry is a worktree found on disk, never stored.
			var titles []string
			for _, iv := range d.Views(v.ID) {
				if iv.Status != session.Recoverable {
					titles = append(titles, iv.Title)
				}
			}
			out := c.OutOrStdout()
			if len(titles) == 0 {
				fmt.Fprintf(out, "Workspace %q (%s): 0 instances\n", ws.Name, ws.Path)
				return nil
			}
			fmt.Fprintf(out, "Workspace %q (%s): %d instance(s)\n", ws.Name, ws.Path, len(titles))
			for _, title := range titles {
				fmt.Fprintf(out, "  - %s\n", title)
			}
			return nil
		})
	},
}

// resolveWorkspace is the registered workspace a command acts on, name's
// or, with name "", the one whose repository holds the working directory,
// and the view the daemon serves it as. It rereads the registry first
// (ReloadRegistry): another process may have changed it.
func (c *client) resolveWorkspace(name string) (config.Workspace, core.WorkspaceView, error) {
	if err := c.ReloadRegistry(); err != nil {
		return config.Workspace{}, core.WorkspaceView{}, err
	}
	reg := &config.WorkspaceRegistry{Workspaces: c.Registry().Workspaces}
	var ws *config.Workspace
	if name != "" {
		if ws = reg.Get(name); ws == nil {
			return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("workspace %q not found", name)
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("failed to get working directory: %w", err)
		}
		if ws = reg.FindByPath(cwd); ws == nil {
			return config.Workspace{}, core.WorkspaceView{}, errors.New("no workspace matches current directory; specify a name")
		}
	}
	v, ok := c.served(ws.Name)
	if !ok {
		return config.Workspace{}, core.WorkspaceView{}, fmt.Errorf("workspace %q is not served by the loom daemon", ws.Name)
	}
	return *ws, v, nil
}

// served is the view of the workspace the daemon serves for the registered
// name ("" the global one): the one served by that name, else the one
// whose config dir is that registry entry's (the daemon serves a directory
// registered twice under the name it first served it by).
func (c *client) served(name string) (core.WorkspaceView, bool) {
	served := c.Workspaces()
	for _, v := range served {
		if v.Name == name {
			return v, true
		}
	}
	if name == "" {
		return core.WorkspaceView{}, false
	}
	reg := &config.WorkspaceRegistry{Workspaces: c.Registry().Workspaces}
	def := reg.Get(name)
	if def == nil {
		return core.WorkspaceView{}, false
	}
	dir := session.CanonicalPath(config.WorkspaceConfigDir(def))
	for _, v := range served {
		if v.ConfigDir != "" && session.CanonicalPath(v.ConfigDir) == dir {
			return v, true
		}
	}
	return core.WorkspaceView{}, false
}

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceName, "name", "", "Override workspace name (defaults to directory basename)")
	WorkspaceCmd.AddCommand(workspaceAddCmd)
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceRemoveCmd)
	WorkspaceCmd.AddCommand(workspaceUseCmd)
	WorkspaceCmd.AddCommand(workspaceRenameCmd)
	WorkspaceCmd.AddCommand(workspaceStatusCmd)
}
