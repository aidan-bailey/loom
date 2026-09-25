package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
)

// accountUsage is one account's probe state: the last good sample and the
// latest probe's error. Usage is display-only, so a failed probe keeps the
// sample, which the views dim with its age, instead of blanking it.
type accountUsage struct {
	last account.Usage
	err  error
}

// initAccounts loads the account registry and publishes it. Called once
// from newHome, before the startup auth probe.
func (m *home) initAccounts() {
	m.ensureAccountMaps()
	m.accountStrip = ui.NewAccountStrip()
	if globalDir, err := config.GetGlobalConfigDir(); err != nil {
		m.accounts = account.Unavailable(err)
	} else {
		m.accounts = account.LoadRegistry(globalDir)
	}
	if err := m.accounts.LoadErr(); err != nil {
		log.For("account").Error("registry.load_failed", "err", err.Error())
		m.errBox.SetError(fmt.Errorf("accounts: %w", err))
	}
	m.publishAccounts()
}

func (m *home) ensureAccountMaps() {
	if m.accountAuth == nil {
		m.accountAuth = map[string]session.RemoteControlAuth{}
	}
	if m.accountSync == nil {
		m.accountSync = map[string]account.SyncReport{}
	}
	if m.usage == nil {
		m.usage = map[string]accountUsage{}
	}
}

// hasExtraAccounts reports whether an account besides default is
// registered; all account UI and polling is gated on it.
func (m *home) hasExtraAccounts() bool { return m.accounts != nil && m.accounts.HasExtra() }

// accountDirs is the registry's name → config dir map (nil without one).
func (m *home) accountDirs() map[string]string {
	if m.accounts == nil {
		return nil
	}
	return m.accounts.Dirs()
}

// publishAccounts hands the registry to session (launch env) and ui
// (badges). Call after every registry change. Update goroutine only.
func (m *home) publishAccounts() {
	var loadErr error
	if m.accounts != nil {
		loadErr = m.accounts.LoadErr()
	}
	session.SetAccountDirs(m.accountDirs(), loadErr)
	ui.SetShowAccounts(m.hasExtraAccounts())
}

// reloadAccounts re-reads accounts.json, which another loom or a `loom
// account` command may have changed, and republishes it. Called before
// every action that reads the registry: probes, pickers, Settings.
func (m *home) reloadAccounts() {
	if m.accounts == nil {
		return
	}
	if err := m.accounts.Reload(); err != nil {
		log.For("account").Warn("registry.reload_failed", "err", err.Error())
	}
	m.publishAccounts()
}

// rcAuthFor returns acct's remote-control auth: the startup probe for the
// default account, the refreshed one for an extra account, and Unknown —
// fail closed, no flag and no prompt — until that has landed.
func (m *home) rcAuthFor(acct string) session.RemoteControlAuth {
	if acct == "" || acct == account.DefaultName {
		return m.rcAuth
	}
	if a, ok := m.accountAuth[acct]; ok {
		return a
	}
	return session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}
}

// claudeProgram is the Claude CLI loom runs account commands with: the
// configured program when it is Claude, else a live Claude session's, else
// "" (no probes).
func (m *home) claudeProgram() string {
	if session.IsClaudeProgram(m.program) {
		return m.program
	}
	for _, inst := range m.activeInstances() {
		if p := inst.Program(); session.IsClaudeProgram(p) {
			return p
		}
	}
	return ""
}

// mainConfigDir is the default account's config dir, which extra accounts
// link to (account.MainDir over the identity read at startup).
func (m *home) mainConfigDir() string {
	return account.MainDir(m.rcAuth.Identity)
}

// accountLoggedOut reports that `claude auth status` ran for acct and said
// it is logged out. Not having asked yet is not logged out.
func (m *home) accountLoggedOut(acct string) bool {
	id := m.rcAuthFor(acct).Identity
	return id.ConfigDir != "" && !id.LoggedIn
}

// accountStatuses builds the view of every account, default first, for the
// strip, the Launch Options row and the Accounts screen.
func (m *home) accountStatuses() []ui.AccountStatus {
	if m.accounts == nil {
		return nil
	}
	def := m.accounts.Default()
	var out []ui.AccountStatus
	for _, name := range m.accounts.Names() {
		u := m.usage[name]
		out = append(out, ui.AccountStatus{
			Name:      name,
			IsDefault: name == def,
			Usage:     u.last,
			Failing:   u.err != nil,
			LoggedOut: m.accountLoggedOut(name),
		})
	}
	return out
}

// refreshAccountViews pushes the current account state into every view
// that shows it. Returns tea.RequestWindowSize when the strip appeared or
// disappeared, since that changes the content height.
//
// An open Launch Options modal follows only while its Account row shows
// (a Claude launch that was given accounts) and the registry loaded: a
// failed load lists no accounts, and refreshing would hide the row and
// reset the choice to the default account, where keeping the stale one
// makes a named account's launch fail closed instead.
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
	if lo := m.launchOptionsOverlay(); lo != nil && lo.AccountsShown() && m.accountsLoaded() {
		lo.SetAccounts(m.accountChoices(statuses))
	}
	if m.accountStrip == nil {
		return nil
	}
	before := m.accountStrip.Height()
	m.accountStrip.SetAccounts(statuses)
	if m.accountStrip.Height() != before {
		return tea.RequestWindowSize
	}
	return nil
}

