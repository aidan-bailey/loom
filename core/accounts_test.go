package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noCredentialOverride clears every credential env var that overrides the
// accounts, so a developer's own environment can't leak into a test.
func noCredentialOverride(t *testing.T) {
	t.Helper()
	for _, v := range account.CredentialOverrides {
		t.Setenv(v, "")
	}
}

// runRefresh runs the dispatched accounts refresh and returns its result.
// The program must not resolve to a real binary.
func runRefresh(t *testing.T, m *Model) accountsRefreshed {
	t.Helper()
	jobs := m.Drain().Jobs
	require.NotEmpty(t, jobs)
	gr, ok := jobs[len(jobs)-1]().(gatedResult)
	require.True(t, ok, "the refresh is gated")
	require.Equal(t, gateAccountsRefresh, gr.kind)
	msg, ok := gr.result.(accountsRefreshed)
	require.True(t, ok)
	return msg
}

func TestRcAuthFor(t *testing.T) {
	m := NewForTest(Options{})
	m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthOK})
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out"}}

	assert.True(t, m.RCAuthFor("").OK())
	assert.True(t, m.RCAuthFor(account.DefaultName).OK())
	assert.True(t, m.RCAuthFor("max-2").Blocked())
	assert.Equal(t, session.RemoteControlAuthUnknown, m.RCAuthFor("max-3").State,
		"an account whose auth has not been read yet fails closed: no flag, no prompt")
}

func TestAccountsRefreshed_StoresAuthAndSync(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	m.Deliver(accountsRefreshed{
		defaultAuth: &def,
		auth:        map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "x"}},
		sync:        map[string]account.SyncReport{"max-2": {Diverged: []string{"settings.json"}}},
	})

	assert.True(t, m.RCAuth().OK())
	assert.True(t, m.RCAuthFor("max-2").Blocked())
	assert.Equal(t, []string{"settings.json"}, m.accountSync["max-2"].Diverged)
}

func TestAccountsRefresh_AReloadFindingAnAccountWithoutAuthReadsIt(t *testing.T) {
	m := NewForTest(Options{})
	main := withAccounts(t, m)
	_, _, err := otherTerminal(t, m).Create("max-2", main)
	require.NoError(t, err)

	m.maybeReloadAccounts()

	assert.True(t, m.gate(gateAccountsRefresh).inFlight, "a new account's auth is read, not left Unknown")
}

func TestAccountsRefresh_AReloadWithEveryAuthKnownReadsNothing(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthOK}}
	require.NoError(t, otherTerminal(t, m).SetDefault("max-2"))

	m.maybeReloadAccounts()

	assert.Equal(t, "max-2", m.accounts.Default(), "the reload happened")
	assert.False(t, m.gate(gateAccountsRefresh).inFlight)
}

func TestAccountsRefresh_IncludesTheDefaultWhileItsIdentityIsUnknown(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // account.MainDir's fallback
	m := NewForTest(Options{Program: "/nonexistent/loom-test/claude"})
	main := withAccounts(t, m, "max-2")

	m.RequestAccountsRefresh(false)
	msg := runRefresh(t, m)
	assert.NotNil(t, msg.defaultAuth, "an RC-off startup never read the default account")

	m.Deliver(gatedResult{kind: gateAccountsRefresh, result: msg})
	editRCAuth(m, func(a *session.RemoteControlAuth) { a.Identity.ConfigDir = main })
	m.RequestAccountsRefresh(false)
	msg = runRefresh(t, m)
	assert.Nil(t, msg.defaultAuth, "known: not reread")
}

func TestRequestAccountsRefresh_DoesNotStack(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")

	m.RequestAccountsRefresh(false)
	require.Len(t, m.Drain().Jobs, 1)
	m.RequestAccountsRefresh(false)
	assert.Empty(t, m.Drain().Jobs, "one refresh in flight at a time")
	assert.True(t, m.gate(gateAccountsRefresh).pending, "the second runs once the first lands")
}

