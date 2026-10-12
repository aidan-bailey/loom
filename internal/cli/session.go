package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)

// SessionCmd is `loom session`: create, list and drive the sessions of a
// registered workspace, as clients of the daemon. main attaches it.
var SessionCmd = newSessionCmd()

// newSessionCmd builds the `loom session` command tree, with flags of its
// own: each call is independent (tests build one per run).
func newSessionCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Create, list and drive a workspace's sessions",
	}
	cmd.PersistentFlags().StringVarP(&workspace, "workspace", "w", "",
		"Registered workspace (default: the one whose repository holds the current directory)")
	cmd.AddCommand(newSessionListCmd(&workspace))
	return cmd
}

func newSessionListCmd(workspace *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a workspace's sessions",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				rows := sessions(d.Views(v.ID))
				if asJSON {
					return writeSessionsJSON(c.OutOrStdout(), rows)
				}
				return writeSessionsTable(c.OutOrStdout(), v.Label, rows, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the sessions as a JSON array")
	return cmd
}

// sessions are the rows a session command shows and names: every row but
// the workspace terminal, Recoverable ones included.
func sessions(views []core.InstanceView) []core.InstanceView {
	out := make([]core.InstanceView, 0, len(views))
	for _, v := range views {
		if !v.IsWorkspaceTerminal {
			out = append(out, v)
		}
	}
	return out
}

// findSession is the session titled title among views, by exact title,
// never a prefix, and never the workspace terminal. Several sessions under
// one title are refused unless id names one; an id must still match the
// title, so a stale one can't land on another session.
func findSession(views []core.InstanceView, title string, id core.InstanceID) (core.InstanceView, error) {
	var found []core.InstanceView
	for _, v := range sessions(views) {
		if v.Title == title && (id == 0 || v.ID == id) {
			found = append(found, v)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return core.InstanceView{}, fmt.Errorf("%d sessions are titled %q: name one with --id (see loom session list --json)", len(found), title)
	case id != 0:
		return core.InstanceView{}, fmt.Errorf("no session %q with id %d", title, id)
	default:
		return core.InstanceView{}, fmt.Errorf("no session %q", title)
	}
}

// sessionJSON is a session as `loom session list --json` prints it. The
// field names are a contract that scripts and scrum's board read:
// TestSessionList_JSONFieldNames pins them.
type sessionJSON struct {
	ID          core.InstanceID `json:"id"`
	Title       string          `json:"title"`
	Status      string          `json:"status"`
	WaitReason  string          `json:"wait_reason"`
	StatusSince time.Time       `json:"status_since"`
	Branch      string          `json:"branch"`
	Worktree    string          `json:"worktree"`
	Repo        string          `json:"repo"`
	Program     string          `json:"program"`
	Account     string          `json:"account"`
	Issue       int             `json:"issue"`
	PR          int             `json:"pr"`
}

func writeSessionsJSON(w io.Writer, rows []core.InstanceView) error {
	out := make([]sessionJSON, 0, len(rows))
	for _, v := range rows {
		pr := 0
		if v.GitHub.HasPR {
			pr = v.GitHub.PRNumber
		}
		out = append(out, sessionJSON{
			ID: v.ID, Title: v.Title, Status: v.Status.String(), WaitReason: v.WaitReason,
			StatusSince: v.StatusSince, Branch: v.Branch, Worktree: v.WorktreePath, Repo: v.RepoPath,
			Program: v.Program, Account: v.Account, Issue: v.Issue, PR: pr,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSessionsTable(w io.Writer, label string, rows []core.InstanceView, now time.Time) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No sessions in %s. Use 'loom session new' to create one.\n", label)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TITLE\tSTATUS\tBRANCH\tACCOUNT\tSINCE")
	for _, v := range rows {
		status := v.Status.String()
		if v.WaitReason != "" {
			status += " (" + v.WaitReason + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.Title, status, orDash(v.Branch), orDash(v.Account), since(v.StatusSince, now))
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// since is how long before now t was, coarsely ("-" for a zero t).
func since(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
