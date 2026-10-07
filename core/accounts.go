package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// accountUsage is one account's probe state: the last good sample and the
// latest probe's error. Usage is display-only, so a failed probe keeps the
// sample, which the views dim with its age, instead of blanking it.
type accountUsage struct {
	last account.Usage
	err  error
}

// errNoRegistry is what an account request answers when the registry
// could not be built at all.
var errNoRegistry = errors.New("the account registry is unavailable")

// InitAccounts loads the account registry and publishes it. Called once
// from newHome, before the startup auth probe.
func (m *Model) InitAccounts() {
	m.ensureAccountMaps()
	var reg *account.Registry
	if globalDir, err := config.GetGlobalConfigDir(); err != nil {
		reg = account.Unavailable(err)
	} else {
		reg = account.LoadRegistry(globalDir)
	}
	if err := reg.LoadErr(); err != nil {
		log.For("account").Error("registry.load_failed", "err", err.Error())
		m.notifyErr(fmt.Errorf("accounts: %w", err))
	}
	// Publishing emits AccountsChanged, which fills the strip now rather
	// than when the first probe lands. Its relayout Cmd is not needed: the
	// first WindowSizeMsg is still to come.
	m.adoptAccounts(reg)
	m.warnIfRunningAsAccount()
	if name, ok := account.ActiveCredentialOverride(); ok && m.HasExtraAccounts() {
		log.For("account").Warn("registry.credential_override", "env", name)
	}
}

// warnIfRunningAsAccount says, once at startup, that loom itself runs as an
// extra account: its own $CLAUDE_CONFIG_DIR lies inside the accounts dir
// (it was started from an account session's pane, say). The default
// account's auth, usage and roster, which run with loom's environment,
// then describe that account rather than the main login. Sync already
// refuses to link against it (syncMainDir).
func (m *Model) warnIfRunningAsAccount() {
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
	m.notifyErr(fmt.Errorf("loom is running %s ($CLAUDE_CONFIG_DIR is %s): \"default\" shows that account's login, usage and sessions, not your main login's", who, dir))
}

