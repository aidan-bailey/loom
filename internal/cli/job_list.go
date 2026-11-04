package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/spf13/cobra"
)

func newJobListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the running and queued jobs, and why each queued one waits",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				return writeJobList(c.OutOrStdout(), d.Queue(), all, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include the finished jobs the daemon still keeps")
	return cmd
}

// writeJobList prints q's jobs as a table (the running, the queued, and with
// all the finished ones kept), led by the reason when admission is paused.
func writeJobList(w io.Writer, q core.QueueView, all bool, now time.Time) error {
	if q.Paused != "" {
		fmt.Fprintf(w, "admission paused: %s\n", q.Paused)
	}
	var rows []core.JobView
	finished := 0
	for _, j := range q.Jobs {
		switch {
		case !j.State.Finished() || all:
			rows = append(rows, j)
		default:
			finished++
		}
	}
	if len(rows) == 0 {
		msg := "No jobs."
		if !all {
			msg = "No running or queued jobs."
			if finished > 0 {
				msg += fmt.Sprintf(" %d finished (loom job list --all shows them).", finished)
			}
		}
		_, err := fmt.Fprintln(w, msg)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tDESCRIPTION\tOWNER\tCLAIM\tTIME\tWAITS FOR")
	for _, j := range rows {
		waits := ""
		if j.State == sched.Queued {
			waits = j.Waiting
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, stateText(j), oneLine(j.Description), oneLine(ownerText(j)), j.Claim.String(), rounded(elapsed(j, now)), oneLine(waits))
	}
	return tw.Flush()
}

func ownerText(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "-"
	case j.OwnerTitle == "":
		return fmt.Sprintf("#%d", j.Owner)
	}
	return j.OwnerTitle
}

// elapsed is how long a job has waited (queued), has run (running), or ran
// (finished; a job that never started, how long it lived).
func elapsed(j core.JobView, now time.Time) time.Duration {
	switch {
	case j.State == sched.Queued:
		return now.Sub(j.Submitted)
	case j.State == sched.Running:
		return now.Sub(j.Started)
	case !j.Started.IsZero():
		return j.Ended.Sub(j.Started)
	}
	return j.Ended.Sub(j.Submitted)
}