func TestUsageResult_LosingAccessRereadsTheAuth(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	m.ensureAccountMaps()
	m.usage["max-2"] = accountUsage{last: account.Usage{Available: true, At: time.Now().Add(-time.Minute)}}

	m.Deliver(gatedResult{kind: gateUsage, result: usageResult{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).inFlight, "an expired login can then show as logged out")
}

func TestUsageResult_AFirstProbeWithoutAccessWhileLoggedInRereadsTheAuth(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: true}}}

	m.Deliver(gatedResult{kind: gateUsage, result: usageResult{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).inFlight)
}

func TestUsageResult_StillNoAccessRereadsNothing(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: true}}}
	m.ensureAccountMaps()
	m.usage["max-2"] = accountUsage{last: account.Usage{At: time.Now().Add(-time.Minute)}}

	m.Deliver(gatedResult{kind: gateUsage, result: usageResult{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.False(t, m.gate(gateAccountsRefresh).inFlight, "an API-key account is n/a every round; one reread is enough")
}

func TestUsageResult_TheDefaultLosingAccessRereadsTheDefault(t *testing.T) {
	m := NewForTest(Options{})
	main := withAccounts(t, m, "max-2")
	editRCAuth(m, func(a *session.RemoteControlAuth) { a.Identity = account.Identity{ConfigDir: main, LoggedIn: true} })
	m.ensureAccountMaps()
	m.usage[account.DefaultName] = accountUsage{last: account.Usage{Available: true, At: time.Now().Add(-time.Minute)}}
	m.gate(gateAccountsRefresh).inFlight = true // a refresh already running

	m.Deliver(gatedResult{kind: gateUsage, result: usageResult{results: map[string]account.Usage{account.DefaultName: {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).pending, "queued behind the running one")
	assert.True(t, m.refreshDefaultAuth, "and it covers the default account")
}

// TestAccountsRefresh_SkipsSyncForAnUnsafeMainDir: a main dir that holds
// the accounts tree would link "accounts" itself into every account, and
// one inside it (a nested loom running as an account) would link an
// account into its siblings.
func TestAccountsRefresh_SkipsSyncForAnUnsafeMainDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		main func(m *Model) string
	}{
		{"contains the accounts dir", func(m *Model) string { return filepath.Dir(m.accounts.AccountsDir()) }},
		{"inside the accounts dir", func(m *Model) string {
			a, _ := m.accounts.Get("max-3")
			return a.Dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewForTest(Options{})
			withAccounts(t, m, "max-2", "max-3")
			acct, _ := m.accounts.Get("max-2")
			main := tc.main(m)
			require.NoError(t, os.WriteFile(filepath.Join(main, "settings.json"), []byte("{}"), 0o644))
			editRCAuth(m, func(a *session.RemoteControlAuth) { a.Identity.ConfigDir = main })

			msg, ok := m.accountsRefreshJob(false)().(accountsRefreshed)
			require.True(t, ok)

			assert.NotContains(t, msg.sync, "max-2", "not synced")
			// account.Create's own bootstrap already linked "projects"
			// from the safe main dir withAccounts created it against;
			// what must not appear is settings.json, written directly
			// into the *unsafe* main dir above and only reachable if the
			// refresh's guard failed to skip syncing against it.
			_, statErr := os.Lstat(filepath.Join(acct.Dir, "settings.json"))
			assert.True(t, os.IsNotExist(statErr), "the unsafe main dir must never be linked into the account")
		})
	}
}

func TestAccountsRefresh_SyncsAgainstASafeMainDir(t *testing.T) {
	m := NewForTest(Options{})
	main := withAccounts(t, m, "max-2")
	require.NoError(t, os.WriteFile(filepath.Join(main, "settings.json"), []byte("{}"), 0o644))
	editRCAuth(m, func(a *session.RemoteControlAuth) { a.Identity.ConfigDir = main })

	msg, ok := m.accountsRefreshJob(false)().(accountsRefreshed)
	require.True(t, ok)

	assert.Equal(t, []string{"settings.json"}, msg.sync["max-2"].Linked)
}

func TestMaybeReloadAccounts_AnUnchangedFileIsNotReread(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	m.Drain()
	// In memory only: a reread would put the file's "" back.
	m.accounts.DefaultAccount = "max-2"

	m.maybeReloadAccounts()
	assert.True(t, m.Drain().Empty())
	assert.Equal(t, "max-2", m.accounts.DefaultAccount, "not reread")
}

// TestReloadAccounts_AChangeIsReportedOnce: a change another process made
// reaches the TUI as one AccountsChanged (the next Sync publishes the
// changed account view), and a reload that finds nothing changed as none.
func TestReloadAccounts_AChangeIsReportedOnce(t *testing.T) {
	m := NewForTest(Options{})
	main := withAccounts(t, m)
	m.Sync()
	_, _, err := otherTerminal(t, m).Create("max-2", main)
	require.NoError(t, err)
	accountsChanged := func() int {
		n := 0
		for _, ev := range m.Sync().Events {
			if _, ok := ev.(AccountsChanged); ok {
				n++
			}
		}
		return n
	}

	m.ReloadAccounts()
	assert.Equal(t, 1, accountsChanged())

	m.ReloadAccounts()
	assert.Zero(t, accountsChanged(), "nothing changed")
}

func TestMaybeReloadAccounts_ARegistryWithNoFileDoesNothing(t *testing.T) {
	m := NewForTest(Options{})
	withAccounts(t, m)
	m.Drain()
	orig := errors.New("no home directory")
	m.accounts = account.Unavailable(orig)

	m.maybeReloadAccounts()
	assert.True(t, m.Drain().Empty())
	assert.ErrorIs(t, m.accounts.LoadErr(), orig)
}

// TestUsageProbe_DoesNotReadTheRegistry: the builder runs on every health
// tick that finds the gate due, and returns nil without an extra account,
// which leaves the gate due again next tick; the registry is the tick's
// cheap stat's business, not the builder's.
func TestUsageProbe_DoesNotReadTheRegistry(t *testing.T) {
	m := NewForTest(Options{Program: "claude"})
	withAccounts(t, m, "max-2")
	m.accounts.Accounts = nil // in memory only

	assert.False(t, m.maybeUsageProbe())
	assert.False(t, m.HasExtraAccounts(), "not reread")
}

// TestAccountsRefreshed_AnExtraAccountsBlockedHintNamesItsOwnLogin: `claude
// auth login` logs in the default account; an extra account logs in with
// `loom account login <name>` or from the Accounts screen.
func TestAccountsRefreshed_AnExtraAccountsBlockedHintNamesItsOwnLogin(t *testing.T) {
	noCredentialOverride(t)
	for _, tc := range []struct {
		name string
		id   account.Identity
	}{
		{"not logged in", account.Identity{LoggedIn: false}},
		{"not a claude.ai login", account.Identity{LoggedIn: true, AuthMethod: "console"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewForTest(Options{})
			withAccounts(t, m, "max-2")
			acct, _ := m.accounts.Get("max-2")
			tc.id.ConfigDir = acct.Dir

			m.Deliver(accountsRefreshed{auth: map[string]session.RemoteControlAuth{"max-2": {
				State: session.RemoteControlAuthBlocked, Reason: "… Run `claude auth login`.", Identity: tc.id,
			}}})

			reason := m.RCAuthFor("max-2").Reason
			assert.Contains(t, reason, "loom account login max-2")
			assert.Contains(t, reason, "Settings → Accounts → l")
			assert.NotContains(t, reason, "claude auth login")
			assert.True(t, m.RCAuthFor("max-2").Blocked(), "still blocked")
		})
	}
}

func TestAccountsRefreshed_TheDefaultAccountsHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	m := NewForTest(Options{})
	main := withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in to Claude — run `claude auth login`.",
		Identity: account.Identity{ConfigDir: main}}

	m.Deliver(accountsRefreshed{defaultAuth: &def})

	assert.Equal(t, def.Reason, m.RCAuth().Reason)
}

// TestAccountsRefreshed_AnOverridesHintIsUnchanged: with a credential in
// loom's environment, logging the account in changes nothing; the hint
// about the variable stands.
func TestAccountsRefreshed_AnOverridesHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	reason := "ANTHROPIC_API_KEY is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`."

	m.Deliver(accountsRefreshed{auth: map[string]session.RemoteControlAuth{"max-2": {
		State: session.RemoteControlAuthBlocked, Reason: reason, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"},
	}}})

	assert.Equal(t, reason, m.RCAuthFor("max-2").Reason)
}

