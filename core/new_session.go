package core

import (
	"errors"
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/launch"
	runewidth "github.com/mattn/go-runewidth"
)

// A new session's rules, for every client that creates one: the TUI's
// creation flows and `loom session new` check a workspace with these
// before they send a Create, and launch on the account
// ChooseLaunchAccount picks. One source keeps the two from drifting.

// InstanceLimit caps the rows a workspace holds (its terminal and
// recoverable rows count) before a client creates another. It is a soft
// guardrail: tmux server memory and git worktree overhead are the real
// bound, so raise it deliberately.
const InstanceLimit = 10

// MaxTitleWidth is the widest title, in terminal cells, a new session
// takes.
const MaxTitleWidth = 32

// CheckRoom refuses a new session in a workspace that already holds rows
// rows (InstanceLimit).
func CheckRoom(rows int) error {
	if rows >= InstanceLimit {
		return fmt.Errorf("you can't create more than %d instances", InstanceLimit)
	}
	return nil
}

// CheckWritable refuses a new session in ws while its storage refuses
// writes (WorkspaceView.WritesRefused): the session could never be saved.
func CheckWritable(ws WorkspaceView) error {
	if ws.WritesRefused {
		return errors.New("new sessions can't be created here: this workspace's saved sessions could not be read, so nothing can be saved (see serve.log)")
	}
	return nil
}

// CheckNewTitle refuses a title a new session in ws can't take: empty,
// wider than MaxTitleWidth cells, or preserved (CheckPreservedTitle).
func CheckNewTitle(title string, ws WorkspaceView) error {
	switch {
	case title == "":
		return errors.New("title cannot be empty")
	case runewidth.StringWidth(title) > MaxTitleWidth:
		return fmt.Errorf("title cannot be longer than %d characters", MaxTitleWidth)
	}
	return CheckPreservedTitle(title, ws)
}

// CheckPreservedTitle refuses a title that belongs to a record ws's
// storage preserves on disk but could not load (a reconcile failure, or a
// newer loom's record after a downgrade). Storage never dedupes against
// such records, so creating the session would persist a second record
// under the title, and the two would share a tmux session name.
func CheckPreservedTitle(title string, ws WorkspaceView) error {
	if slices.Contains(ws.PreservedTitles, title) {
		return fmt.Errorf("title %q belongs to a saved session this version of loom could not load; choose another", title)
	}
	return nil
}

// ChooseLaunchAccount is opts with the account a launch of program runs
// on, as the Session Launch Options modal pre-fills it, and the notice to
// show when it moved the launch off a removed account ("" otherwise). The
// caller reloads the accounts first (Core.ReloadAccounts).
//
// A Claude launch with no account chosen, or with one no longer
// registered, takes the registry's default, so R on a session whose
// account was removed can't relaunch as it again; the notice names the
// switch (RemovedAccountNotice). A registry that failed to load keeps a
// named account: it lists no accounts, and rewriting the account would
// move the session to a subscription the user never saw chosen; kept, its
// launch fails closed (session.RegistryLoadError). Any other program, or
// a model with no account registry, records no account.
func ChooseLaunchAccount(c Core, opts launch.Options, program string) (launch.Options, string) {
	names := c.AccountNames()
	switch {
	case !session.IsClaudeProgram(program) || !names.Present:
		opts.Account = ""
	case opts.Account == "":
		opts.Account = names.Default
	case opts.Account == account.DefaultName || !c.AccountsLoaded():
		// Kept: the default, or a registry that can't tell whether the
		// account still exists.
	default:
		if _, ok := c.Account(opts.Account); !ok {
			removed := opts.Account
			opts.Account = names.Default
			return opts, RemovedAccountNotice(removed, opts.Account)
		}
	}
	return opts, ""
}

// RemovedAccountNotice says a launch whose account was removed runs on
// another one, which the user sees before confirming.
func RemovedAccountNotice(removed, now string) string {
	return fmt.Sprintf("%s was removed — this session will run on %s", removed, now)
}
