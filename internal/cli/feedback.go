package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)

// FeedbackCmd is `loom feedback`: the inbox loom's session agents write to
// (session feedback spec). main attaches it.
var FeedbackCmd = newFeedbackCmd()

// feedbackReq is the ID of the one request a feedback command makes.
const feedbackReq core.ReqID = 1

// envEffort is the variable Claude Code sets in every Bash call to the
// turn's effort.
const envEffort = "CLAUDE_EFFORT"

func newFeedbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "File and read notes about loom itself, which loom's session agents write",
	}
	cmd.AddCommand(newFeedbackAddCmd(), newFeedbackListCmd(), newFeedbackShowCmd(), newFeedbackFixCmd(), newFeedbackCloseCmd())
	return cmd
}

func newFeedbackAddCmd() *cobra.Command {
	var kind, title, text, file string
	cmd := &cobra.Command{
		Use:   "add --kind K --title T (--text X | --file F)",
		Short: "File a note about loom from inside a session loom launched",
		Long: "File a note about loom's own tooling (loom session, loom job, the daemon, the loom context): " +
			"what failed, surprised you, contradicted the context, or was missing. The daemon records which " +
			"session filed it, its model and effort, and its loom build, and groups it with notes about the same problem.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			body, err := feedbackText(text, file, c.Flags().Changed("text"))
			if err != nil {
				return err
			}
			note, err := feedbackNote(kind, title, body)
			if err != nil {
				return err
			}
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				d.FileFeedback(note, feedbackReq)
				r, err := d.awaitReply(feedbackReq)
				switch {
				case err != nil:
					return err
				case r.Err != nil:
					return r.Err
				}
				fmt.Fprintf(c.OutOrStdout(), "[loom] feedback n%d filed\n", r.Note)
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&kind, "kind", "", "What it is: "+kindList("|"))
	fl.StringVar(&title, "title", "", "One line naming the problem")
	fl.StringVar(&text, "text", "", "What happened, what you expected, and the command")
	fl.StringVar(&file, "file", "", "Read the text from this file")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
	cmd.MarkFlagsOneRequired("text", "file")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

// feedbackNote is add's note, with the shell it runs in: the launch
// ($LOOM_LAUNCH, which the daemon resolves to the filing session) and the
// turn's effort ($CLAUDE_EFFORT, which Claude Code sets in every Bash
// call). With no launch the command runs outside a session loom launched,
// and is refused before it reaches the daemon.
func feedbackNote(kind, title, body string) (core.FeedbackNote, error) {
	launch := os.Getenv(claudemod.EnvLaunch)
	if launch == "" {
		return core.FeedbackNote{}, fmt.Errorf("loom feedback add runs inside a session loom launched: $%s is not set", claudemod.EnvLaunch)
	}
	return core.FeedbackNote{Kind: feedback.Kind(kind), Title: title, Text: body, Launch: launch, Effort: os.Getenv(envEffort)}, nil
}

// kindList is the kinds, joined by sep.
func kindList(sep string) string {
	words := make([]string, len(feedback.Kinds))
	for i, k := range feedback.Kinds {
		words[i] = string(k)
	}
	return strings.Join(words, sep)
}

// feedbackText is add's text: --text's or --file's. The model checks its
// bounds.
func feedbackText(text, file string, hasText bool) (string, error) {
	if hasText {
		return text, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("--file: %w", err)
	}
	return string(b), nil
}

func newFeedbackListCmd() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "list [--all] [--json]",
		Short: "List the threads and the notes not in one yet, most-reported first",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				v := d.Feedback()
				rows := feedback.Rank(v.Inbox, all)
				if asJSON {
					if rows == nil {
						rows = []feedback.Row{}
					}
					enc := json.NewEncoder(c.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(rows)
				}
				return writeFeedbackList(c.OutOrStdout(), v, rows, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include closed threads")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the rows as JSON")
	return cmd
}

// rowID is a row's ID as the commands take it: "3" for a thread, "n12" for
// a note no thread holds.
func rowID(r feedback.Row) string {
	if r.Thread != 0 {
		return strconv.Itoa(int(r.Thread))
	}
	return "n" + strconv.Itoa(int(r.Note))
}

// rowStatus is a thread's status (fixing names its session) or a note's
// grouping.
func rowStatus(r feedback.Row) string {
	switch {
	case r.Thread == 0:
		return string(r.Grouping)
	case r.Status == feedback.Fixing && r.Fix.Title != "":
		return "fixing " + r.Fix.Title
	}
	return string(r.Status)
}

// writeFeedbackList prints rows as a table, then the grouper's state and
// the inbox's error when there are any.
func writeFeedbackList(w io.Writer, v core.FeedbackView, rows []feedback.Row, now time.Time) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No feedback.")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tSESSIONS\tNOTES\tLAST\tSTATUS\tTITLE")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n", rowID(r), r.Kind, r.Sessions, r.Notes, since(r.Last, now), rowStatus(r), r.Title)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if v.Grouper != "" {
		fmt.Fprintf(w, "grouper: %s\n", v.Grouper)
	}
	if v.Err != "" {
		fmt.Fprintf(w, "inbox: %s\n", v.Err)
	}
	return nil
}

