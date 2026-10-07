package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
)

// credentialOverrideWarning is what the strip and the Accounts screen say
// while a credential in loom's environment overrides every account
// (account.ActiveCredentialOverride): every account session runs and bills
// as it, and the usage probes, which inherit loom's environment, show its
// usage under every account's name. "" otherwise.
func credentialOverrideWarning() string {
	if name, ok := account.ActiveCredentialOverride(); ok {
		return "$" + name + " set: all accounts use it"
	}
	return ""
}

// accountsScreenNotice is the Accounts screen's notice: the credential
// override warning while an extra account exists, since with default
// alone there is no account choice for it to void.
func (m *home) accountsScreenNotice() string {
	if !m.core.HasExtraAccounts() {
		return ""
	}
	return credentialOverrideWarning()
}

// accountStatuses builds the view of every account, default first, for the
// strip, the Launch Options row and the Accounts screen.
func (m *home) accountStatuses() []ui.AccountStatus {
	if m.core.Accounts() == nil {
		return nil
	}
	def := m.core.Accounts().Default()
	var out []ui.AccountStatus
	for _, name := range m.core.Accounts().Names() {
		usage, usageErr := m.core.AccountUsage(name)
		out = append(out, ui.AccountStatus{
			Name:      name,
			IsDefault: name == def,
			Usage:     usage,
			Failing:   usageErr != nil,
			LoggedOut: m.core.AccountLoggedOut(name),
		})
	}
	return out
}

// refreshAccountViews pushes the current account state into every view
// that shows it, with the credential override warning read afresh
// (credentialOverrideWarning). Returns tea.RequestWindowSize when the
// strip appeared or disappeared, since that changes the content height.
//
// An open Settings overlay's Accounts rows always follow. An open Launch
// Options modal follows only while its Account row shows
// (a Claude launch that was given accounts) and the registry loaded: a
// failed load lists no accounts, and refreshing would hide the row and
// reset the choice to the default account, where keeping the stale one
// makes a named account's launch fail closed instead. When the modal's
// selected account is gone the selection moves to the first choice, and
// the modal says so (removedAccountNotice).
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
	if so := m.settingsOverlay(); so != nil {
		so.SetAccountRows(m.accountRows(statuses))
		so.SetAccountNotice(m.accountsScreenNotice())
	}
	if lo := m.launchOptionsOverlay(); lo != nil && lo.AccountsShown() && m.core.AccountsLoaded() {
		choices := m.accountChoices(statuses)
		if sel := lo.Options().Account; sel != "" && len(choices) > 0 && !hasAccountChoice(choices, sel) {
			lo.SetAccountNotice(removedAccountNotice(sel, choices[0].Name))
		}
		lo.SetAccounts(choices)
	}
	if m.accountStrip == nil {
		return nil
	}
	before := m.accountStrip.Height()
	m.accountStrip.SetWarning(credentialOverrideWarning())
	m.accountStrip.SetAccounts(statuses)
	if m.accountStrip.Height() != before {
		return tea.RequestWindowSize
	}
	return nil
}

// accountChoices are the Launch Options Account row's options, default
// first. Default alone hides the row unless a notice shows it.
func (m *home) accountChoices(statuses []ui.AccountStatus) []overlay.AccountChoice {
	now := time.Now()
	out := make([]overlay.AccountChoice, 0, len(statuses))
	for _, s := range statuses {
		a := m.core.RCAuthFor(s.Name)
		out = append(out, overlay.AccountChoice{
			Name:      s.Name,
			Summary:   ui.AccountUsageText(s, now),
			RCBlocked: a.Blocked(),
			RCReason:  a.Reason,
		})
	}
	return out
}

func hasAccountChoice(choices []overlay.AccountChoice, name string) bool {
	for _, c := range choices {
		if c.Name == name {
			return true
		}
	}
	return false
}

// removedAccountNotice is the Account row's notice for a session whose
// account was removed: it runs on another one, which the user sees before
// confirming.
func removedAccountNotice(removed, now string) string {
	return fmt.Sprintf("%s was removed — this session will run on %s", removed, now)
}

// newLaunchOptionsOverlay builds the Session Launch Options modal for opts,
// launching program, and the Cmd of re-reading the registry, which it does
// first (core.Model.ReloadAccounts). A Claude launch gets
// the Account row when an extra account exists, and an empty or
// unregistered opts.Account becomes the registry default, so R on a
// session whose account was removed can't relaunch as it again; the row
// then shows, even with default the only choice left, with a notice
// naming the switch (removedAccountNotice). A registry
// that failed to load keeps a named opts.Account: it lists no accounts, so
// the row is hidden, and rewriting the account would move the session to
// another subscription the user never saw chosen; kept, its launch fails
// closed (session.RegistryLoadError). Any other program records no account
// and gets no row.
func (m *home) newLaunchOptionsOverlay(opts overlay.LaunchOptions, program string) (*overlay.SessionLaunchOptions, tea.Cmd) {
	m.core.ReloadAccounts()
	reloaded := m.drainCore()
	claude := session.IsClaudeProgram(program)
	notice := ""
	switch {
	case !claude || m.core.Accounts() == nil:
		opts.Account = ""
	case opts.Account == "":
		opts.Account = m.core.Accounts().Default()
	case opts.Account == account.DefaultName || !m.core.AccountsLoaded():
		// Kept: the default, or a registry that can't tell whether the
		// account still exists.
	default:
		if _, ok := m.core.Account(opts.Account); !ok {
			removed := opts.Account
			opts.Account = m.core.Accounts().Default()
			notice = removedAccountNotice(removed, opts.Account)
		}
	}
	auth := m.core.RCAuthFor(opts.Account)
	lo := overlay.NewSessionLaunchOptions(opts, auth.Blocked(), auth.Reason)
	// Otherwise the row stays hidden: a registry that failed to load lists
	// default alone, and SetAccounts would move the kept account onto it.
	if claude && (m.core.HasExtraAccounts() || notice != "") {
		lo.SetAccountNotice(notice)
		lo.SetAccounts(m.accountChoices(m.accountStatuses()))
	}
	return lo, reloaded
}

