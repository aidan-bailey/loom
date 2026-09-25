package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
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
			m := newTestHome(t)
			withAccounts(t, m, "max-2")
			acct, _ := m.accounts.Get("max-2")
			tc.id.ConfigDir = acct.Dir

			m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": {
				State: session.RemoteControlAuthBlocked, Reason: "… Run `claude auth login`.", Identity: tc.id,
			}}})

			reason := m.rcAuthFor("max-2").Reason
			assert.Contains(t, reason, "loom account login max-2")
			assert.Contains(t, reason, "Settings → Accounts → l")
			assert.NotContains(t, reason, "claude auth login")
			assert.True(t, m.rcAuthFor("max-2").Blocked(), "still blocked")
		})
	}
}

func TestAccountsRefreshed_TheDefaultAccountsHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	main := withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in to Claude — run `claude auth login`.",
		Identity: account.Identity{ConfigDir: main}}

	m.Update(accountsRefreshedMsg{defaultAuth: &def})

	assert.Equal(t, def.Reason, m.rcAuth.Reason)
}

// TestAccountsRefreshed_AnOverridesHintIsUnchanged: with a credential in
// loom's environment, logging the account in changes nothing; the hint
// about the variable stands.
func TestAccountsRefreshed_AnOverridesHintIsUnchanged(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	reason := "ANTHROPIC_API_KEY is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`."

	m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": {
		State: session.RemoteControlAuthBlocked, Reason: reason, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"},
	}}})

	assert.Equal(t, reason, m.rcAuthFor("max-2").Reason)
}

func TestAccountsRefreshed_AnOKAuthIsUntouched(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	ok := session.RemoteControlAuth{State: session.RemoteControlAuthOK, Identity: account.Identity{LoggedIn: true, AuthMethod: "claude.ai"}}

	m.Update(accountsRefreshedMsg{auth: map[string]session.RemoteControlAuth{"max-2": ok}})

	require.True(t, m.rcAuthFor("max-2").OK())
	assert.Empty(t, m.rcAuthFor("max-2").Reason)
}

// globalAccounts registers names in a registry at a fresh LOOM_GLOBAL_DIR,
// where initAccounts finds it, and undoes initAccounts' publication at
// cleanup. Returns the registry.
func globalAccounts(t *testing.T, names ...string) *account.Registry {
	t.Helper()
	global := t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	reg := account.LoadRegistry(global)
	main := t.TempDir()
	for _, n := range names {
		_, _, err := reg.Create(n, main)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		session.SetAccountDirs(nil, nil)
		ui.SetShowAccounts(false)
	})
	return reg
}

// TestInitAccounts_WarnsWhenLoomRunsAsAnAccount: started from an account's
// pane, loom's own CLAUDE_CONFIG_DIR is that account's, so "default"
// describes it rather than the main login.
func TestInitAccounts_WarnsWhenLoomRunsAsAnAccount(t *testing.T) {
	noCredentialOverride(t)
	reg := globalAccounts(t, "max-2")
	acct, _ := reg.Get("max-2")
	t.Setenv("CLAUDE_CONFIG_DIR", acct.Dir)
	m := newTestHome(t)

	m.initAccounts()

	toast := m.errBox.String()
	assert.Contains(t, toast, `account "max-2"`)
	assert.Contains(t, toast, "CLAUDE_CONFIG_DIR")

	m.errBox.Clear()
	require.NoError(t, reg.SetDefault("max-2"))
	m.maybeReloadAccounts()
	assert.NotContains(t, m.errBox.String(), "CLAUDE_CONFIG_DIR", "said once, at startup")
}

func TestInitAccounts_NoRunningAsWarningFromTheMainDir(t *testing.T) {
	noCredentialOverride(t)
	globalAccounts(t, "max-2")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	m := newTestHome(t)

	m.initAccounts()

	assert.NotContains(t, m.errBox.String(), "CLAUDE_CONFIG_DIR")
}

// TestAccountsRefresh_RunningAsAnAccountSkipsSync: with no identity read,
// the main dir falls back to $CLAUDE_CONFIG_DIR, here an account's own;
// linking against it would spread that account into its siblings.
func TestAccountsRefresh_RunningAsAnAccountSkipsSync(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	withAccounts(t, m, "max-2", "max-3")
	self, _ := m.accounts.Get("max-3")
	require.NoError(t, os.WriteFile(filepath.Join(self.Dir, "settings.json"), []byte("{}"), 0o644))
	t.Setenv("CLAUDE_CONFIG_DIR", self.Dir)
	m.rcAuth = session.RemoteControlAuth{}

	msg, ok := m.accountsRefreshCmd(false)().(accountsRefreshedMsg)
	require.True(t, ok)

	assert.Empty(t, msg.sync, "nothing synced")
	// Create already linked "projects" from the real main dir; the
	// running account's own settings.json must not follow it.
	other, _ := m.accounts.Get("max-2")
	_, err := os.Lstat(filepath.Join(other.Dir, "settings.json"))
	assert.True(t, os.IsNotExist(err), "an account is never linked into its sibling")
}
