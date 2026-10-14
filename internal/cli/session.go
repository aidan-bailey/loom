package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
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
	cmd.AddCommand(
		newSessionListCmd(&workspace),
		newSessionNewCmd(&workspace),
		newSessionSendCmd(&workspace),
		newSessionOpCmd(&workspace, "kill", "Kill a session: its agent, tmux session and worktree", "Killed", true,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Kill(id, req) }),
		newSessionOpCmd(&workspace, "pause", "Pause a session: its agent stops and its worktree is removed, its branch kept", "Paused", false,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Pause(id, req) }),
		newSessionOpCmd(&workspace, "resume", "Resume a paused session", "Resumed", false,
			func(d *client, id core.InstanceID, req core.ReqID) { d.Resume(id, req) }),
	)
	return cmd
}

// loomErrors marks c's errors from here on as loom's, not usage mistakes:
// main prints them once, with no usage text after them, as it does the
// root command's.
func loomErrors(c *cobra.Command) {
	c.SilenceUsage, c.SilenceErrors = true, true
}

// sessionReq is the ID of the one request a session command makes, which
// its Reply (and a Create's Started and notices) names.
const sessionReq core.ReqID = 1

func newSessionListCmd(workspace *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a workspace's sessions",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
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

// newFlags are `loom session new`'s flags.
type newFlags struct {
	prompt, promptFile, branch             string
	issue                                  int
	account, model, permissionMode, effort string
	noRemoteControl                        bool
}

// validate refuses flag values the Session Launch Options modal doesn't
// offer.
func (f newFlags) validate() error {
	for _, o := range []struct {
		name, value string
		allowed     []string
	}{
		{"model", f.model, config.ClaudeModelAliases()},
		{"permission-mode", f.permissionMode, config.ClaudePermissionModes},
		{"effort", f.effort, config.ClaudeEfforts},
	} {
		if o.value != "" && !slices.Contains(o.allowed, o.value) {
			return fmt.Errorf("--%s %q: want one of %s", o.name, o.value, strings.Join(o.allowed, ", "))
		}
	}
	if f.issue < 0 {
		return fmt.Errorf("--issue %d: want an issue number", f.issue)
	}
	return nil
}

// readPrompt is the creation prompt: --prompt's, or --prompt-file's text
// without its trailing newlines (the paste's Enter submits it).
func (f newFlags) readPrompt() (string, error) {
	if f.promptFile == "" {
		return f.prompt, nil
	}
	b, err := os.ReadFile(f.promptFile)
	if err != nil {
		return "", fmt.Errorf("--prompt-file: %w", err)
	}
	return strings.TrimRight(string(b), "\n"), nil
}

func newSessionNewCmd(workspace *string) *cobra.Command {
	var f newFlags
	cmd := &cobra.Command{
		Use:   "new <title>",
		Short: "Create and start a session, and wait until it runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			prompt, err := f.readPrompt()
			if err != nil {
				return err
			}
			loomErrors(c)
			// An interrupt ends the wait (await), not the start.
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				return d.newSession(c.OutOrStdout(), *workspace, args[0], prompt, f)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.prompt, "prompt", "", "Send this prompt once the agent is up")
	fl.StringVar(&f.promptFile, "prompt-file", "", "Send this file's text as the prompt")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	fl.StringVar(&f.branch, "branch", "", "Check out this existing branch (default: a new branch)")
	fl.IntVar(&f.issue, "issue", 0, "Link the session to this GitHub issue")
	fl.StringVar(&f.account, "account", "", "Claude account to run on (default: the default account)")
	fl.StringVar(&f.model, "model", "", "Model: "+strings.Join(config.ClaudeModelAliases(), ", "))
	fl.StringVar(&f.permissionMode, "permission-mode", "", "Permission mode: "+strings.Join(config.ClaudePermissionModes, ", "))
	fl.StringVar(&f.effort, "effort", "", "Effort: "+strings.Join(config.ClaudeEfforts, ", "))
	fl.BoolVar(&f.noRemoteControl, "no-remote-control", false, "Start without remote control")
	return cmd
}

// newSession creates title in the workspace and waits until it runs: the
// TUI's checks first (core's), then a title no row of the workspace
// holds, since the CLI names sessions by title, then the launch options
// (launchOptions), the Create and its start (awaitStart).
func (d *client) newSession(out io.Writer, workspace, title, prompt string, f newFlags) error {
	_, v, err := d.resolveWorkspace(workspace)
	if err != nil {
		return err
	}
	views := d.Views(v.ID)
	for _, check := range []error{core.CheckRoom(len(views)), core.CheckWritable(v), core.CheckNewTitle(title, v)} {
		if check != nil {
			return check
		}
	}
	for _, iv := range views {
		if iv.Title == title {
			return fmt.Errorf("%s already has a session titled %q", v.Label, title)
		}
	}
	program := v.Settings.GetProgram()
	d.ReloadAccounts()
	opts, err := d.launchOptions(v, program, f)
	if err != nil {
		return err
	}
	d.Create(v.ID, core.NewInstance{
		Title: title, Path: v.RepoPath, Program: program, Prompt: prompt,
		Branch: f.branch, Issue: f.issue, Launch: opts, Start: true,
	}, sessionReq)
	st, err := d.awaitStart(sessionReq)
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("interrupted: the loom daemon is still starting %q", title)
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "Started %q in %s\n", title, v.Label)
	if iv, ok := d.View(st.ID); ok {
		if iv.Branch != "" {
			fmt.Fprintf(out, "  branch:   %s\n", iv.Branch)
		}
		if iv.WorktreePath != "" {
			fmt.Fprintf(out, "  worktree: %s\n", iv.WorktreePath)
		}
	}
	return nil
}