func TestAccountsRefreshed_AnOKAuthIsUntouched(t *testing.T) {
	noCredentialOverride(t)
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"}}

	m.Deliver(accountsRefreshed{auth: map[string]session.RemoteControlAuth{"max-2": ok}})

	require.True(t, m.RCAuthFor("max-2").OK())
	assert.Empty(t, m.RCAuthFor("max-2").Reason)
}

// TestAccountsRefresh_RunningAsAnAccountSkipsSync: with no identity read,
// the main dir falls back to $CLAUDE_CONFIG_DIR, here an account's own;
// linking against it would spread that account into its siblings.
func TestAccountsRefresh_RunningAsAnAccountSkipsSync(t *testing.T) {
	noCredentialOverride(t)
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2", "max-3")
	self, _ := m.accounts.Get("max-3")
	require.NoError(t, os.WriteFile(filepath.Join(self.Dir, "settings.json"), []byte("{}"), 0o644))
	t.Setenv("CLAUDE_CONFIG_DIR", self.Dir)
	m.SetRCAuth(session.RemoteControlAuth{})

	msg, ok := m.accountsRefreshJob(false)().(accountsRefreshed)
	require.True(t, ok)

	assert.Empty(t, msg.sync, "nothing synced")
	// Create already linked "projects" from the real main dir; the
	// running account's own settings.json must not follow it.
	other, _ := m.accounts.Get("max-2")
	_, err := os.Lstat(filepath.Join(other.Dir, "settings.json"))
	assert.True(t, os.IsNotExist(err), "an account is never linked into its sibling")
}

