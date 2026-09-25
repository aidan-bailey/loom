package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"

	"github.com/spf13/cobra"
)

var (
	accountNoLogin bool
	accountForce   bool

	// accountExec runs the claude CLI for the account commands; a var so
	// tests answer it without a real claude.
	accountExec Executor = Exec{}
	// accountMainDir resolves the main config dir accounts link to; a var
	// so tests never read the developer's ~/.claude.
	accountMainDir = defaultMainConfigDir
	// accountLogin runs the interactive login; a var so tests skip it.
	accountLogin = runAccountLogin
)

// AccountCmd is the parent command for Claude account management.
var AccountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage the Claude accounts sessions can run on",
}

func loadAccountRegistry() (*account.Registry, error) {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	reg := account.LoadRegistry(globalDir)
	return reg, reg.LoadErr()
}

// claudeProgram is the Claude CLI the account commands run: the global
// config's program when it is Claude (so a pinned or Nix path is honored),
// else "claude" on PATH.
func claudeProgram() string {
	if p := config.LoadConfigFromGlobal().GetProgram(); session.IsClaudeProgram(p) {
		return p
	}
	return "claude"
}

// defaultMainConfigDir is the default account's config dir
// (account.MainDir over what `claude auth status` reports).
func defaultMainConfigDir(program string) string {
	id, _ := account.AuthStatus(program, nil, accountExec)
	return account.MainDir(id)
}

func runAccountLogin(program string, env []string) error {
	c := account.LoginCmd(program, env)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
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

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create an account, share your Claude setup with it, and log it in",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		if main == "" {
			return fmt.Errorf("cannot locate your main Claude config dir")
		}
		acct, rep, err := reg.Create(args[0], main)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %s (linked %d entries from %s)\n", acct.Dir, len(rep.Linked), main)
		for _, d := range rep.Diverged {
			fmt.Fprintf(out, "  not shared: %s\n", d)
		}
		if accountNoLogin {
			fmt.Fprintf(out, "Log in later with: loom account login %s\n", acct.Name)
			return nil
		}
		return loginAndReport(out, program, acct.Name, account.EnvFor(acct.Dir))
	},
}

var accountLoginCmd = &cobra.Command{
	Use:   "login <name>",
	Short: `Log an account in to Claude ("default" is your main login)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		env, err := reg.Env(args[0])
		if err != nil {
			return err
		}
		return loginAndReport(cmd.OutOrStdout(), claudeProgram(), args[0], env)
	},
}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List accounts with their login and plan usage",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		now := time.Now()
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tEMAIL\tPLAN\t5H\t7D\tNOTE")
		for _, name := range reg.Names() {
			env, _ := reg.Env(name)
			cwd := main
			if a, ok := reg.Get(name); ok {
				cwd = a.Dir
			}
			mark := ""
			if name == reg.Default() {
				mark = "*"
			}
			var u account.Usage
			note := ""
			id, err := account.AuthStatus(program, env, accountExec)
			switch {
			case err != nil:
				note = err.Error()
			case !id.LoggedIn:
				note = "logged out"
			default:
				if u, err = account.ProbeUsage(program, env, cwd, accountExec); err != nil {
					note = err.Error()
				} else if !u.Available {
					note = "no plan limits"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", mark, name, dash(id.Email), dash(id.Plan),
				dash(u.FiveHour.Text(now)), dash(u.SevenDay.Text(now)), note)
		}
		return w.Flush()
	},
}

var accountUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the account new sessions preselect",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		if err := reg.SetDefault(args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Default account set to %q\n", args[0])
		return nil
	},
}

var accountSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Share new entries of your main Claude config dir with every account",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		main := accountMainDir(claudeProgram())
		for _, a := range reg.Accounts {
			rep, err := account.Sync(a.Dir, main)
			if err != nil {
				return fmt.Errorf("%s: %w", a.Name, err)
			}
			fmt.Fprintf(out, "%s: linked %d new\n", a.Name, len(rep.Linked))
			for _, d := range rep.Diverged {
				fmt.Fprintf(out, "  not shared: %s\n", d)
			}
		}
		return nil
	},
}

var accountRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an account and delete its config dir",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, name := cmd.OutOrStdout(), args[0]
		reg, err := loadAccountRegistry()
		if err != nil {
			return err
		}
		acct, ok := reg.Get(name)
		if !ok {
			return fmt.Errorf("account %q is not registered", name)
		}
		if !accountForce {
			dirs, err := account.KnownStateDirs()
			if err != nil {
				return fmt.Errorf("can't tell whether sessions use %s: %w (--force removes it anyway)", name, err)
			}
			n, err := account.CountUsers(dirs, name)
			if err != nil {
				return fmt.Errorf("can't tell whether sessions use %s: %w (--force removes it anyway)", name, err)
			}
			if n > 0 {
				return fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account first (or use --force)", n, name)
			}
			fmt.Fprintf(out, "Remove account %q and delete %s? [y/N] ", name, acct.Dir)
			line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}
		deleted, err := reg.Remove(name, accountForce)
		if err != nil {
			return err
		}
		if deleted {
			fmt.Fprintf(out, "Removed %s and deleted %s\n", name, acct.Dir)
		} else {
			fmt.Fprintf(out, "Removed %s (left %s in place: loom did not create it)\n", name, acct.Dir)
		}
		return nil
	},
}

func init() {
	accountAddCmd.Flags().BoolVar(&accountNoLogin, "no-login", false, "Create the account without logging it in")
	accountRemoveCmd.Flags().BoolVar(&accountForce, "force", false, "Skip the confirmation and the in-use check")
	AccountCmd.AddCommand(accountAddCmd, accountLoginCmd, accountListCmd, accountUseCmd, accountSyncCmd, accountRemoveCmd)
}
