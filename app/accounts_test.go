package app

import (
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAccounts registers extra accounts on m, linked against a throwaway
// main dir, and publishes them; the package-level publication is undone at
// cleanup. Returns the main dir.
func withAccounts(t *testing.T, m *home, names ...string) string {
	t.Helper()
	reg := account.LoadRegistry(t.TempDir())
	main := t.TempDir()
	for _, n := range names {
		_, _, err := reg.Create(n, main)
		require.NoError(t, err)
	}
	if m.accountStrip == nil {
		m.accountStrip = ui.NewAccountStrip()
	}
	m.adoptAccounts(reg)
	t.Cleanup(func() {
		session.SetAccountDirs(nil, nil)
		ui.SetShowAccounts(false)
	})
	return main
}

func TestRcAuthFor(t *testing.T) {
	m := newTestHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out"}}

	assert.True(t, m.rcAuthFor("").OK())
	assert.True(t, m.rcAuthFor(account.DefaultName).OK())
	assert.True(t, m.rcAuthFor("max-2").Blocked())
	assert.Equal(t, session.RemoteControlAuthUnknown, m.rcAuthFor("max-3").State,
		"an account whose auth has not been read yet fails closed: no flag, no prompt")
}

func TestPublishAccounts_BadgesOnlyWithAnExtraAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	assert.False(t, ui.ShowAccounts())
	assert.False(t, m.hasExtraAccounts())

	withAccounts(t, m, "max-2")
	assert.True(t, ui.ShowAccounts())
	assert.True(t, m.hasExtraAccounts())
}

func TestReloadAccounts_SeesAnotherProcessesChanges(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	require.False(t, ui.ShowAccounts())

	// A `loom account add` in another terminal writes the same file.
	other := account.LoadRegistry(filepath.Dir(m.accounts.AccountsDir()))
	_, _, err := other.Create("max-2", main)
	require.NoError(t, err)
	assert.False(t, m.hasExtraAccounts(), "not seen until reloaded")

	m.reloadAccounts()

	_, ok := m.accounts.Get("max-2")
	assert.True(t, ok)
	assert.True(t, ui.ShowAccounts(), "the reload is republished")
}

func TestHandleAccountsRefreshed_StoresAuthAndSync(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	def := session.RemoteControlAuth{State: session.RemoteControlAuthOK}

	m.Update(accountsRefreshedMsg{
		defaultAuth: &def,
		auth:        map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "x"}},
		sync:        map[string]account.SyncReport{"max-2": {Diverged: []string{"settings.json"}}},
	})

	assert.True(t, m.rcAuth.OK())
	assert.True(t, m.rcAuthFor("max-2").Blocked())
	assert.Equal(t, []string{"settings.json"}, m.accountSync["max-2"].Diverged)
}

func TestAccountStatuses_DefaultFirstAndMarked(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.accounts.SetDefault("max-2"))
	m.accountAuth = map[string]session.RemoteControlAuth{
		"max-2": {Identity: account.Identity{ConfigDir: "/acct", LoggedIn: false}},
	}

	st := m.accountStatuses()

	require.Len(t, st, 2)
	assert.Equal(t, account.DefaultName, st[0].Name)
	assert.False(t, st[0].IsDefault)
	assert.False(t, st[0].LoggedOut, "no auth read yet is not logged out")
	assert.Equal(t, "max-2", st[1].Name)
	assert.True(t, st[1].IsDefault)
	assert.True(t, st[1].LoggedOut)
}

func TestRefreshAccountViews_RequestsAResizeWhenTheStripAppears(t *testing.T) {
	m := newTestHome(t)
	m.accountStrip = ui.NewAccountStrip()
	withAccounts(t, m)
	assert.Nil(t, m.refreshAccountViews(), "one account: no strip, no resize")

	withAccounts(t, m, "max-2")
	assert.NotNil(t, m.refreshAccountViews(), "the strip appearing changes the content height")
	assert.Nil(t, m.refreshAccountViews(), "no change, no resize")
}