// launchOptions are the options a new session in v launches program with:
// what the Session Launch Options modal pre-fills (the workspace's
// settings, then core.ChooseLaunchAccount), then f's flags. The caller
// reloads the accounts first. The modal's notice, and an --account a
// non-Claude program can't use, go to errOut. Remote control the
// account's login can't use is refused, naming --no-remote-control: the
// modal asks here, and a command can't.
func (d *client) launchOptions(v core.WorkspaceView, program string, f newFlags) (launch.Options, error) {
	opts, notice := core.ChooseLaunchAccount(d, launch.FromSettings(v.Settings), program)
	if notice != "" {
		fmt.Fprintf(d.errOut, "loom: %s\n", notice)
	}
	if f.account != "" {
		switch {
		case !session.IsClaudeProgram(program):
			fmt.Fprintf(d.errOut, "loom: --account ignored: %s is not Claude\n", program)
		case f.account != account.DefaultName:
			if _, ok := d.Account(f.account); !ok {
				return launch.Options{}, fmt.Errorf("account %q is not registered", f.account)
			}
			opts.Account = f.account
		default:
			opts.Account = f.account
		}
	}
	if f.model != "" {
		opts.Model = f.model
	}
	if f.permissionMode != "" {
		opts.PermissionMode = f.permissionMode
	}
	if f.effort != "" {
		opts.Effort = f.effort
	}
	if f.noRemoteControl {
		opts.RemoteControl = false
	}
	if auth := d.RCAuthFor(opts.Account); launch.RemoteControlBlocked(auth, launch.EffectiveRemoteControl(opts), program) {
		return launch.Options{}, fmt.Errorf("remote control is unavailable: %s (--no-remote-control starts without it)", auth.Reason)
	}
	return opts, nil
}

