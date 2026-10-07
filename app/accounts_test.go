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
	testModel(m).AdoptAccountsForTest(reg)
	// Publishing shows the badges (ui.SetShowAccounts), as it did before
	// it became an event; the views refresh only when a test asks.
	testModel(m).Drain()
	ui.SetShowAccounts(m.core.HasExtraAccounts())
	t.Cleanup(func() {
		session.SetAccountDirs(nil, nil)
		ui.SetShowAccounts(false)
	})
	return main
}

// TestPublishAccounts_BadgesOnlyWithAnExtraAccount runs production's path
// (the model's AccountsChanged, applied by drainCore), not withAccounts,
// which sets the badges itself.
func TestPublishAccounts_BadgesOnlyWithAnExtraAccount(t *testing.T) {
	m := newTestHome(t)
	m.accountStrip = ui.NewAccountStrip()
	t.Cleanup(func() {
		session.SetAccountDirs(nil, nil)
		ui.SetShowAccounts(false)
	})
	reg := account.LoadRegistry(t.TempDir())

	ui.SetShowAccounts(true) // stale: the event must clear it
	testModel(m).AdoptAccountsForTest(reg)
	m.drainCore()
	assert.False(t, ui.ShowAccounts())
	assert.False(t, m.core.HasExtraAccounts())

	_, _, err := reg.Create("max-2", t.TempDir())
	require.NoError(t, err)
	testModel(m).AdoptAccountsForTest(reg)
	require.False(t, ui.ShowAccounts(), "not shown until the event is applied")
	m.drainCore()
	assert.True(t, ui.ShowAccounts())
	assert.True(t, m.core.HasExtraAccounts())
}

func TestReloadAccounts_SeesAnotherProcessesChanges(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	require.False(t, ui.ShowAccounts())

	// A `loom account add` in another terminal writes the same file.
	other := account.LoadRegistry(filepath.Dir(m.core.AccountsRegistry().AccountsDir()))
	_, _, err := other.Create("max-2", main)
	require.NoError(t, err)
	assert.False(t, m.core.HasExtraAccounts(), "not seen until reloaded")

	m.core.ReloadAccounts()
	m.drainCore()

	_, ok := m.core.AccountsRegistry().Get("max-2")
	assert.True(t, ok)
	assert.True(t, ui.ShowAccounts(), "the reload is republished")
}

func TestAccountStatuses_DefaultFirstAndMarked(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.core.AccountsRegistry().SetDefault("max-2"))
	testModel(m).SetAccountAuthForTest(map[string]session.RemoteControlAuth{
		"max-2": {Identity: account.Identity{ConfigDir: "/acct", LoggedIn: false}},
	})

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
