package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/spf13/cobra"
)

// newJobAgentCmd is `loom job agent` (smart scheduler spec §16): the
// scheduler's agent, its state, today's use and its recent calls.
func newJobAgentCmd() *cobra.Command {
	var (
		calls  int
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Show the scheduler's agent: its state, today's use and its recent calls",
		Long: "Shows the agent that sizes commands the scheduler has never measured: whether it is on, the call it is " +
			"making, today's calls and cost, and its recent calls with what each took and answered. The calls are kept " +
			"in ~/.loom/jobs/advisor.jsonl.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				page, err := d.readAgent(calls)
				if err != nil {
					return err
				}
				out := c.OutOrStdout()
				if asJSON {
					data, err := json.MarshalIndent(page, "", "  ")
					if err != nil {
						return fmt.Errorf("encode the agent's page: %w", err)
					}
					_, err = fmt.Fprintln(out, string(data))
					return err
				}
				writeAgent(out, page, time.Now())
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&calls, "calls", 20, "How many recent calls to list (at most 500)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the state and calls as JSON")
	return cmd
}

// readAgent makes the ReadAgent request and waits for its Reply.
func (d *client) readAgent(limit int) (core.AgentPage, error) {
	d.ReadAgent(limit, sessionReq)
	reply, err := d.awaitReply(sessionReq)
	switch {
	case err != nil:
		return core.AgentPage{}, err
	case reply.Err != nil:
		return core.AgentPage{}, reply.Err
	}
	return reply.Agent, nil
}

// writeAgent prints the agent's state and a table of its recent calls.
func writeAgent(w io.Writer, page core.AgentPage, now time.Time) {
	s := page.State
	if s.On {
		fmt.Fprintf(w, "agent: on · %s, %s · a job waits up to %s for it\n", s.Model, s.Effort, sched.RoughDuration(s.Wait))
	} else {
		fmt.Fprintf(w, "agent: off · %s\n", s.Why)
	}
	if f := s.Flight; f.Kind != "" {
		fmt.Fprintf(w, "in flight: %s for job %d (%s), %s\n", f.Kind, f.Job, f.Command, sched.RoughDuration(now.Sub(f.Started)))
	}
	if s.Pending > 0 {
		fmt.Fprintf(w, "waiting: %s\n", plural(s.Pending, "call"))
	}
	if s.Failures > 0 {
		fmt.Fprintf(w, "failures in a row: %d\n", s.Failures)
	}
	if !s.PausedUntil.IsZero() {
		fmt.Fprintf(w, "paused until %s\n", s.PausedUntil.Local().Format("15:04"))
	}
	fmt.Fprintf(w, "today: %s\n", todayText(s.Today))
	if page.LogErr != "" {
		fmt.Fprintf(w, "log: kept in memory only (%s)\n", page.LogErr)
	} else {
		fmt.Fprintf(w, "log: %s, %s\n", page.Path, plural(page.Kept, "call"))
	}
	fmt.Fprintln(w)
	if len(page.Calls) == 0 {
		fmt.Fprintln(w, "No calls yet.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tKIND\tJOB\tCOMMAND\tMODEL\tTIME\tTOKENS\tCOST\tOUTCOME")
	for _, c := range page.Calls {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", whenText(c.At, now), c.Kind, c.Job,
			cutTo(oneLine(c.Command), 30), c.Model+"/"+c.Effort, callTime(c), callTokens(c.Usage), callCost(c.Usage), cutTo(oneLine(outcomeText(c)), 90))
	}
	_ = tw.Flush()
}

// todayText is today's calls: "no calls", or "5 calls (1 failed), $0.06".
func todayText(d core.AgentDay) string {
	if d.Calls == 0 {
		return "no calls"
	}
	text := plural(d.Calls, "call")
	if d.Failed > 0 {
		text += fmt.Sprintf(" (%d failed)", d.Failed)
	}
	return text + fmt.Sprintf(", $%.2f", d.CostUSD)
}

// whenText is a call's time: "15:04" today, "01-02 15:04" before.
func whenText(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return at.Format("15:04")
	}
	return at.Format("01-02 15:04")
}

func callTime(c advisor.CallRecord) string {
	if c.Usage.DurationMS <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fs", float64(c.Usage.DurationMS)/1000)
}

// callTokens is "in/out": every input token, cached or not, then output.
func callTokens(u advisor.Usage) string {
	in := u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
	if in == 0 && u.OutputTokens == 0 {
		return "-"
	}
	return compactCount(in) + "/" + compactCount(u.OutputTokens)
}

func compactCount(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func callCost(u advisor.Usage) string {
	if u.CostUSD <= 0 {
		return "-"
	}
	return fmt.Sprintf("$%.3f", u.CostUSD)
}

// outcomeText says what a call came to: a prior's estimate and note, a
// reconcile's note, or why it failed or was dropped.
func outcomeText(c advisor.CallRecord) string {
	switch c.Outcome {
	case advisor.OutcomeFailed:
		return "failed: " + c.Error
	case advisor.OutcomeSkipped:
		return "skipped: " + c.Error
	}
	if c.Kind == advisor.KindReconcile {
		var a struct {
			Note string `json:"note"`
		}
		_ = json.Unmarshal([]byte(c.Answer), &a)
		return "note: " + a.Note
	}
	var a advisor.PriorAnswer
	if json.Unmarshal([]byte(c.Answer), &a) != nil {
		return c.Answer
	}
	claim := sched.Claim{CPUs: a.CPUs, MemoryGB: a.MemoryGB, IO: a.IO}.String()
	text := fmt.Sprintf("%s ~%s", claim, sched.RoughDuration(time.Duration(a.DurationSeconds)*time.Second))
	if a.Like != "" {
		text += " like " + string(a.Like)
	}
	if note := strings.TrimSpace(a.Note); note != "" {
		text += ": " + note
	}
	return text
}

// plural is "1 call" or "N calls".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
