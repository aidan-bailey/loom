package cli

import (
	"fmt"
	"io"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)

// resetReq is the ID of the one request Reset makes, which its Reply
// names.
const resetReq core.ReqID = 1

// Reset has the daemon reset the workspace registered as name ("" the
// global one), as `loom reset --force` does while a daemon serves the
// global dir, waits for the reset to finish (its Reply) and says so on
// out. The daemon
// drops the workspace's sessions at once, then deletes their records, kills
// the tmux sessions started in its roots and removes its worktrees and
// their branches; it refuses while one of them is busy, and for a
// workspace whose sessions failed to load.
func Reset(c *cobra.Command, out io.Writer, name string) error {
	return withDaemon(c, func(d *client) error {
		if name != "" {
			if err := d.ReloadRegistry(); err != nil {
				return err
			}
		}
		v, ok := d.served(name)
		if !ok {
			return fmt.Errorf("workspace %q not found", name)
		}
		if err := d.Reset(v.ID, resetReq); err != nil {
			return err
		}
		reply, err := d.awaitReply(resetReq)
		switch {
		case err != nil:
			return err
		case reply.Err != nil:
			return reply.Err
		}
		fmt.Fprintf(out, "The loom daemon reset %s: its instances, tmux sessions (those started in its repo or worktrees directory), worktrees and branches are gone\n", v.Label)
		return nil
	})
}