func newSessionSendCmd(workspace *string) *cobra.Command {
	var (
		id               uint64
		text, file, from string
		wait             bool
	)
	cmd := &cobra.Command{
		Use:   "send <title>",
		Short: "Send a message to a session's Claude, queued behind its running turn or open dialog",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			body, err := readMessage(c.InOrStdin(), text, file, c.Flags().Changed("text"))
			if err != nil {
				return err
			}
			loomErrors(c)
			// An interrupt ends the wait (await), not the delivery.
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				s, err := findSession(d.Views(v.ID), args[0], core.InstanceID(id))
				if err != nil {
					return fmt.Errorf("%s: %w", v.Label, err)
				}
				return d.send(c.OutOrStdout(), v.Label, s, core.Message{Text: body, From: from, Wait: wait, Origin: core.OriginCLI})
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&text, "text", "", "The message (default: read from stdin)")
	fl.StringVar(&file, "file", "", "Send this file's text")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
	fl.StringVar(&from, "from", "", "Name the sender on a line before the message")
	fl.BoolVar(&wait, "wait", false, "Wait until the turn that carries the message ends, and print Claude's reply")
	fl.Uint64Var(&id, "id", 0, "The session's ID (loom session list --json), when several share the title")
	return cmd
}

// readMessage is send's text: --text's, --file's, or stdin's, without its
// trailing newlines. An empty message is refused.
func readMessage(stdin io.Reader, text, file string, hasText bool) (string, error) {
	var s string
	switch {
	case hasText:
		s = text
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("--file: %w", err)
		}
		s = string(b)
	default:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		s = string(b)
	}
	s = strings.TrimRight(s, "\n")
	if strings.TrimSpace(s) == "" {
		return "", errors.New("send: the message is empty")
	}
	return s, nil
}

// send makes the Send request for s and waits for its Reply and, with
// msg.Wait, for its Answered, whose text alone goes to out. Messages Claude
// merged into one turn share its answer, so the text may answer them all.
func (d *client) send(out io.Writer, wsLabel string, s core.InstanceView, msg core.Message) error {
	d.Send(s.ID, msg, sessionReq)
	ans, err := d.awaitSend(sessionReq, msg.Wait)
	switch {
	case errors.Is(err, errDeliveredNoAnswer):
		return fmt.Errorf("interrupted: the message to %q was delivered; its reply will not be printed", s.Title)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("interrupted: the message to %q may still be delivered", s.Title)
	case err != nil:
		return err
	}
	if !msg.Wait {
		fmt.Fprintf(out, "Queued for %q in %s\n", s.Title, wsLabel)
		return nil
	}
	fmt.Fprintln(out, ans.Text)
	if ans.Reason != "" && ans.Reason != "answer" {
		return fmt.Errorf("the turn ended: %s", ans.Reason)
	}
	return nil
}

// newSessionOpCmd is `kill`, `pause` or `resume`: op's request on the
// session named by title (and --id), waiting for its Reply, then done.
// With confirm it asks first, as `account remove` does; -y skips that.
func newSessionOpCmd(workspace *string, verb, short, done string, confirm bool, op func(d *client, id core.InstanceID, req core.ReqID)) *cobra.Command {
	var (
		id  uint64
		yes bool
	)
	cmd := &cobra.Command{
		Use:   verb + " <title>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				s, err := findSession(d.Views(v.ID), args[0], core.InstanceID(id))
				if err != nil {
					return fmt.Errorf("%s: %w", v.Label, err)
				}
				if confirm && !yes {
					fmt.Fprintf(c.OutOrStdout(), "Kill session '%s' in %s? [y/N] ", s.Title, v.Label)
					line, _ := bufio.NewReader(c.InOrStdin()).ReadString('\n')
					if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
						return errors.New("aborted: nothing was killed")
					}
				}
				if err := d.request(func(req core.ReqID) { op(d, s.ID, req) }); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "%s %q in %s\n", done, s.Title, v.Label)
				return nil
			})
		},
	}
	cmd.Flags().Uint64Var(&id, "id", 0, "The session's ID (loom session list --json), when several share the title")
	if confirm {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	}
	return cmd
}