// TestAccountUsers_CountsAnUnloadedWorkspacesSessions: a workspace that is
// not open has only its state.json to go by.
func TestAccountUsers_CountsAnUnloadedWorkspacesSessions(t *testing.T) {
	global, repo := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", global)
	require.NoError(t, os.WriteFile(filepath.Join(global, "workspaces.json"),
		[]byte(`{"workspaces":[{"name":"r","path":"`+repo+`"}]}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".loom"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".loom", "state.json"),
		[]byte(`{"instances":[{"title":"x","account":"max-2"},{"title":"y","account":"max-2"}]}`), 0o644))
	m := NewForTest(Options{})
	withAccounts(t, m, "max-2")
	m.SetWorkspacesForTest(NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{ConfigDir: global}}), nil)

	n, err := m.accountUsers("max-2")

	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// TestAccountRequests_RefuseWithoutARegistry: before InitAccounts (or with
// no registry at all) every request says so instead of acting.
func TestAccountRequests_RefuseWithoutARegistry(t *testing.T) {
	m := NewForTest(Options{})
	_, err := m.AddAccount("max-2")
	assert.EqualError(t, err, "the account registry is unavailable")
	assert.EqualError(t, m.SetDefaultAccount("max-2"), "the account registry is unavailable")
	assert.EqualError(t, m.RemoveAccount("max-2"), "the account registry is unavailable")
}

// TestInitAccounts_FillsTheStripOnce: the startup publication tells the
// TUI once (AccountsChanged, at the first Sync), which fills the strip
// when newHome drains; a second event only repeated the same refresh.
func TestInitAccounts_FillsTheStripOnce(t *testing.T) {
	noCredentialOverride(t)
	global := t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, _, err := account.LoadRegistry(global).Create("max-2", t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
	m := NewForTest(Options{})

	m.InitAccounts()

	changed := 0
	for _, ev := range m.Sync().Events {
		if _, ok := ev.(AccountsChanged); ok {
			changed++
		}
	}
	assert.Equal(t, 1, changed)
	assert.True(t, m.HasExtraAccounts(), "the registry was loaded and published")
}