// adoptAccounts installs reg as the registry and publishes it, recording
// the file version and state it holds as seen (noteAccountsState).
func (m *Model) adoptAccounts(reg *account.Registry) {
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
func (m *Model) noteAccountsState() {
	if p := m.accounts.Path(); p != "" {
		m.accountsStamp = statAccountsFile(p)
	}
	m.accountsSeen = accountsSignature(m.accounts)
}

// maybeReloadAccounts is the health tick's check for a change another loom
// or a `loom account` run made to accounts.json: one stat, and a reload
// only when the file's size or modification time moved. A registry with no
// file (Unavailable) does nothing.
func (m *Model) maybeReloadAccounts() {
	if m.accounts == nil || m.accounts.Path() == "" {
		return
	}
	if statAccountsFile(m.accounts.Path()) == m.accountsStamp {
		return
	}
	m.ReloadAccounts()
}

func (m *Model) ensureAccountMaps() {
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

// HasExtraAccounts reports whether an account besides default is
// registered; all account UI and polling is gated on it.
func (m *Model) HasExtraAccounts() bool { return m.accounts != nil && m.accounts.HasExtra() }

// accountDirs is the registry's name → config dir map (nil without one).
func (m *Model) accountDirs() map[string]string {
	if m.accounts == nil {
		return nil
	}
	return m.accounts.Dirs()
}

// publishAccounts hands the registry to session (launch env) and the TUI
// (badges, through AccountsChanged). Call after every registry change.
// Update goroutine only.
func (m *Model) publishAccounts() {
	var loadErr error
	if m.accounts != nil {
		loadErr = m.accounts.LoadErr()
	}
	session.SetAccountDirs(m.accountDirs(), loadErr)
	m.emit(AccountsChanged{})
}

// ReloadAccounts re-reads accounts.json, which another loom or a `loom
// account` command may have changed, and handles any change
// (accountsChanged). The health tick calls it when the file changed
// (maybeReloadAccounts); the user-paced actions that read the registry —
// Launch Options, Settings, account requests — call it first regardless.
func (m *Model) ReloadAccounts() {
	if m.accounts == nil {
		return
	}
	prevErr := m.accounts.LoadErr()
	if p := m.accounts.Path(); p != "" {
		// Stat before reading: a write landing in between then reads as a
		// change on the next check instead of being missed.
		m.accountsStamp = statAccountsFile(p)
	}
	_ = m.accounts.Reload() // a failure is latched in LoadErr
	m.accountsChanged(prevErr)
}

// accountsChanged handles a reload. When the registry holds what it did
// last time it does nothing; otherwise it republishes, refreshes the views
// and logs the change once, and shows a load error that has just appeared
// (prevErr is the error before the reload) once, not on every reload that
// still fails.
func (m *Model) accountsChanged(prevErr error) {
	sig := accountsSignature(m.accounts)
	if sig == m.accountsSeen {
		return
	}
	m.accountsSeen = sig
	m.publishAccounts()
	if err := m.accounts.LoadErr(); err != nil {
		log.For("account").Warn("registry.reload_failed", "err", err.Error())
		if prevErr == nil {
			m.notifyErr(fmt.Errorf("accounts: %w", err))
		}
	} else {
		log.For("account").Info("registry.changed", "accounts", strings.Join(m.accounts.Names(), ","), "default", m.accounts.Default())
	}
	// An account another terminal added has no auth read yet: Unknown means
	// no remote control, no identity and no "logged out" until it is.
	for _, a := range m.accounts.Accounts {
		if _, ok := m.accountAuth[a.Name]; !ok {
			m.RequestAccountsRefresh(false)
			break
		}
	}
}

// RCAuthFor returns acct's remote-control auth: the startup probe for the
// default account, the refreshed one for an extra account, and Unknown —
// fail closed, no flag and no prompt — until that has landed.
func (m *Model) RCAuthFor(acct string) session.RemoteControlAuth {
	if acct == "" || acct == account.DefaultName {
		return m.rcAuth
	}
	if a, ok := m.accountAuth[acct]; ok {
		return a
	}
	return session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}
}

// ClaudeProgram is the Claude CLI loom runs account commands with: the
// configured program when it is Claude, else a live Claude session's, else
// "" (no probes).
func (m *Model) ClaudeProgram() string {
	if session.IsClaudeProgram(m.program) {
		return m.program
	}
	for _, inst := range m.ActiveInstances() {
		if p := inst.Program(); session.IsClaudeProgram(p) {
			return p
		}
	}
	return ""
}

// MainConfigDir is the default account's config dir, which extra accounts
// link to (account.MainDir over the identity read at startup).
func (m *Model) MainConfigDir() string {
	return account.MainDir(m.rcAuth.Identity)
}

// AccountLoggedOut reports that `claude auth status` ran for acct and said
// it is logged out. Not having asked yet is not logged out.
func (m *Model) AccountLoggedOut(acct string) bool {
	id := m.RCAuthFor(acct).Identity
	return id.ConfigDir != "" && !id.LoggedIn
}

// accountsRefreshed carries accountsRefreshJob's results.
type accountsRefreshed struct {
	// defaultAuth is the default account's re-read auth; nil when the
	// refresh did not cover it.
	defaultAuth *session.RemoteControlAuth
	auth        map[string]session.RemoteControlAuth
	sync        map[string]account.SyncReport
	errs        map[string]error
}

// accountsRefreshJob re-links every extra account against the main config
// dir, when that dir is safe to link from (syncMainDir), and re-reads its
// auth (and the default account's too when withDefault). Its inputs are
// copied here; the Job touches no model state.
func (m *Model) accountsRefreshJob(withDefault bool) Job {
	var accts []account.Account
	if m.accounts != nil {
		accts = append(accts, m.accounts.Accounts...)
	}
	if len(accts) == 0 && !withDefault {
		return nil
	}
	program, syncDir := m.ClaudeProgram(), m.syncMainDir(m.MainConfigDir())
	return func() any {
		r := internalexec.Default{}
		msg := accountsRefreshed{
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

// RequestAccountsRefresh asks for an accounts refresh (accountsRefreshJob):
// now when none is in flight, else once more when the running one lands.
// withDefault also rereads the default account's auth.
func (m *Model) RequestAccountsRefresh(withDefault bool) {
	if withDefault {
		m.refreshDefaultAuth = true
	}
	m.gate(gateAccountsRefresh).request()
	m.maybeAccountsRefresh()
}

// maybeAccountsRefresh dispatches an accounts refresh unless one is in
// flight. It rereads the default account too when asked to, or when its
// identity is unknown while extra accounts exist: startup skips reading it
// when remote control is off and no extra account was registered yet, yet
// the accounts link to the config dir it names and the views show it.
func (m *Model) maybeAccountsRefresh() bool {
	return m.dispatchGated(gateAccountsRefresh, time.Now(), func() Job {
		withDefault := m.refreshDefaultAuth || (m.HasExtraAccounts() && m.rcAuth.Identity.ConfigDir == "")
		job := m.accountsRefreshJob(withDefault)
		if job != nil {
			m.refreshDefaultAuth = false
		}
		return job
	})
}

// syncMainDir is mainDir when accounts may be linked against it, else ""
// (no Sync). account.ValidateMainDir refuses a dir holding the accounts
// tree, which would link it into every account, and one inside it, as a
// nested loom running as an account would report. The refusal is logged
// once per reason, not on every refresh.
func (m *Model) syncMainDir(mainDir string) string {
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

// deliverAccountsRefreshed stores a refresh's results and redraws the views.
func (m *Model) deliverAccountsRefreshed(msg accountsRefreshed) {
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
	m.emit(AccountsChanged{})
}

// AccountsLoaded reports whether the registry exists and loaded, so its
// account list can be trusted to be complete.
func (m *Model) AccountsLoaded() bool { return m.accounts != nil && m.accounts.LoadErr() == nil }

// accountUsers counts the sessions on acct: every loaded workspace's live
// instances, plus the stored records of every config dir whose sessions
// aren't all loaded. A workspace's own state.json holds the same sessions
// as its live list, so it is skipped — unless the workspace's storage
// failed to load or keeps records it could not load, which only the file
// then shows (an overcount there beats missing a session).
func (m *Model) accountUsers(acct string) (int, error) {
	n := 0
	covered := map[string]bool{}
	for _, ws := range m.Loaded() {
		for _, inst := range ws.insts {
			if inst.Account() == acct {
				n++
			}
		}
		if dir := wsStateDir(ws); dir != "" && ws.storage != nil &&
			!ws.storage.WritesRefused() && len(ws.storage.PreservedTitles()) == 0 {
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

// wsStateDir is the config dir holding ws's state.json: its context's,
// the default one for an empty context dir, "" when ws has no context.
func wsStateDir(ws *Workspace) string {
	if ws.ctx == nil {
		return ""
	}
	if ws.ctx.ConfigDir != "" {
		return ws.ctx.ConfigDir
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
func (m *Model) afterAccountsChanged() {
	m.noteAccountsState()
	m.publishAccounts()
	m.RequestUsageProbe()
}

// AddAccount creates account name (linked against the main config dir)
// and records its first sync report. The TUI then runs its login.
func (m *Model) AddAccount(name string) (string, error) {
	if m.accounts == nil {
		return "", errNoRegistry
	}
	m.ReloadAccounts()
	m.ensureAccountMaps()
	acct, rep, err := m.accounts.Create(name, m.MainConfigDir())
	if err != nil {
		return "", err
	}
	m.accountSync[acct.Name] = rep
	m.afterAccountsChanged()
	return acct.Name, nil
}

// SetDefaultAccount makes name the account new sessions preselect.
func (m *Model) SetDefaultAccount(name string) error {
	if m.accounts == nil {
		return errNoRegistry
	}
	m.ReloadAccounts()
	if err := m.accounts.SetDefault(name); err != nil {
		return err
	}
	m.afterAccountsChanged()
	return nil
}

// RemoveAccount removes account name, refusing while a session uses it
// (accountUsers) or while its dir holds unshared files (the error then
// names them and the CLI command that can force it; this path never
// forces).
func (m *Model) RemoveAccount(name string) error {
	if m.accounts == nil {
		return errNoRegistry
	}
	m.ReloadAccounts()
	m.ensureAccountMaps()
	n, err := m.accountUsers(name)
	if err != nil {
		return fmt.Errorf("can't tell whether sessions use %s, so it was kept: %w", name, err)
	}
	if n > 0 {
		return fmt.Errorf("%d session(s) use %s: kill them or relaunch them on another account (R) first", n, name)
	}
	// Never forced from here: an account dir holding real, unshared
	// entries is kept, and the toast names them and the CLI command
	// that can force it (Remove's own text offers a --force this
	// screen doesn't have).
	if _, err := m.accounts.Remove(name, false); err != nil {
		var unshared *account.UnsharedError
		if errors.As(err, &unshared) {
			err = fmt.Errorf("account %q holds files that are not shared with your main config: %s; to remove it anyway run `loom account remove --force %s`",
				unshared.Name, strings.Join(unshared.Entries, ", "), unshared.Name)
		}
		return err
	}
	delete(m.accountAuth, name)
	delete(m.accountSync, name)
	delete(m.usage, name)
	m.afterAccountsChanged()
	return nil
}

// AccountEnv is the environment a command runs with as account name.
func (m *Model) AccountEnv(name string) ([]string, error) {
	if m.accounts == nil {
		return nil, errNoRegistry
	}
	return m.accounts.Env(name)
}

// Account returns the registered account name.
func (m *Model) Account(name string) (account.Account, bool) {
	if m.accounts == nil {
		return account.Account{}, false
	}
	return m.accounts.Get(name)
}

// AccountSync returns name's last link report.
func (m *Model) AccountSync(name string) (account.SyncReport, bool) {
	rep, ok := m.accountSync[name]
	return rep, ok
}

// AccountUsage returns name's latest usage sample and the last probe's
// error (a failed probe keeps the last good sample).
func (m *Model) AccountUsage(name string) (account.Usage, error) {
	u := m.usage[name]
	return u.last, u.err
}

// Accounts is the registry for the TUI's views to read (nil before
// InitAccounts); writes go through the requests above.
func (m *Model) Accounts() *account.Registry { return m.accounts }
