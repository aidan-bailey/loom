package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/agent"

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
	if p := config.LoadConfigFromGlobal().GetProgram(); isClaudeProgram(p) {
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

// validMainDir rejects a main config dir before add or sync link anything
// against it: accountMainDir can return "" (an unreachable or logged-out
// `claude auth status`), and account.ValidateMainDir catches the same
// unsafe cases Create would otherwise only reject after Sync had already
// started populating a new account's dir.
func validMainDir(main string, reg *account.Registry) error {
	if main == "" {
		return fmt.Errorf("cannot locate your main Claude config dir")
	}
	return account.ValidateMainDir(main, reg.AccountsDir())
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
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		if err := validMainDir(main, reg); err != nil {
			return err
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
		if err := loginAndReport(out, program, acct.Name, account.EnvFor(acct.Dir)); err != nil {
			fmt.Fprintf(out, "%s was created; finish with: loom account login %s\n", acct.Name, acct.Name)
			return err
		}
		return nil
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
		name := args[0]
		targetsDefault := name == "" || name == account.DefaultName
		if err := guardAgainstAccountEnv(reg, targetsDefault); err != nil {
			return err
		}
		env, err := reg.Env(name)
		if err != nil {
			return err
		}
		if !targetsDefault {
			if a, ok := reg.Get(name); ok {
				if _, statErr := os.Stat(a.Dir); statErr != nil {
					if os.IsNotExist(statErr) {
						return fmt.Errorf("account %q's config dir %s is missing; run: loom account remove %s, then: loom account add %s",
							name, a.Dir, name, name)
					}
					return fmt.Errorf("account %q: checking its config dir: %w", name, statErr)
				}
			}
		}
		return loginAndReport(cmd.OutOrStdout(), claudeProgram(), name, env)
	},
}

// accountRow is one line of `list`'s table, filled by a probeAccountRow
// goroutine and printed in Names() order once every row is ready.
type accountRow struct {
	name string
	mark string
	id   account.Identity
	u    account.Usage
	note string
}

// probeAccountRow runs one account's AuthStatus and (when logged in)
// ProbeUsage. Safe to run concurrently across accounts: reg is only read
// (Get/Env/Default), never written, for the lifetime of the list command,
// and accountExec's fakes in tests are themselves read-only.
func probeAccountRow(program string, reg *account.Registry, main, name string) accountRow {
	row := accountRow{name: name}
	if name == reg.Default() {
		row.mark = "*"
	}
	env, _ := reg.Env(name)
	id, err := account.AuthStatus(program, env, accountExec)
	row.id = id
	if err != nil {
		row.note = err.Error()
		return row
	}
	if !id.LoggedIn {
		row.note = "logged out"
		return row
	}
	cwd := main
	if a, ok := reg.Get(name); ok {
		cwd = a.Dir
	} else if name == account.DefaultName && main == "" {
		// The default account's usage probe needs somewhere to run that
		// isn't an arbitrary directory (ProbeUsage's cwd becomes a
		// project entry in Claude's own config) — without a known main
		// dir there is nowhere safe to point it, so skip the probe
		// rather than running it in whatever directory `loom account
		// list` happens to be invoked from.
		row.note = "no main config dir found"
		return row
	}
	u, err := account.ProbeUsage(program, env, cwd, accountExec)
	if err != nil {
		row.note = err.Error()
		return row
	}
	row.u = u
	if !u.Available {
		row.note = "no plan limits"
	}
	return row
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
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		program := claudeProgram()
		main := accountMainDir(program)
		names := reg.Names()
		rows := make([]accountRow, len(names))
		var wg sync.WaitGroup
		for i, name := range names {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				rows[i] = probeAccountRow(program, reg, main, name)
			}(i, name)
		}
		wg.Wait()

		now := time.Now()
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tEMAIL\tPLAN\t5H\t7D\tNOTE")
		for _, row := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.mark, row.name, dash(row.id.Email), dash(row.id.Plan),
				dash(row.u.FiveHour.Text(now)), dash(row.u.SevenDay.Text(now)), row.note)
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
		if err := guardAgainstAccountEnv(reg, true); err != nil {
			return err
		}
		main := accountMainDir(claudeProgram())
		if err := validMainDir(main, reg); err != nil {
			return err
		}
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

// accountUserCount counts the stored sessions using name across every
// config dir loom knows about (account.KnownStateDirs).
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
	RunE: func(cmd *cobra.Command, args []string) error {
		out, name := cmd.OutOrStdout(), args[0]
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
			fmt.Fprintf(out, "Removed %s and deleted %s\n", name, dir)
		} else {
			fmt.Fprintf(out, "Removed %s (left %s in place: loom did not create it)\n", name, dir)
		}
		return nil
	},
}

func init() {
	accountAddCmd.Flags().BoolVar(&accountNoLogin, "no-login", false, "Create the account without logging it in")
	accountRemoveCmd.Flags().BoolVar(&accountForce, "force", false, "Override the in-use and unshared-files refusals (reports what it overrode)")
	accountRemoveCmd.Flags().BoolVarP(&accountYes, "yes", "y", false, "Skip the confirmation prompt")
	AccountCmd.AddCommand(accountAddCmd, accountLoginCmd, accountListCmd, accountUseCmd, accountSyncCmd, accountRemoveCmd)
}

// isClaudeProgram reports whether program launches Claude Code, through the
// same adapter registry session.IsClaudeProgram uses. cmd must not import
// session: session/tmux's tests import cmd/cmd_test, which imports cmd, and
// session imports session/tmux, so that would be an import cycle.
func isClaudeProgram(program string) bool {
	return agent.DefaultRegistry().Lookup(program).Name() == "claude"
}
