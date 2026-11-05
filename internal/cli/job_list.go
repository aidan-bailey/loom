package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
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
	if q.Learning != "" {
		fmt.Fprintf(w, "learning off: %s\n", q.Learning)
	} else if q.Agent.Why != "" && slices.ContainsFunc(q.Jobs, func(j core.JobView) bool { return j.State == sched.Queued }) {
		fmt.Fprintf(w, "agent off: %s\n", q.Agent.Why)
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
	fmt.Fprintln(tw, "ID\tSTATE\tDESCRIPTION\tOWNER\tCLAIM\tTIME\tUSUAL\tWAITS FOR")
	for _, j := range rows {
		waits := ""
		if j.State == sched.Queued {
			waits = j.Waiting
		}
		if j.Unsized != "" {
			waits = strings.TrimPrefix(waits+"; not sized: "+j.Unsized, "; ")
		}
		if j.State == sched.Queued && !j.StartsAt.IsZero() {
			waits = startsText(j.StartsAt, now) + " · " + waits
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, stateText(j), oneLine(j.Description), oneLine(ownerText(j)), claimText(j), rounded(elapsed(j, now)), usualText(j, now), oneLine(waits))
	}
	return tw.Flush()
}

// usualText is a job's usual duration as `list` shows it: "~2m", "~2m
// overdue" for a running job past its mark, "-" for none.
func usualText(j core.JobView, now time.Time) string {
	if j.Usual <= 0 {
		return "-"
	}
	text := "~" + sched.RoughDuration(j.Usual)
	if j.State == sched.Running && j.OverdueAfter > 0 && now.Sub(j.Started) > j.OverdueAfter {
		text += " overdue"
	}
	return text
}

// startsText is a queued job's forecast start: "starts ~3m", or "starts
// now" when it is due.
func startsText(at, now time.Time) string {
	if !at.After(now) {
		return "starts now"
	}
	return "starts ~" + sched.RoughDuration(at.Sub(now))
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
