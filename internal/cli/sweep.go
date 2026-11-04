package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/spf13/cobra"
)

// SweepCmd is `loom sweep`: the daemon's build sweep, run now.
var SweepCmd = newSweepCmd()

// newSweepCmd builds `loom sweep`, with flags of its own: each call is
// independent (tests build one per run).
func newSweepCmd() *cobra.Command {
	var (
		dryRun, verbose bool
		after           int
	)
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete idle and paused sessions' unused Rust binaries",
		Long: "Asks the daemon to run its build sweep now: in every session whose agent is idle or paused, " +
			"it deletes the executables in the worktree's cargo target dir unused for build_sweep_after_hours " +
			"(or --after). --dry-run lists them and deletes nothing.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			if after < 0 {
				return errors.New("--after takes a number of hours, 0 or more (0 uses build_sweep_after_hours)")
			}
			return withDaemon(c, func(d *client) error {
				d.SweepBuilds(buildsweep.Options{DryRun: dryRun, AfterHours: after}, sessionReq)
				reply, err := d.awaitReply(sessionReq)
				switch {
				case err != nil:
					return err
				case reply.Err != nil:
					return reply.Err
				}
				writeSweepReport(c.OutOrStdout(), reply.Sweep, verbose, time.Now())
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be deleted, and delete nothing")
	cmd.Flags().IntVar(&after, "after", 0, "Hours unused before a binary goes (default: build_sweep_after_hours)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "List every file")
	return cmd
}

// writeSweepReport prints a sweep: a heading, one row per session (its
// files and bytes, or why it was skipped) with, verbose, each file under
// it, then the total.
func writeSweepReport(w io.Writer, rep buildsweep.Report, verbose bool, now time.Time) {
	if rep.DryRun {
		fmt.Fprintf(w, "Dry run, nothing deleted: binaries unused for %dh\n", rep.AfterHours)
	} else {
		fmt.Fprintf(w, "Deleted binaries unused for %dh\n", rep.AfterHours)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var files int
	var freed int64
	for _, r := range rep.Results {
		if r.Skip != "" {
			fmt.Fprintf(tw, "%s\t%s\t%s\tskipped (%s): %s\n", r.Workspace, r.Title, r.Status, r.Skip, r.Detail)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s, %s\n", r.Workspace, r.Title, r.Status, fileCount(len(r.Files)), formatSize(r.Freed))
		}
		for _, e := range r.Errors {
			fmt.Fprintf(tw, "\t\t\terror: %s\n", e)
		}
		if verbose {
			for _, f := range r.Files {
				fmt.Fprintf(tw, "\t\t\t  %s  %s  unused %dh\n", f.Path, formatSize(f.Size), int(now.Sub(f.LastUsed).Hours()))
			}
		}
		files += len(r.Files)
		freed += r.Freed
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "Total: %s, %s\n", fileCount(files), formatSize(freed))
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// formatSize is n bytes in binary units, to one decimal: 0B, 1.5K, 3.0G.
func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