// parseRowID reads "3" as thread 3 and "n12" as note 12.
func parseRowID(s string) (feedback.ThreadID, feedback.NoteID, error) {
	if rest, ok := strings.CutPrefix(s, "n"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil || n < 1 {
			return 0, 0, fmt.Errorf("%q is not a note: write n and its number, as list shows it", s)
		}
		return 0, feedback.NoteID(n), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, 0, fmt.Errorf("%q is not a thread or a note: write a thread's number, or n and a note's", s)
	}
	return feedback.ThreadID(n), 0, nil
}

// parseThread reads a thread's number.
func parseThread(s string) (feedback.ThreadID, error) {
	t, n, err := parseRowID(s)
	switch {
	case err != nil:
		return 0, err
	case n != 0:
		return 0, fmt.Errorf("%s is a note no thread holds yet: only a thread can be fixed or closed", s)
	}
	return t, nil
}

func newFeedbackShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <thread | nN>",
		Short: "Show a thread with every note in it, or one note",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			t, n, err := parseRowID(args[0])
			if err != nil {
				return err
			}
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				in := d.Feedback().Inbox
				out := c.OutOrStdout()
				if n != 0 {
					note, ok := in.Note(n)
					if !ok {
						return fmt.Errorf("note %d doesn't exist", n)
					}
					writeNote(out, note)
					return nil
				}
				th, ok := in.Thread(t)
				if !ok {
					return fmt.Errorf("thread %d doesn't exist", t)
				}
				fmt.Fprintf(out, "thread %d (%s, %s, %d sessions): %s\n", th.ID, th.Kind, th.Status, in.Sessions(th), th.Title)
				if th.Fix.ID != 0 {
					fmt.Fprintf(out, "fix session: %s in %s, started %s\n", th.Fix.Title, th.Fix.Workspace, th.Fix.At.Local().Format("2006-01-02 15:04"))
				}
				for _, id := range th.Notes {
					if note, ok := in.Note(id); ok {
						fmt.Fprintln(out)
						writeNote(out, note)
					}
				}
				return nil
			})
		},
	}
}

// writeNote prints one note: who filed it, on what, then its title and
// text.
func writeNote(w io.Writer, n feedback.Note) {
	fmt.Fprintf(w, "n%d (%s) from %q in %s, %s\n", n.ID, n.Kind, n.From.Title, n.From.Workspace, n.At.Local().Format("2006-01-02 15:04"))
	if n.From.Agent != "" {
		fmt.Fprintf(w, "  subagent: %s\n", n.From.Agent)
	}
	if me := feedback.ModelEffort(n.From); me != "" {
		fmt.Fprintf(w, "  ran on:   %s\n", me)
	}
	if n.Build != "" {
		fmt.Fprintf(w, "  loom:     %s\n", n.Build)
	}
	if n.From.ClaudeSession != "" {
		fmt.Fprintf(w, "  claude:   %s %s\n", n.From.ClaudeSession, n.From.Transcript)
	}
	switch n.Grouping {
	case feedback.Grouped:
		fmt.Fprintf(w, "  thread:   %d\n", n.Thread)
	case feedback.Ungrouped:
		fmt.Fprintf(w, "  ungrouped: %s\n", n.GroupErr)
	default:
		fmt.Fprintf(w, "  %s\n", n.Grouping)
	}
	fmt.Fprintf(w, "\n  %s\n\n", n.Title)
	for line := range strings.SplitSeq(n.Text, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func newFeedbackFixCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "fix <thread> [-w <workspace>]",
		Short: "Start a session that fixes a thread's problem, and wait until it runs",
		Long: "Start a session titled fb-<thread>-<slug> in the workspace (-w, else the global config.json's " +
			"feedback.workspace), prompted with every note in the thread, and wait until it runs.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			t, err := parseThread(args[0])
			if err != nil {
				return err
			}
			loomErrors(c)
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				d.FixThread(t, workspace, feedbackReq)
				st, err := d.awaitStart(feedbackReq)
				switch {
				case errors.Is(err, context.Canceled):
					return fmt.Errorf("interrupted: the loom daemon is still starting thread %d's fix session", t)
				case err != nil:
					return err
				}
				out := c.OutOrStdout()
				fmt.Fprintf(out, "Started %q in %s\n", st.Title, st.OwnerLabel)
				if iv, ok := d.View(st.ID); ok {
					if iv.Branch != "" {
						fmt.Fprintf(out, "  branch:   %s\n", iv.Branch)
					}
					if iv.WorktreePath != "" {
						fmt.Fprintf(out, "  worktree: %s\n", iv.WorktreePath)
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&workspace, "workspace", "w", "", "The workspace to start it in (default: feedback.workspace)")
	return cmd
}

func newFeedbackCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <thread>",
		Short: "Close a thread: a problem that comes back opens a new one",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			t, err := parseThread(args[0])
			if err != nil {
				return err
			}
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				d.CloseThread(t, feedbackReq)
				r, err := d.awaitReply(feedbackReq)
				switch {
				case err != nil:
					return err
				case r.Err != nil:
					return r.Err
				}
				fmt.Fprintf(c.OutOrStdout(), "Closed thread %d\n", t)
				return nil
			})
		},
	}
}
