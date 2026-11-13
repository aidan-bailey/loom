package cli

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)

// WatchCmd is `loom watch`: the issue watcher's switch and its readouts, each
// a request to the daemon for one workspace (the issue watcher spec §1). main
// attaches it.
var WatchCmd = newWatchCmd()

func newWatchCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Switch a workspace's issue watcher on or off, and see what it decides",
	}
	cmd.PersistentFlags().StringVarP(&workspace, "workspace", "w", "",
		"Registered workspace (default: the one whose repository holds the current directory)")
	cmd.AddCommand(newWatchSwitchCmd(&workspace, true), newWatchSwitchCmd(&workspace, false))
	return cmd
}

// newWatchSwitchCmd is `on` (on true) or `off`: SetWatch for the workspace,
// which the daemon stores in the workspace's local config. Switching off
// stops the watcher; the sessions it started keep running untouched.
func newWatchSwitchCmd(workspace *string, on bool) *cobra.Command {
	use, short := "off", "Switch the workspace's issue watcher off; its sessions keep running"
	if on {
		use, short = "on", "Switch the workspace's issue watcher on"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				if err := d.request(func(req core.ReqID) { d.SetWatch(v.ID, on, req) }); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "watch %s: %s\n", use, v.Label)
				return nil
			})
		},
	}
}
