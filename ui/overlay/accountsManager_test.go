package overlay

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func accountRows() []AccountRow {
	return []AccountRow{
		{Name: "default", Email: "you@example.com", Plan: "max", Usage: "5h 64% · 7d 40%", IsDefault: true},
		{Name: "max-2", Email: "you+2@example.com", Plan: "max", Usage: "5h 12% · 7d 31%", Warning: "not shared: settings.json"},
	}
}

func press(a *AccountsManager, keys ...string) (closed bool) {
	for _, k := range keys {
		switch k {
		case "enter":
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		default:
			r := []rune(k)[0]
			closed = a.HandleKeyPress(tea.KeyPressMsg{Code: r, Text: k})
		}
	}
	return closed
}

func TestAccountsManager_AddRequestsTheTypedName(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "a", "m", "a", "x", "-", "3", "enter")

	req, ok := a.TakeRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestAdd, Name: "max-3"}, req)
	_, ok = a.TakeRequest()
	assert.False(t, ok, "a request is taken once")
}

func TestAccountsManager_EscCancelsAddThenCloses(t *testing.T) {
	a := NewAccountsManager(accountRows())
	assert.False(t, press(a, "a", "esc"), "esc leaves the name prompt, not the screen")
	_, ok := a.TakeRequest()
	assert.False(t, ok)
	assert.True(t, press(a, "esc"))
}

func TestAccountsManager_RowActions(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "j", "enter")
	req, _ := a.TakeRequest()
	assert.Equal(t, AccountRequest{Kind: AccountRequestSetDefault, Name: "max-2"}, req)

	press(a, "l")
	req, _ = a.TakeRequest()
	assert.Equal(t, AccountRequest{Kind: AccountRequestLogin, Name: "max-2"}, req)
}

func TestAccountsManager_RemoveConfirmsAndSparesDefault(t *testing.T) {
	a := NewAccountsManager(accountRows())
	press(a, "x")
	assert.NotContains(t, a.Render(), "Remove account", "the default account has no remove")

	press(a, "j", "x")
	assert.Contains(t, a.Render(), `Remove account "max-2"`)
	press(a, "n")
	_, ok := a.TakeRequest()
	assert.False(t, ok)

	press(a, "x", "y")
	req, ok := a.TakeRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestRemove, Name: "max-2"}, req)
}

func TestAccountsManager_RendersRows(t *testing.T) {
	out := NewAccountsManager(accountRows()).Render()
	assert.Contains(t, out, "* default")
	assert.Contains(t, out, "you+2@example.com")
	assert.Contains(t, out, "5h 12% · 7d 31%")
	assert.Contains(t, out, "not shared: settings.json")
}

func TestSettingsOverlay_OpensAccountsAndPassesRequestsThrough(t *testing.T) {
	s := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	s.SetAccountRows(accountRows())
	for i := 0; i < int(settingsFieldCount); i++ {
		s.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	assert.Contains(t, s.Render(), "Accounts")
	s.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	s.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	s.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	req, ok := s.TakeAccountRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestSetDefault, Name: "max-2"}, req)
}
