package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	var reg *account.Registry
	if globalDir, err := config.GetGlobalConfigDir(); err != nil {
		reg = account.Unavailable(err)
	} else {
		reg = account.LoadRegistry(globalDir)
	}
	if err := reg.LoadErr(); err != nil {
		log.For("account").Error("registry.load_failed", "err", err.Error())
		m.errBox.SetError(fmt.Errorf("accounts: %w", err))
	}
	m.adoptAccounts(reg)
	m.warnIfRunningAsAccount()
	if name, ok := account.ActiveCredentialOverride(); ok && m.hasExtraAccounts() {
		log.For("account").Warn("registry.credential_override", "env", name)
	}
	// Fill the strip now rather than when the first probe lands. Its
	// relayout Cmd is not needed: the first WindowSizeMsg is still to come.
	m.refreshAccountViews()
}

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

// warnIfRunningAsAccount says, once at startup, that loom itself runs as an
// extra account: its own $CLAUDE_CONFIG_DIR lies inside the accounts dir
// (it was started from an account session's pane, say). The default
// account's auth, usage and roster, which run with loom's environment,
// then describe that account rather than the main login. Sync already
// refuses to link against it (syncMainDir).
func (m *home) warnIfRunningAsAccount() {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" || m.accounts == nil || m.accounts.Path() == "" {
		return
	}
	rel, err := filepath.Rel(canonicalDir(m.accounts.AccountsDir()), canonicalDir(dir))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	who := "inside the accounts dir"
	if rel != "." {
		who = fmt.Sprintf("as account %q", strings.SplitN(rel, string(filepath.Separator), 2)[0])
	}
	log.For("account").Warn("registry.running_as_account", "claude_config_dir", dir)
	m.errBox.SetError(fmt.Errorf("loom is running %s ($CLAUDE_CONFIG_DIR is %s): \"default\" shows that account's login, usage and sessions, not your main login's", who, dir))
}

// adoptAccounts installs reg as the registry and publishes it, recording
// the file version and state it holds as seen (noteAccountsState).
func (m *home) adoptAccounts(reg *account.Registry) {
	m.accounts = reg
	m.noteAccountsState()
	m.publishAccounts()
}

// accountsFileStamp is one stat of accounts.json: enough to tell that the
// file changed without reading it. The zero value (taken false) matches no
// stat, so a registry never stamped is read on the first check.
type accountsFileStamp struct {
	taken   bool
	exists  bool
	size    int64
	modTime int64
	statErr string
}

func statAccountsFile(path string) accountsFileStamp {
	fi, err := os.Stat(path)
	switch {
	case err == nil:
		return accountsFileStamp{taken: true, exists: true, size: fi.Size(), modTime: fi.ModTime().UnixNano()}
	case os.IsNotExist(err):
		return accountsFileStamp{taken: true}
	default:
		return accountsFileStamp{taken: true, statErr: err.Error()}
	}
}

// accountsSignature is what a reload compares to tell whether anything
// changed: the default, every account's name and dir, and the load error.
func accountsSignature(r *account.Registry) string {
	var b strings.Builder
	b.WriteString(r.Default())
	for _, a := range r.Accounts {
		b.WriteString("\x00" + a.Name + "=" + a.Dir)
	}
	if err := r.LoadErr(); err != nil {
		b.WriteString("\x00error=" + err.Error())
	}
	return b.String()
}

// noteAccountsState records accounts.json's current version and the
// registry's state as seen: after a load, and after this process wrote the
// file itself, so neither reads as a change on the next check.
func (m *home) noteAccountsState() {
	if p := m.accounts.Path(); p != "" {
		m.accountsStamp = statAccountsFile(p)
	}
	m.accountsSeen = accountsSignature(m.accounts)
}

