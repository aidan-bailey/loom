package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/watch"
	"github.com/spf13/cobra"
)

// watchReq is the ID of the one request status and dry-run make, which its
// Reply names.
const watchReq core.ReqID = 1

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
	cmd.AddCommand(newWatchSwitchCmd(&workspace, true), newWatchSwitchCmd(&workspace, false),
		newWatchReadCmd(&workspace, false), newWatchReadCmd(&workspace, true))
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

// newWatchReadCmd is `status` (dryRun false) or `dry-run`: ReadWatch for the
// workspace, printed from the Reply's view. `status` shows the last tick's
// decisions and the watcher's faults; `dry-run` the actions the next tick
// would take, which the daemon works out and takes none of.
func newWatchReadCmd(workspace *string, dryRun bool) *cobra.Command {
	var asJSON bool
	use, short := "status", "Show each issue's state, the watcher's last decisions, its counts, why issues wait and any fault"
	if dryRun {
		use, short = "dry-run", "Show the actions the watcher's next tick would take, and take none"
	}
	cmd := &cobra.Command{
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
				d.ReadWatch(v.ID, dryRun, watchReq)
				r, err := d.awaitReply(watchReq)
				switch {
				case err != nil:
					return err
				case r.Err != nil:
					return r.Err
				}
				switch {
				case asJSON:
					return writeWatchJSON(c.OutOrStdout(), r.Watch)
				case dryRun:
					return writeWatchDryRun(c.OutOrStdout(), v.Label, r.Watch)
				}
				return writeWatchStatus(c.OutOrStdout(), v.Label, r.Watch)
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the watcher's view as JSON")
	return cmd
}

// writeWatchJSON prints the view as the wire has it (WatchView's own keys).
func writeWatchJSON(w io.Writer, v core.WatchView) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeWatchStatus prints the watcher's state for label: its switch, fault,
// viewer and counts, then one row per issue (its state, session, PR, nudge
// counts, whether it is escalated, why it waits, the text of its failed or
// refused action, and the actions the last tick decided for it) and the
// escalations. A wait (ActWait) is the WAITING
// column, never a decision.
func writeWatchStatus(w io.Writer, label string, v core.WatchView) error {
	state := "off"
	if v.On {
		state = "on"
	}
	fmt.Fprintf(w, "Watch %s for %s\n", state, label)
	if v.Err != "" {
		fmt.Fprintf(w, "Error: %s\n", v.Err)
	}
	if v.Viewer != "" {
		fmt.Fprintf(w, "Viewer: %s\n", v.Viewer)
	}
	if v.Considered > 0 || v.Unconsidered > 0 {
		fmt.Fprintf(w, "Issues considered: %d", v.Considered)
		if v.Unconsidered > 0 {
			fmt.Fprintf(w, " (at least %d not considered)", v.Unconsidered)
		}
		fmt.Fprintln(w)
	}
	if len(v.Issues) == 0 {
		fmt.Fprintln(w, "No issues.")
	} else {
		decided := map[int][]string{}
		for _, a := range v.Actions {
			if a.Kind != watch.ActWait {
				decided[a.Issue] = append(decided[a.Issue], describeWatchAction(a))
			}
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ISSUE\tSTATE\tSESSION\tPR\tNUDGES\tESCALATED\tWAITING\tFAILED\tDECISION")
		for _, is := range v.Issues {
			pr, escalated := "-", "-"
			if is.PR != 0 {
				pr = fmt.Sprintf("#%d", is.PR)
			}
			if is.Escalated {
				escalated = "yes"
			}
			fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", is.Number, orDash(is.State), orDash(is.Session),
				pr, watchNudges(is.Nudges), escalated, orDash(is.Waiting), orDash(is.Failed),
				orDash(strings.Join(decided[is.Number], "; ")))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(v.Escalations) > 0 {
		fmt.Fprintln(w, "Escalations:")
		for _, e := range v.Escalations {
			fmt.Fprintf(w, "  #%d  %s  %s\n", e.Issue, e.At.UTC().Format(time.RFC3339), e.Reason)
		}
	}
	return nil
}

// writeWatchDryRun prints what the next tick would do for label: its actions
// (the ones that act), then the issues that would wait, with why, then the
// issues whose last action failed, with its text. The view is a dry run's,
// so nothing was done.
func writeWatchDryRun(w io.Writer, label string, v core.WatchView) error {
	fmt.Fprintf(w, "Dry run for %s: nothing was done.\n", label)
	if !v.On {
		fmt.Fprintln(w, "Watch is off: the next tick would take no action.")
		return nil
	}
	if v.Err != "" {
		fmt.Fprintf(w, "Error: %s\n", v.Err)
	}
	var acts, waits []watch.Action
	for _, a := range v.Actions {
		if a.Kind == watch.ActWait {
			waits = append(waits, a)
		} else {
			acts = append(acts, a)
		}
	}
	if len(acts) == 0 {
		fmt.Fprintln(w, "The next tick would take no action.")
	} else {
		fmt.Fprintln(w, "The next tick would:")
		for _, a := range acts {
			fmt.Fprintf(w, "  #%d  %s\n", a.Issue, describeWatchAction(a))
		}
	}
	if len(waits) > 0 {
		fmt.Fprintln(w, "Waiting:")
		for _, a := range waits {
			fmt.Fprintf(w, "  #%d  %s\n", a.Issue, a.Reason)
		}
	}
	var failed []core.WatchIssueView
	for _, is := range v.Issues {
		if is.Failed != "" {
			failed = append(failed, is)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintln(w, "Failed:")
		for _, is := range failed {
			fmt.Fprintf(w, "  #%d  %s\n", is.Number, is.Failed)
		}
	}
	return nil
}

// describeWatchAction is one line for an action, without its issue: what
// Decide chose, as the fields of its kind give it.
func describeWatchAction(a watch.Action) string {
	switch a.Kind {
	case watch.ActStart:
		if a.Resume {
			return fmt.Sprintf("start (resume PR #%d on %s)", a.PR, a.Branch)
		}
		return "start"
	case watch.ActKill:
		return fmt.Sprintf("kill session %d", a.Session)
	case watch.ActClosePR:
		return fmt.Sprintf("close PR #%d: %s", a.PR, a.Reason)
	case watch.ActSend:
		return fmt.Sprintf("nudge %s (PR #%d, nudge %d)", a.Nudge.Kind, a.PR, a.N)
	case watch.ActEscalate:
		return "escalate: " + a.Reason
	case watch.ActWait:
		return "wait: " + a.Reason
	}
	return string(a.Kind)
}

// watchNudges is a PR's nudge counts as "kind=n" pairs in kind order, "-" for
// none.
func watchNudges(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	pairs := make([]string, len(kinds))
	for i, k := range kinds {
		pairs[i] = fmt.Sprintf("%s=%d", k, counts[k])
	}
	return strings.Join(pairs, ",")
}
