package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/session"

	"github.com/spf13/cobra"
)

var (
	accountNoLogin bool
	// accountForce overrides remove's in-use and unshared-files refusals
	// (and reports what it overrode). It does NOT imply accountYes — a
	// forced removal still asks for confirmation unless --yes is given
	// too, so --force alone can't be a single accidental keystroke away
	// from deleting a session's only account.
	accountForce bool
	// accountYes skips remove's confirmation prompt only; the in-use and
	// unshared-files refusals still apply unless accountForce also
	// overrides them.
	accountYes bool

	// accountExec runs the claude CLI for the login's report; a var so
	// tests answer it without a real claude.
	accountExec cmd.Executor = cmd.Exec{}
	// accountLogin runs the interactive login; a var so tests skip it.
	accountLogin = runAccountLogin
)

// AccountCmd is the parent command for Claude account management. Its
// commands are clients of the daemon, which creates, removes, links and
// probes the accounts; each reads its registry from disk only for the
// guards on its own environment (guardAgainstAccountEnv) and remove's
// checks before its prompt.
var AccountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage the Claude accounts sessions can run on",
}

// loadAccountRegistry reads the account registry, which the daemon owns,
// for what a command checks on its own side: the accounts dir its
// environment is guarded against, and remove's checks before its prompt.
// A registry that does not load is refused, as the daemon refuses it.
func loadAccountRegistry() (*account.Registry, error) {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	reg := account.LoadRegistry(globalDir)
	return reg, reg.LoadErr()
}

// claudeProgram is the Claude CLI `login` runs in this terminal: the
// program configured in the global config dir when it is Claude (so a
// pinned or Nix path is honored), else "claude" on PATH. The daemon runs
// its own probes with the program it was started with.
//
// "The global config dir" is config.GetGlobalConfigDir() (LOOM_GLOBAL_DIR,
// default ~/.loom) — the same directory loadAccountRegistry reads the
// account registry from, and the one config.GlobalWorkspaceContext hands
// the TUI's classic/global-mode startup (main.go) and `loom debug`, both of
// which then config.LoadConfigFrom(wsCtx.ConfigDir) from it. It is
// deliberately NOT config.LoadConfigFromGlobal(), which reads
// config.GetConfigDir() (LOOM_HOME): LOOM_HOME is a distinct, independently
// overridable variable, and production classic-mode startup never reads
// config.json from it — only workspace mode does, from a workspace's own
// config dir, which these account commands have no notion of. Reading
// LOOM_HOME here would silently ignore the configured program (pinned
// path, Nix store path, profile) whenever the two vars differ, falling
// back to a bare "claude" on PATH without any indication why.
func claudeProgram() string {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return "claude"
	}
	if p := config.LoadConfigFrom(globalDir).GetProgram(); session.IsClaudeProgram(p) {
		return p
	}
	return "claude"
}

// mainDirOf is the main config dir the daemon links accounts against, as
// it reports the default account's identity ("" until it has read it).
func mainDirOf(d *client) string {
	return d.RCAuthFor(account.DefaultName).Identity.ConfigDir
}

// sameMainDir refuses `login default` when the main config dir this shell
// would log in (its CLAUDE_CONFIG_DIR, else ~/.claude: account.MainDir)
// is not the one the daemon runs the default account in: the login would
// go to a dir no session uses. A daemon that has not read its default
// account's identity yet decides nothing.
func sameMainDir(d *client) error {
	daemonDir := mainDirOf(d)
	if daemonDir == "" {
		return nil
	}
	own := account.MainDir(account.Identity{})
	if session.CanonicalPath(own) == session.CanonicalPath(daemonDir) {
		return nil
	}
	return fmt.Errorf("this shell's main Claude config dir is %s, but the loom daemon runs the default account in %s: run `loom account login default` from a shell whose CLAUDE_CONFIG_DIR is %s (or unset, if that is your ~/.claude)",
		dash(own), daemonDir, daemonDir)
}