// maybeReloadAccounts is the health tick's check for a change another loom
// or a `loom account` run made to accounts.json: one stat, and a reload
// only when the file's size or modification time moved. A registry with no
// file (Unavailable) does nothing.
func (m *home) maybeReloadAccounts() tea.Cmd {
	if m.accounts == nil || m.accounts.Path() == "" {
		return nil
	}
	if statAccountsFile(m.accounts.Path()) == m.accountsStamp {
		return nil
	}
	return m.reloadAccounts()
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
// account` command may have changed, and handles any change
// (accountsChanged). The health tick calls it when the file changed
// (maybeReloadAccounts); the user-paced actions that read the registry —
// Launch Options, Settings, account requests — call it first regardless.
func (m *home) reloadAccounts() tea.Cmd {
	if m.accounts == nil {
		return nil
	}
	prevErr := m.accounts.LoadErr()
	if p := m.accounts.Path(); p != "" {
		// Stat before reading: a write landing in between then reads as a
		// change on the next check instead of being missed.
		m.accountsStamp = statAccountsFile(p)
	}
	_ = m.accounts.Reload() // a failure is latched in LoadErr
	return m.accountsChanged(prevErr)
}

// accountsChanged handles a reload. When the registry holds what it did
// last time it does nothing; otherwise it republishes, refreshes the views
// and logs the change once, and shows a load error that has just appeared
// (prevErr is the error before the reload) once, not on every reload that
// still fails.
func (m *home) accountsChanged(prevErr error) tea.Cmd {
	sig := accountsSignature(m.accounts)
	if sig == m.accountsSeen {
		return nil
	}
	m.accountsSeen = sig
	m.publishAccounts()
	cmds := []tea.Cmd{m.refreshAccountViews()}
	if err := m.accounts.LoadErr(); err != nil {
		log.For("account").Warn("registry.reload_failed", "err", err.Error())
		if prevErr == nil {
			cmds = append(cmds, m.handleError(fmt.Errorf("accounts: %w", err)))
		}
	} else {
		log.For("account").Info("registry.changed", "accounts", strings.Join(m.accounts.Names(), ","), "default", m.accounts.Default())
	}
	// An account another terminal added has no auth read yet: Unknown means
	// no remote control, no identity and no "logged out" until it is.
	for _, a := range m.accounts.Accounts {
		if _, ok := m.accountAuth[a.Name]; !ok {
			cmds = append(cmds, m.requestAccountsRefresh(false))
			break
		}
	}
	return tea.Batch(cmds...)
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
	}
	if lo := m.launchOptionsOverlay(); lo != nil && lo.AccountsShown() && m.accountsLoaded() {
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
// dir, when that dir is safe to link from (syncMainDir), and re-reads its
// auth (and the default account's too when withDefault). Its inputs are
// copied here; the Cmd touches no model state.
func (m *home) accountsRefreshCmd(withDefault bool) tea.Cmd {
	var accts []account.Account
	if m.accounts != nil {
		accts = append(accts, m.accounts.Accounts...)
	}
	if len(accts) == 0 && !withDefault {
		return nil
	}
	program, syncDir := m.claudeProgram(), m.syncMainDir(m.mainConfigDir())
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
			if syncDir != "" {
				if rep, err := account.Sync(a.Dir, syncDir); err != nil {
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

// requestAccountsRefresh asks for an accounts refresh (accountsRefreshCmd):
// now when none is in flight, else once more when the running one lands.
// withDefault also rereads the default account's auth.
func (m *home) requestAccountsRefresh(withDefault bool) tea.Cmd {
	if withDefault {
		m.refreshDefaultAuth = true
	}
	m.gate(gateAccountsRefresh).request()
	return m.maybeAccountsRefresh()
}

// maybeAccountsRefresh dispatches an accounts refresh unless one is in
// flight. It rereads the default account too when asked to, or when its
// identity is unknown while extra accounts exist: startup skips reading it
// when remote control is off and no extra account was registered yet, yet
// the accounts link to the config dir it names and the views show it.
func (m *home) maybeAccountsRefresh() tea.Cmd {
	return m.dispatchGated(gateAccountsRefresh, time.Now(), func() tea.Cmd {
		withDefault := m.refreshDefaultAuth || (m.hasExtraAccounts() && m.rcAuth.Identity.ConfigDir == "")
		cmd := m.accountsRefreshCmd(withDefault)
		if cmd != nil {
			m.refreshDefaultAuth = false
		}
		return cmd
	})
}

// syncMainDir is mainDir when accounts may be linked against it, else ""
// (no Sync). account.ValidateMainDir refuses a dir holding the accounts
// tree, which would link it into every account, and one inside it, as a
// nested loom running as an account would report. The refusal is logged
// once per reason, not on every refresh.
func (m *home) syncMainDir(mainDir string) string {
	if mainDir == "" || m.accounts == nil {
		return ""
	}
	if err := account.ValidateMainDir(mainDir, m.accounts.AccountsDir()); err != nil {
		if msg := err.Error(); msg != m.syncRefusalLogged {
			m.syncRefusalLogged = msg
			log.For("account").Warn("sync.main_dir_refused", "main_dir", mainDir, "err", msg)
		}
		return ""
	}
	m.syncRefusalLogged = ""
	return mainDir
}

// extraAccountAuth points an extra account's blocked remote-control reason
// at the login that fixes it: the CLI's reason says `claude auth login`,
// which logs in the default account instead. The cause is read from the
// identity, not the reason's wording. A block by a credential in loom's
// environment (override) keeps its reason, since no login changes it.
func extraAccountAuth(name string, a session.RemoteControlAuth, override bool) session.RemoteControlAuth {
	if !a.Blocked() || override {
		return a
	}
	fix := fmt.Sprintf("Run `loom account login %s`, or Settings → Accounts → l.", name)
	switch {
	case !a.Identity.LoggedIn:
		a.Reason = "not logged in to Claude. " + fix
	case a.Identity.AuthMethod != "claude.ai":
		a.Reason = "authenticated with a non-claude.ai account; remote control needs a claude.ai login. " + fix
	}
	return a
}

// handleAccountsRefreshed stores a refresh's results and redraws the views.
func (m *home) handleAccountsRefreshed(msg accountsRefreshedMsg) tea.Cmd {
	m.ensureAccountMaps()
	if msg.defaultAuth != nil {
		m.rcAuth = *msg.defaultAuth
	}
	_, override := account.ActiveCredentialOverride()
	for name, a := range msg.auth {
		m.accountAuth[name] = extraAccountAuth(name, a, override)
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
// first. Default alone hides the row unless a notice shows it.
func (m *home) accountChoices(statuses []ui.AccountStatus) []overlay.AccountChoice {
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
// first (reloadAccounts). A Claude launch gets
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
	reloaded := m.reloadAccounts()
	claude := session.IsClaudeProgram(program)
	notice := ""
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
			removed := opts.Account
			opts.Account = m.accounts.Default()
			notice = removedAccountNotice(removed, opts.Account)
		}
	}
	auth := m.rcAuthFor(opts.Account)
	lo := overlay.NewSessionLaunchOptions(opts, auth.Blocked(), auth.Reason)
	// Otherwise the row stays hidden: a registry that failed to load lists
	// default alone, and SetAccounts would move the kept account onto it.
	if claude && (m.hasExtraAccounts() || notice != "") {
		lo.SetAccountNotice(notice)
		lo.SetAccounts(m.accountChoices(m.accountStatuses()))
	}
	return lo, reloaded
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
		id := m.rcAuthFor(s.Name).Identity
		row := overlay.AccountRow{Name: s.Name, Email: id.Email, Plan: id.Plan, Usage: ui.AccountUsageText(s, now), IsDefault: s.IsDefault}
		if rep, ok := m.accountSync[s.Name]; ok && len(rep.Diverged) > 0 {
			row.Warning = "not shared: " + strings.Join(rep.Diverged, ", ")
		}
		rows = append(rows, row)
	}
	return rows
}

// accountUsers counts the sessions on acct: every loaded slot's live
// instances, plus the stored records of every config dir whose sessions
// aren't all loaded. A slot's own state.json holds the same sessions as its
// live list, so it is skipped — unless the slot's storage failed to load or
// keeps records it could not load, which only the file then shows (an
// overcount there beats missing a session).
func (m *home) accountUsers(acct string) (int, error) {
	n := 0
	covered := map[string]bool{}
	for _, s := range m.openSlots() {
		if s.list != nil {
			for _, inst := range s.list.GetInstances() {
				if inst.Account() == acct {
					n++
				}
			}
		}
		if dir := slotStateDir(s); dir != "" && s.storage != nil &&
			!s.storage.WritesRefused() && len(s.storage.PreservedTitles()) == 0 {
			covered[canonicalDir(dir)] = true
		}
	}
	dirs, err := account.KnownStateDirs()
	if err != nil {
		return n, err
	}
	var uncovered []string
	for _, d := range dirs {
		if !covered[canonicalDir(d)] {
			uncovered = append(uncovered, d)
		}
	}
	stored, err := account.CountUsers(uncovered, acct)
	return n + stored, err
}

// slotStateDir is the config dir holding s's state.json: its context's,
// the default one for an empty context dir, "" when s has no context.
func slotStateDir(s *workspaceSlot) string {
	if s.wsCtx == nil {
		return ""
	}
	if s.wsCtx.ConfigDir != "" {
		return s.wsCtx.ConfigDir
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return ""
	}
	return dir
}

// canonicalDir resolves dir's symlinks when it can, so two spellings of one
// directory compare equal.
func canonicalDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

// afterAccountsChanged republishes the registry after this process wrote
// it, records the written state as seen, and refreshes every view.
func (m *home) afterAccountsChanged() tea.Cmd {
	m.noteAccountsState()
	m.publishAccounts()
	return tea.Batch(m.refreshAccountViews(), m.requestUsageProbe())
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
	program := m.claudeProgram()
	if program == "" {
		program = "claude"
	}
	env, err := m.accounts.Env(acct)
	if err != nil {
		return m.handleError(err)
	}
	if a, ok := m.accounts.Get(acct); ok {
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
// registry as it is on disk now.
func (m *home) handleAccountRequest(req overlay.AccountRequest) tea.Cmd {
	if m.accounts == nil {
		return m.handleError(errors.New("the account registry is unavailable"))
	}
	return tea.Batch(m.reloadAccounts(), m.carryOutAccountRequest(req))
}

func (m *home) carryOutAccountRequest(req overlay.AccountRequest) tea.Cmd {
	m.ensureAccountMaps()
	switch req.Kind {
	case overlay.AccountRequestAdd:
		acct, rep, err := m.accounts.Create(req.Name, m.mainConfigDir())
		if err != nil {
			return m.handleError(err)
		}
		m.accountSync[acct.Name] = rep
		return tea.Batch(m.afterAccountsChanged(), m.accountLoginCmd(acct.Name))
	case overlay.AccountRequestLogin:
		return m.accountLoginCmd(req.Name)
	case overlay.AccountRequestSetDefault:
		if err := m.accounts.SetDefault(req.Name); err != nil {
			return m.handleError(err)
		}
		return m.afterAccountsChanged()
	case overlay.AccountRequestRemove:
		n, err := m.accountUsers(req.Name)
		if err != nil {
			return m.handleError(fmt.Errorf("can't tell whether sessions use %s, so it was kept: %w", req.Name, err))
		}
		if n > 0 {
			return m.handleError(fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account (R) first", n, req.Name))
		}
		// Never forced from here: an account dir holding real, unshared
		// entries is kept, and the toast names them and the CLI command
		// that can force it (Remove's own text offers a --force this
		// screen doesn't have).
		if _, err := m.accounts.Remove(req.Name, false); err != nil {
			var unshared *account.UnsharedError
			if errors.As(err, &unshared) {
				err = fmt.Errorf("account %q holds files that are not shared with your main config: %s; to remove it anyway run `loom account remove --force %s`",
					unshared.Name, strings.Join(unshared.Entries, ", "), unshared.Name)
			}
			return m.handleError(err)
		}
		delete(m.accountAuth, req.Name)
		delete(m.accountSync, req.Name)
		delete(m.usage, req.Name)
		return m.afterAccountsChanged()
	}
	return nil
}
