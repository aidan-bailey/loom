package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
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
func (m *home) refreshAccountViews() tea.Cmd {
	statuses := m.accountStatuses()
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