// runWithChildSignals runs c the way a foreground, interactive child
// should: SIGINT is caught, not ignored, here in loom, so the same Ctrl-C
// the terminal delivers to c doesn't also abort loom's own RunE mid-flow
// (during `add`, that would skip the "created but not logged in" report
// below) — the signal lands in ch instead of loom's default (process-
// terminating) handling and is otherwise never acted on. Catching rather
// than ignoring matters for the child too: a signal.Ignore'd disposition
// is SIG_IGN at the OS level, which survives exec into the child (and any
// children after it) — the login process would inherit SIGINT
// permanently ignored, so Ctrl-C would reach neither loom nor claude. A
// caught signal has no such leak: POSIX resets it to its default
// disposition across exec, so c starts with ordinary SIGINT handling of
// its own.
func runWithChildSignals(c *exec.Cmd) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer signal.Stop(ch)
	return c.Run()
}

func runAccountLogin(program string, env []string) error {
	c := account.LoginCmd(program, env)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return runWithChildSignals(c)
}

// loginAndReport runs the login, then says who the account is now.
func loginAndReport(out io.Writer, program, name string, env []string) error {
	if err := accountLogin(program, env); err != nil {
		return fmt.Errorf("claude auth login for %s: %w", name, err)
	}
	id, err := account.AuthStatus(program, env, accountExec)
	switch {
	case err != nil:
		fmt.Fprintf(out, "Could not confirm the login: %v\n", err)
	case !id.LoggedIn:
		fmt.Fprintf(out, "%s is still logged out\n", name)
	default:
		fmt.Fprintf(out, "Logged in %s as %s (%s)\n", name, id.Email, id.Plan)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// resolvedOrClean resolves path's symlinks when possible, else falls back
// to a plain Clean — good enough for accountFromEnv below, where a
// resolution failure just means the compared dir does not exist, which is
// already "not inside AccountsDir".
func resolvedOrClean(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// accountFromEnv reports the account name this process's own
// CLAUDE_CONFIG_DIR already selects, when it resolves (symlinks included)
// inside reg.AccountsDir(). "" and false when CLAUDE_CONFIG_DIR is unset
// or points elsewhere.
func accountFromEnv(reg *account.Registry) (string, bool) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return "", false
	}
	accountsDir, got := resolvedOrClean(reg.AccountsDir()), resolvedOrClean(dir)
	rel, err := filepath.Rel(accountsDir, got)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	name, _, _ := strings.Cut(rel, string(filepath.Separator))
	if name == "" {
		return "", false
	}
	return name, true
}

// localCredentialOverride names a credential env var set in this
// command's own environment (account.ActiveCredentialOverride); a var so
// tests tell it from the daemon's, which is the same process's in them.
var localCredentialOverride = account.ActiveCredentialOverride

// warnOverride prints the warning that the credential in $name overrides
// every account's own login: claude checks CLAUDE_CODE_OAUTH_TOKEN,
// ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN before ever reading a config
// dir's stored credentials, so every account runs (and bills) as that one
// credential, whichever config dir CLAUDE_CONFIG_DIR names: account
// selection has no effect. where says whose environment it is in.
func warnOverride(out io.Writer, name, where string) {
	fmt.Fprintf(out, "warning: $%s is set%s — every account runs as that credential, so account selection has no effect; unset it to use accounts\n", name, where)
}

// warnDaemonOverride warns when a credential in the daemon's environment
// overrides every account (Core.CredentialOverride): sessions run in the
// daemon's environment (its tmux server's), not this command's. Call it
// after a request, so the published view is the daemon's now.
func warnDaemonOverride(out io.Writer, d *client) string {
	name := d.CredentialOverride()
	if name != "" {
		warnOverride(out, name, " in the loom daemon's environment")
	}
	return name
}

// warnLoginOverride warns when a credential in this command's own
// environment, which a login's claude runs in, overrides it, unless the
// daemon's warning named it already (warned).
func warnLoginOverride(out io.Writer, warned string) {
	if name, ok := localCredentialOverride(); ok && name != warned {
		warnOverride(out, name, "")
	}
}

// guardAgainstAccountEnv refuses an operation that targets the default
// account or the main config dir when this process's own
// CLAUDE_CONFIG_DIR already resolves inside one of the accounts it
// manages: the shell loom is running in is itself running AS that
// account, and treating its inherited env as "the default" would
// silently act on the wrong identity — log the wrong account in as
// default, list or sync it as if it were the main login. targetsDefault
// is false for a command that already names its own account explicitly
// (use, remove, a login of a specific extra account), which read that
// account's own CLAUDE_CONFIG_DIR override rather than the inherited one
// and so are unaffected.
func guardAgainstAccountEnv(reg *account.Registry, targetsDefault bool) error {
	if !targetsDefault {
		return nil
	}
	name, ok := accountFromEnv(reg)
	if !ok {
		return nil
	}
	return fmt.Errorf("CLAUDE_CONFIG_DIR points at account %q (this shell runs as that account); run loom account from a shell without it", name)
}

// requestRefresh asks the daemon to reread its accounts' auth (and the
// default account's too when withDefault), as the TUI does after a login,
// and waits for the request after it: the command exits next, and its
// cast must reach the daemon before the connection closes.
func requestRefresh(d *client, withDefault bool) {
	d.RequestAccountsRefresh(withDefault)
	d.ReloadAccounts()
}

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create an account, share your Claude setup with it, and log it in",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out := c.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		return withDaemon(c, func(d *client) error {
			d.ReloadAccounts()
			warned := warnDaemonOverride(out, d)
			// The daemon creates it, linked against the main config dir
			// it knows, which it validates first (account.ValidateMainDir).
			name, err := d.AddAccount(args[0])
			if err != nil {
				return err
			}
			acct, _ := d.Account(name)
			rep, _ := d.AccountSync(name)
			main := mainDirOf(d)
			if main == "" {
				main = "your main Claude config dir"
			}
			fmt.Fprintf(out, "Created %s (linked %d entries from %s)\n", acct.Dir, len(rep.Linked), main)
			for _, entry := range rep.Diverged {
				fmt.Fprintf(out, "  not shared: %s\n", entry)
			}
			if accountNoLogin {
				requestRefresh(d, false)
				fmt.Fprintf(out, "Log in later with: loom account login %s\n", name)
				return nil
			}
			warnLoginOverride(out, warned)
			err = loginAndReport(out, claudeProgram(), name, account.EnvFor(acct.Dir))
			requestRefresh(d, false)
			if err != nil {
				fmt.Fprintf(out, "%s was created; finish with: loom account login %s\n", name, name)
				return err
			}
			return nil
		})
	},
}

