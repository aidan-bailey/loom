package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountRequest_SetDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestSetDefault, Name: "max-2"})
	assert.Equal(t, "max-2", m.accounts.Default())
}

func TestAccountRequest_AddCreatesAndLogsIn(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	m.rcAuth.Identity = account.Identity{LoggedIn: true, ConfigDir: main}

	cmd := m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestAdd, Name: "max-3"})

	require.NotNil(t, cmd, "the login runs next")
	_, ok := m.accounts.Get("max-3")
	assert.True(t, ok)
	assert.True(t, m.hasExtraAccounts())
}

func TestAccountRequest_RemoveIsRefusedWhileASessionUsesIt(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "on-max-2", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("max-2")
	m.list.AddInstance(inst)

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	_, ok := m.accounts.Get("max-2")
	assert.True(t, ok, "in use: not removed")
}

func TestAccountRequest_RemoveDeletesAnUnusedAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	_, ok := m.accounts.Get("max-2")
	assert.False(t, ok)
	_, err := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(err))
	assert.False(t, m.hasExtraAccounts())
}

// TestAccountRequest_RemoveKeepsAnAccountHoldingUnsharedFiles: a real
// settings.json in the account dir exists nowhere else, so the Settings
// screen (which never forces) keeps the account and says why.
func TestAccountRequest_RemoveKeepsAnAccountHoldingUnsharedFiles(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	acct, ok := m.accounts.Get("max-2")
	require.True(t, ok)
	settings := filepath.Join(acct.Dir, "settings.json")
	require.NoError(t, os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o644))

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	_, ok = m.accounts.Get("max-2")
	assert.True(t, ok, "kept: its settings.json is not shared")
	_, err := os.Stat(settings)
	assert.NoError(t, err, "nothing deleted")
	assert.Contains(t, m.errBox.String(), "--force", "the toast is Remove's own refusal")
}

// TestAccountRequest_ReloadsTheRegistryFirst: an account another terminal
// added is visible to the Settings actions without a restart.
func TestAccountRequest_ReloadsTheRegistryFirst(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	other := account.LoadRegistry(filepath.Dir(m.accounts.AccountsDir()))
	_, _, err := other.Create("max-2", main)
	require.NoError(t, err)

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestSetDefault, Name: "max-2"})

	assert.Equal(t, "max-2", m.accounts.Default())
}

// TestHandleStateSettingsKey_CarriesOutAnAccountsScreenRequest drives the
// Accounts screen by keys: Settings → Accounts → max-2 → enter (default).
func TestHandleStateSettingsKey_CarriesOutAnAccountsScreenRequest(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	runOpenSettings(m)
	require.NotNil(t, m.settingsOverlay())
	press := func(k rune) { handleStateSettingsKey(m, tea.KeyPressMsg{Code: k}) }
	for i := 0; i < 20; i++ {
		press(tea.KeyDown) // the list clamps at its last row, Accounts
	}
	press(tea.KeyEnter)
	require.Contains(t, m.settingsOverlay().Render(), "x remove", "the Accounts screen is open")
	require.Contains(t, m.settingsOverlay().Render(), "max-2")

	press(tea.KeyDown)  // default → max-2
	press(tea.KeyEnter) // make it the default

	assert.Equal(t, "max-2", m.accounts.Default())
	assert.Contains(t, m.settingsOverlay().Render(), "* max-2", "the open screen is refreshed")
}

func TestRunOpenSettings_ListsTheAccounts(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	runOpenSettings(m)
	so := m.settingsOverlay()
	require.NotNil(t, so)
	assert.Contains(t, so.Render(), "Accounts")
}
