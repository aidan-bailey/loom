package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/spf13/cobra"
)

// newJobBeliefsCmd is `loom job beliefs` (smart scheduler spec §7): what
// the scheduler has learned of each command, with `show` and `forget`.
func newJobBeliefsCmd() *cobra.Command {
	var repo string
	cmd := &cobra.Command{
		Use:   "beliefs",
		Short: "List what the scheduler has learned of each command",
		Long: "Lists the scheduler's beliefs: each command it has seen, with the claim a job of it gets now, " +
			"its usual duration and how many runs taught it, led by the agent's track record.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				page, err := d.readBeliefs(sched.BeliefQuery{Repo: repo})
				if err != nil {
					return err
				}
				writeBeliefs(c.OutOrStdout(), page, time.Now())
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "Only the beliefs of this repo, by its name")
	cmd.AddCommand(newJobBeliefsShowCmd(), newJobBeliefsForgetCmd())
	return cmd
}

func newJobBeliefsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Print one belief in full: its claim, durations, prior, notes and runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				page, err := d.readBeliefs(sched.BeliefQuery{ID: sched.SignatureID(args[0])})
				if err != nil {
					return err
				}
				// The query named one ID, so the page holds that belief alone.
				for _, line := range sched.FormatBelief(page.Beliefs[0]) {
					fmt.Fprintln(c.OutOrStdout(), line)
				}
				return nil
			})
		},
	}
}

func newJobBeliefsForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget ID",
		Short: "Drop a belief whose history no longer holds (a new toolchain, a rewritten build)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				id := sched.SignatureID(args[0])
				page, err := d.readBeliefs(sched.BeliefQuery{ID: id})
				if err != nil {
					return err
				}
				v := page.Beliefs[0]
				d.ForgetBelief(id, sessionReq)
				reply, err := d.awaitReply(sessionReq)
				switch {
				case err != nil:
					return err
				case reply.Err != nil:
					return reply.Err
				}
				fmt.Fprintf(c.OutOrStdout(), "forgot belief %s (%s · %s, %s)\n", id, v.RepoName,
					oneLine(strings.Join(v.Belief.Command, " ")), runsText(len(v.Belief.Runs)))
				return nil
			})
		},
	}
}

// readBeliefs makes the ReadBeliefs request and waits for its Reply.
func (d *client) readBeliefs(q sched.BeliefQuery) (sched.BeliefsPage, error) {
	d.ReadBeliefs(q, sessionReq)
	reply, err := d.awaitReply(sessionReq)
	switch {
	case err != nil:
		return sched.BeliefsPage{}, err
	case reply.Err != nil:
		return sched.BeliefsPage{}, reply.Err
	}
	return reply.Beliefs, nil
}

// writeBeliefs prints the agent's track record and a table of page's
// beliefs.
func writeBeliefs(w io.Writer, page sched.BeliefsPage, now time.Time) {
	t := page.Track
	fmt.Fprintf(w, "agent priors: %d scored · memory within 25%%: %d · under on memory: %d · duration ×%.1f\n",
		t.Scored, t.MemoryWithin, t.MemoryUnder, t.DurationMedian)
	if len(page.Beliefs) == 0 {
		fmt.Fprintln(w, "No beliefs yet.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO\tFOLDER\tCOMMAND\tRUNS\tCLAIM\tSCALES\tUSUAL\tLAST SEEN")
	for _, v := range page.Beliefs {
		usual := "-"
		if v.Usual > 0 {
			usual = "~" + sched.RoughDuration(v.Usual)
		}
		claim := claimText(core.JobView{Claim: v.Sized.Claim, Sources: v.Sized.Sources, Learned: v.Sized.Learned, FamilyRuns: v.Sized.FamilyRuns, AboveCap: v.Sized.AboveCap})
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", v.Belief.ID, v.RepoName, v.Belief.Folder,
			cutTo(oneLine(beliefCommand(v)), 40), len(v.Belief.Runs), claim, scalesText(v.Scaling), usual,
			sched.RoughDuration(now.Sub(v.Belief.LastSeen))+" ago")
	}
	_ = tw.Flush()
}

// scalesText is the SCALES column (job scaling spec §6): the parallel
// fraction as a percentage, "overruns", or "-" while it is unknown.
func scalesText(s sched.Scaling) string {
	switch {
	case s.Overruns:
		return "overruns"
	case s.Known:
		return fmt.Sprintf("%.0f%%", 100*s.Parallel)
	}
	return "-"
}

// cutTo cuts s to n characters, with an ellipsis.
func cutTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// runsText is "1 run" or "N runs".
func runsText(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// beliefCommand is a belief's command as `loom job beliefs` lists it, with
// the command it runs after (dependent jobs spec §5.6).
func beliefCommand(v sched.BeliefView) string {
	command := strings.Join(v.Belief.Command, " ")
	if v.After != nil {
		command += " (after " + strings.Join(v.After, " ") + ")"
	}
	return command
}