var accountLoginCmd = &cobra.Command{
	Use:   "login <name>",
	Short: `Log an account in to Claude ("default" is your main login)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out := c.OutOrStdout()
		// The login's claude runs here, in this command's environment.
		warnLoginOverride(out, "")
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		name := args[0]
		targetsDefault := name == "" || name == account.DefaultName
		if err := guardAgainstAccountEnv(reg, targetsDefault); err != nil {
			return err
		}
		return withDaemon(c, func(d *client) error {
			d.ReloadAccounts()
			env, err := d.AccountEnv(name)
			if err != nil {
				return err
			}
			if targetsDefault {
				if err := sameMainDir(d); err != nil {
					return err
				}
			} else {
				if a, ok := d.Account(name); ok {
					if _, statErr := os.Stat(a.Dir); statErr != nil {
						if os.IsNotExist(statErr) {
							return fmt.Errorf("account %q's config dir %s is missing; run: loom account remove %s, then: loom account add %s",
								name, a.Dir, name, name)
						}
						return fmt.Errorf("account %q: checking its config dir: %w", name, statErr)
					}
				}
			}
			err = loginAndReport(out, claudeProgram(), name, env)
			requestRefresh(d, targetsDefault)
			return err
		})
	},
}

// accountRow is one line of `list`'s table.
type accountRow struct {
	name string
	mark string
	id   account.Identity
	u    account.Usage
	note string
}

// listRow is name's row as the daemon publishes it: its identity, its
// latest usage sample and the last probe's error. A sample from before
// since says how old it is.
func listRow(d *client, def, name string, since, now time.Time) accountRow {
	row := accountRow{name: name, id: d.RCAuthFor(name).Identity}
	if name == def {
		row.mark = "*"
	}
	u, err := d.AccountUsage(name)
	row.u = u
	switch {
	case err != nil:
		row.note = err.Error()
	case d.AccountLoggedOut(name):
		row.note = "logged out"
	case u.At.IsZero():
	case !u.Available:
		row.note = "no plan limits"
	}
	if !u.At.IsZero() && u.At.Before(since) {
		row.note = strings.TrimSpace(row.note + " (as of " + ageText(now.Sub(u.At)) + " ago)")
	}
	return row
}

// ageText renders a usage sample's age, to the minute.
func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// probed reports that the daemon's usage round since started has landed
// for every name: each has a sample from it, or a failed probe.
func probed(d *client, names []string, since time.Time) func([]core.Event) bool {
	return func([]core.Event) bool {
		for _, name := range names {
			if u, err := d.AccountUsage(name); err == nil && u.At.Before(since) {
				return false
			}
		}
		return true
	}
}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List accounts with their login and plan usage",
	Args:  cobra.NoArgs,
	RunE: func(c *cobra.Command, args []string) error {
		out := c.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		return withDaemon(c, func(d *client) error {
			d.ReloadAccounts()
			warnDaemonOverride(out, d)
			names := d.AccountNames()
			since := time.Now()
			// The daemon probes usage only once an extra account exists,
			// and only with a Claude program: then a round is asked for
			// and waited for, and a sample older than it is shown with
			// its age. With the default account alone it probes nothing,
			// so its row shows no usage (unlike `list` before the daemon,
			// which probed it here).
			if d.HasExtraAccounts() && d.ClaudeProgram() != "" {
				d.RequestUsageProbe()
				if _, err := d.await(accountWait, probed(d, names.Names, since)); err != nil {
					return err
				}
			}
			now := time.Now()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "\tNAME\tEMAIL\tPLAN\t5H\t7D\tNOTE")
			for _, name := range names.Names {
				row := listRow(d, names.Default, name, since, now)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.mark, row.name, dash(row.id.Email), dash(row.id.Plan),
					dash(row.u.FiveHour.Text(now)), dash(row.u.SevenDay.Text(now)), row.note)
			}
			return w.Flush()
		})
	},
}

var accountUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the account new sessions preselect",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		return withDaemon(c, func(d *client) error {
			if err := d.SetDefaultAccount(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Default account set to %q\n", args[0])
			return nil
		})
	},
}

// extraNames are the accounts besides default, in the daemon's order.
func extraNames(names core.AccountNames) []string {
	var out []string
	for _, name := range names.Names {
		if name != account.DefaultName {
			out = append(out, name)
		}
	}
	return out
}

// synced reports that every name's link report is from a sync started at
// since or later: the daemon's refresh asked for then.
func synced(d *client, names []string, since time.Time) func([]core.Event) bool {
	return func([]core.Event) bool {
		for _, name := range names {
			if rep, ok := d.AccountSync(name); !ok || rep.At.Before(since) {
				return false
			}
		}
		return true
	}
}

// safeMainDir refuses the main config dir the daemon reports when accounts
// may not be linked against it (account.ValidateMainDir): the daemon then
// links nothing, and the reports would be the last ones.
func safeMainDir(d *client, reg *account.Registry) error {
	if main := mainDirOf(d); main != "" {
		return account.ValidateMainDir(main, reg.AccountsDir())
	}
	return nil
}

var accountSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Share new entries of your main Claude config dir with every account",
	Args:  cobra.NoArgs,
	RunE: func(c *cobra.Command, args []string) error {
		out := c.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		return withDaemon(c, func(d *client) error {
			d.ReloadAccounts()
			if err := safeMainDir(d, reg); err != nil {
				return err
			}
			// The daemon re-links every account in its accounts refresh,
			// which rereads the default account's identity too; each
			// report says when its sync ran.
			names, since := extraNames(d.AccountNames()), time.Now()
			d.RequestAccountsRefresh(true)
			if _, err := d.await(accountWait, synced(d, names, since)); err != nil {
				return err
			}
			if err := safeMainDir(d, reg); err != nil {
				return err
			}
			var missed []string
			for _, name := range names {
				rep, _ := d.AccountSync(name)
				if rep.At.Before(since) {
					missed = append(missed, name)
					continue
				}
				fmt.Fprintf(out, "%s: linked %d new\n", name, len(rep.Linked))
				for _, entry := range rep.Diverged {
					fmt.Fprintf(out, "  not shared: %s\n", entry)
				}
			}
			if len(missed) > 0 {
				return fmt.Errorf("the loom daemon did not sync %s within %s (see %s)", strings.Join(missed, ", "), accountWait, serveLogHint())
			}
			return nil
		})
	},
}

// serveLogHint names the daemon's log for a message.
func serveLogHint() string {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return "the loom daemon's log"
	}
	return daemon.LogPath(globalDir)
}

// accountUserCount counts the stored sessions using name across every
// config dir loom knows about (account.KnownStateDirs), for remove's
// check before its prompt; the daemon checks its live sessions again.
func accountUserCount(name string) (int, error) {
	dirs, err := account.KnownStateDirs()
	if err != nil {
		return 0, err
	}
	return account.CountUsers(dirs, name)
}

var accountRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an account and delete its config dir",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out, name := c.OutOrStdout(), args[0]
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if _, ok := reg.Get(name); !ok {
			return fmt.Errorf("account %q is not registered", name)
		}

		// In-use check: --force overrides it (and says so); nothing but
		// --force does, so --yes alone still refuses.
		n, cerr := accountUserCount(name)
		switch {
		case cerr != nil && !accountForce:
			return fmt.Errorf("can't tell whether sessions use %s: %w (--force removes it anyway)", name, cerr)
		case cerr == nil && n > 0 && !accountForce:
			return fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account first (or use --force)", n, name)
		case cerr == nil && n > 0:
			fmt.Fprintf(out, "--force: overriding %d session(s) using %s\n", n, name)
		}

		// Unshared-files check: run (and, if it refuses, report) before
		// the confirmation prompt, so the user is never asked to confirm
		// a removal that then turns out to be refused anyway.
		dir, owned := reg.OwnedDir(name)
		if owned {
			unshared, uerr := account.Unshared(dir)
			switch {
			case uerr != nil && !accountForce:
				return fmt.Errorf("account %q: checking for unshared files: %w", name, uerr)
			case uerr == nil && len(unshared) > 0 && !accountForce:
				return &account.UnsharedError{Name: name, Entries: unshared}
			case uerr == nil && len(unshared) > 0:
				fmt.Fprintf(out, "--force: overriding unshared files: %s\n", strings.Join(unshared, ", "))
			}
		}

		if !accountYes {
			if owned {
				fmt.Fprintf(out, "Remove account %q and delete %s? [y/N] ", name, dir)
			} else {
				fmt.Fprintf(out, "Remove account %q? Its dir %s is not loom's and will be left in place. [y/N] ", name, dir)
			}
			line, _ := bufio.NewReader(c.InOrStdin()).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}

		// The daemon removes it, checking its live sessions again: with
		// force it overrides what was overridden here.
		return withDaemon(c, func(d *client) error {
			if err := d.RemoveAccount(name, accountForce); err != nil {
				return err
			}
			if owned {
				fmt.Fprintf(out, "Removed %s and deleted %s\n", name, dir)
			} else {
				fmt.Fprintf(out, "Removed %s (left %s in place: loom did not create it)\n", name, dir)
			}
			return nil
		})
	},
}

func init() {
	accountAddCmd.Flags().BoolVar(&accountNoLogin, "no-login", false, "Create the account without logging it in")
	accountRemoveCmd.Flags().BoolVar(&accountForce, "force", false, "Override the in-use and unshared-files refusals (reports what it overrode)")
	accountRemoveCmd.Flags().BoolVarP(&accountYes, "yes", "y", false, "Skip the confirmation prompt")
	AccountCmd.AddCommand(accountAddCmd, accountLoginCmd, accountListCmd, accountUseCmd, accountSyncCmd, accountRemoveCmd)
}