// accountOrDefault maps an instance's stored account ("" = default) to the
// name the Account row shows.
func accountOrDefault(name string) string {
	if name == "" {
		return account.DefaultName
	}
	return name
}

// topChromeHeight is the rows above the content: the account strip (when
// shown) plus the workspace tab bar. Every content-height and mouse/cursor
// offset goes through it, so both rows stay accounted for.
func (m *home) topChromeHeight() int {
	h := m.tabBar.Height()
	if m.accountStrip != nil {
		h += m.accountStrip.Height()
	}
	return h
}

// accountRows formats the Accounts screen's rows. A logged-out account
// says so in its usage column (ui.AccountUsageText), so the warning line
// carries only what the row can't show.
func (m *home) accountRows(statuses []ui.AccountStatus) []overlay.AccountRow {
	now := time.Now()
	rows := make([]overlay.AccountRow, 0, len(statuses))
	for _, s := range statuses {
		id := m.core.RCAuthFor(s.Name).Identity
		row := overlay.AccountRow{Name: s.Name, Email: id.Email, Plan: id.Plan, Usage: ui.AccountUsageText(s, now), IsDefault: s.IsDefault}
		if rep, ok := m.core.AccountSync(s.Name); ok && len(rep.Diverged) > 0 {
			row.Warning = "not shared: " + strings.Join(rep.Diverged, ", ")
		}
		rows = append(rows, row)
	}
	return rows
}

// accountLoginDoneMsg is returned when `claude auth login` hands the
// terminal back.
type accountLoginDoneMsg struct {
	name string
	err  error
}

// accountLoginCmd suspends the TUI and runs `claude auth login` as acct in
// the real terminal (it is a browser flow), like $EDITOR in the file
// explorer. A registered account whose config dir is gone is refused, as
// `loom account login` refuses it: claude would recreate the dir bare,
// without the links to the main config that removing and re-adding it
// restores.
func (m *home) accountLoginCmd(acct string) tea.Cmd {
	program := m.core.ClaudeProgram()
	if program == "" {
		program = "claude"
	}
	env, err := m.core.AccountEnv(acct)
	if err != nil {
		return m.handleError(err)
	}
	if a, ok := m.core.Account(acct); ok {
		if _, err := os.Stat(a.Dir); os.IsNotExist(err) {
			return m.handleError(fmt.Errorf("account %q's config dir %s is missing; run `loom account remove %s`, then add it again", acct, a.Dir, acct))
		} else if err != nil {
			return m.handleError(fmt.Errorf("account %q: %w", acct, err))
		}
	}
	return tea.ExecProcess(account.LoginCmd(program, env), func(err error) tea.Msg {
		return accountLoginDoneMsg{name: acct, err: err}
	})
}

// handleAccountRequest carries out one Accounts-screen action, on the
// registry as it is on disk now: each of the model's requests reloads it
// first. The model's events (the reload's, the change's) are applied
// before the request's own error or login, as they were when the reload
// and the change refreshed the views themselves, so an open Accounts
// screen is current when this returns.
func (m *home) handleAccountRequest(req overlay.AccountRequest) tea.Cmd {
	if m.core.Accounts() == nil {
		return m.handleError(errors.New("the account registry is unavailable"))
	}
	login, err := m.carryOutAccountRequest(req)
	cmds := []tea.Cmd{m.drainCore()}
	if err != nil {
		return tea.Batch(append(cmds, m.handleError(err))...)
	}
	if login != "" {
		cmds = append(cmds, m.accountLoginCmd(login))
	}
	return tea.Batch(cmds...)
}

// carryOutAccountRequest asks the model to carry out req. It returns the
// account whose `claude auth login` runs next (an add, a login), or the
// request's error.
func (m *home) carryOutAccountRequest(req overlay.AccountRequest) (login string, err error) {
	switch req.Kind {
	case overlay.AccountRequestAdd:
		return m.core.AddAccount(req.Name)
	case overlay.AccountRequestLogin:
		m.core.ReloadAccounts()
		return req.Name, nil
	case overlay.AccountRequestSetDefault:
		return "", m.core.SetDefaultAccount(req.Name)
	case overlay.AccountRequestRemove:
		return "", m.core.RemoveAccount(req.Name)
	}
	return "", nil
}