// accountsRefreshedMsg carries accountsRefreshCmd's results.
type accountsRefreshedMsg struct {
	// defaultAuth is the default account's re-read auth; nil when the
	// refresh did not cover it.
	defaultAuth *session.RemoteControlAuth
	auth        map[string]session.RemoteControlAuth
	sync        map[string]account.SyncReport
	errs        map[string]error
}

// accountsRefreshCmd re-links every extra account against the main config
// dir and re-reads its auth (and the default account's too when
// withDefault). Its inputs are copied here; the Cmd touches no model state.
func (m *home) accountsRefreshCmd(withDefault bool) tea.Cmd {
	var accts []account.Account
	if m.accounts != nil {
		accts = append(accts, m.accounts.Accounts...)
	}
	if len(accts) == 0 && !withDefault {
		return nil
	}
	program, mainDir := m.claudeProgram(), m.mainConfigDir()
	return func() tea.Msg {
		r := internalexec.Default{}
		msg := accountsRefreshedMsg{
			auth: map[string]session.RemoteControlAuth{},
			sync: map[string]account.SyncReport{},
			errs: map[string]error{},
		}
		if withDefault && program != "" {
			a := session.DetectClaudeRemoteControlAuth(program, r)
			msg.defaultAuth = &a
		}
		for _, a := range accts {
			if mainDir != "" {
				if rep, err := account.Sync(a.Dir, mainDir); err != nil {
					msg.errs[a.Name] = err
				} else {
					msg.sync[a.Name] = rep
				}
			}
			if program != "" {
				msg.auth[a.Name] = session.DetectClaudeRemoteControlAuthEnv(program, account.EnvFor(a.Dir), r)
			}
		}
		return msg
	}
}

// handleAccountsRefreshed stores a refresh's results and redraws the views.
func (m *home) handleAccountsRefreshed(msg accountsRefreshedMsg) tea.Cmd {
	m.ensureAccountMaps()
	if msg.defaultAuth != nil {
		m.rcAuth = *msg.defaultAuth
	}
	for name, a := range msg.auth {
		m.accountAuth[name] = a
	}
	for name, rep := range msg.sync {
		m.accountSync[name] = rep
		if len(rep.Diverged) > 0 {
			log.For("account").Warn("sync.diverged", "account", name, "entries", strings.Join(rep.Diverged, ","))
		}
	}
	for name, err := range msg.errs {
		log.For("account").Warn("sync.failed", "account", name, "err", err.Error())
	}
	return m.refreshAccountViews()
}

// accountsLoaded reports whether the registry exists and loaded, so its
// account list can be trusted to be complete.
func (m *home) accountsLoaded() bool { return m.accounts != nil && m.accounts.LoadErr() == nil }

// accountChoices are the Launch Options Account row's options, default
// first; nil (the row hidden) without an extra account.
func (m *home) accountChoices(statuses []ui.AccountStatus) []overlay.AccountChoice {
	if !m.hasExtraAccounts() {
		return nil
	}
	now := time.Now()
	out := make([]overlay.AccountChoice, 0, len(statuses))
	for _, s := range statuses {
		a := m.rcAuthFor(s.Name)
		out = append(out, overlay.AccountChoice{
			Name:      s.Name,
			Summary:   ui.AccountUsageText(s, now),
			RCBlocked: a.Blocked(),
			RCReason:  a.Reason,
		})
	}
	return out
}

// newLaunchOptionsOverlay builds the Session Launch Options modal for opts,
// launching program. The registry is re-read first. A Claude launch gets
// the Account row when an extra account exists, and an empty or
// unregistered opts.Account becomes the registry default, so R on a
// session whose account was removed can't relaunch as it again. A registry
// that failed to load keeps a named opts.Account: it lists no accounts, so
// the row is hidden, and rewriting the account would move the session to
// another subscription the user never saw chosen; kept, its launch fails
// closed (session.RegistryLoadError). Any other program records no account
// and gets no row.
func (m *home) newLaunchOptionsOverlay(opts overlay.LaunchOptions, program string) *overlay.SessionLaunchOptions {
	m.reloadAccounts()
	claude := session.IsClaudeProgram(program)
	switch {
	case !claude || m.accounts == nil:
		opts.Account = ""
	case opts.Account == "":
		opts.Account = m.accounts.Default()
	case opts.Account == account.DefaultName || !m.accountsLoaded():
		// Kept: the default, or a registry that can't tell whether the
		// account still exists.
	default:
		if _, ok := m.accounts.Get(opts.Account); !ok {
			opts.Account = m.accounts.Default()
		}
	}
	auth := m.rcAuthFor(opts.Account)
	lo := overlay.NewSessionLaunchOptions(opts, auth.Blocked(), auth.Reason)
	// Without choices the row stays hidden; SetAccounts(nil) would also
	// blank the account resolved above.
	if choices := m.accountChoices(m.accountStatuses()); claude && choices != nil {
		lo.SetAccounts(choices)
	}
	return lo
}

// applyChosenLaunch records the chosen launch options on inst: the program
// composed from base with the chosen account's remote-control auth, the env
// toggles, and the account itself.
func (m *home) applyChosenLaunch(inst *session.Instance, opts overlay.LaunchOptions, base string) {
	inst.SetLaunchOptions(applyLaunchOptions(opts, m.rcAuthFor(opts.Account), base, inst.Title), opts.HeadroomProxy, opts.CacheTTL1h)
	inst.SetAccount(opts.Account)
}

// accountOrDefault maps an instance's stored account ("" = default) to the
// name the Account row shows.
func accountOrDefault(name string) string {
	if name == "" {
		return account.DefaultName
	}
	return name
}
