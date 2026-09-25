package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
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
	toast := m.errBox.String()
	assert.Contains(t, toast, "settings.json", "names what it kept")
	assert.Contains(t, toast, "to remove it anyway run `loom account remove --force max-2`",
		"this screen can't force; the CLI can")
	assert.NotContains(t, toast, "or remove with --force")
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

// TestAccountRows_LoggedOutIsSaidOnce: the usage column already says
// "logged out"; the warning line is for what the row can't show.
func TestAccountRows_LoggedOutIsSaidOnce(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: false}}}
	m.ensureAccountMaps()
	m.accountSync["max-2"] = account.SyncReport{Diverged: []string{"settings.json"}}

	rows := m.accountRows(m.accountStatuses())

	require.Len(t, rows, 2)
	assert.Equal(t, "logged out", rows[1].Usage)
	assert.Equal(t, "not shared: settings.json", rows[1].Warning)
}

// TestAccountRequest_RemoveCountsALoadedSessionOnce: a loaded slot's own
// state.json holds the same sessions as its live list.
func TestAccountRequest_RemoveCountsALoadedSessionOnce(t *testing.T) {
	global := t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", global)
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.wsCtx = &config.WorkspaceContext{ConfigDir: global}
	inst, err := session.NewInstance(session.InstanceOptions{Title: "on-max-2", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("max-2")
	m.list.AddInstance(inst)
	require.NoError(t, os.WriteFile(filepath.Join(global, "state.json"),
		[]byte(`{"instances":[{"title":"on-max-2","account":"max-2"}]}`), 0o644))

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestRemove, Name: "max-2"})

	assert.Contains(t, m.errBox.String(), "1 session(s) use max-2")
	_, ok := m.accounts.Get("max-2")
	assert.True(t, ok)
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
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.wsCtx = &config.WorkspaceContext{ConfigDir: global}

	n, err := m.accountUsers("max-2")

	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// TestAccountRequest_LoginRefusesAMissingAccountDir: claude would recreate
// the dir bare, unlinked from the main config; remove and re-add links it.
func TestAccountRequest_LoginRefusesAMissingAccountDir(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	acct, ok := m.accounts.Get("max-2")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(acct.Dir))

	m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestLogin, Name: "max-2"})

	toast := m.errBox.String()
	assert.Contains(t, toast, "max-2")
	assert.Contains(t, toast, "loom account remove max-2")
	assert.Contains(t, toast, "add it again")
	_, err := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(err), "nothing launched, nothing recreated")
}

func TestAccountRequest_LoginProceedsOnAnIntactAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")

	cmd := m.handleAccountRequest(overlay.AccountRequest{Kind: overlay.AccountRequestLogin, Name: "max-2"})

	assert.NotNil(t, cmd)
	assert.NotContains(t, m.errBox.String(), "max-2")
}
