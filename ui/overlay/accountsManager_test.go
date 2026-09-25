package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

// TestAccountsManager_RowsFitOnOneLineAtDefaultWidth pins the width
// budget: border + padding eat 6 columns of the default 60 (matching
// every other Settings sub-screen), so nothing wraps. Email now renders
// on its own indented line under the row (see
// TestAccountsManager_UsageAndNamesSurviveTightWidths for why), so it is
// checked separately from Usage rather than sharing a line with it.
func TestAccountsManager_RowsFitOnOneLineAtDefaultWidth(t *testing.T) {
	a := NewAccountsManager(accountRows())
	require.Equal(t, 60, a.width, "default width matches the other Settings sub-screens")
	out := a.Render()
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, len([]rune(ansi.Strip(line))), 60)
	}
	stripped := ansi.Strip(out)
	assert.Contains(t, stripped, "you+2@example.com")
	assert.Contains(t, stripped, "5h 12% · 7d 31%")
	assert.Contains(t, stripped, "not shared: settings.json")
	// None of those three strings is itself split across two lines.
	for _, want := range []string{"you+2@example.com", "5h 12% · 7d 31%", "not shared: settings.json"} {
		found := 0
		for _, line := range strings.Split(stripped, "\n") {
			if strings.Contains(line, want) {
				found++
			}
		}
		assert.Equal(t, 1, found, "%q must appear intact on exactly one line", want)
	}
}

// TestAccountsManager_UsageAndNamesSurviveTightWidths reproduces the
// misleading-truncation bug: the Accounts overlay opens at width 60 (app
// never resizes it before the first render) and an 80-column terminal's
// 60%-wide overlay gives 48, and at both widths a percentage must never
// be cut mid-digit (100% must never render as if it read 10%), and two
// similarly named accounts — exactly what this screen exists to tell
// apart before logging in or setting the default — must both show their
// full name rather than an identical-looking truncated prefix.
func TestAccountsManager_UsageAndNamesSurviveTightWidths(t *testing.T) {
	rows := []AccountRow{
		{Name: "work-sub-1", Email: "a@example.com", Plan: "max", Usage: "5h 100% · 7d 100%", IsDefault: true},
		{Name: "work-sub-2", Email: "b@example.com", Plan: "max", Usage: "5h 12% · 7d 31% · 9m ago"},
	}
	for _, w := range []int{48, 60} {
		a := NewAccountsManager(rows)
		a.SetWidth(w)
		out := ansi.Strip(a.Render())

		assert.Contains(t, out, "work-sub-1", "width %d: name must not be cut when it fits the cap", w)
		assert.Contains(t, out, "work-sub-2", "width %d: name must not be cut when it fits the cap", w)

		assert.Contains(t, out, "5h 100%", "width %d: the 5-hour figure must survive intact", w)
		assert.Contains(t, out, "5h 12%", "width %d: the 5-hour figure must survive intact", w)
		assert.NotContains(t, out, "10…", "width %d: 100% must never be clipped to read as 10%", w)

		for _, line := range strings.Split(out, "\n") {
			assert.LessOrEqual(t, len([]rune(line)), w, "width %d", w)
		}
	}
}

// TestAccountsManager_RemoveTargetsByIdentityNotPosition reproduces the
// bug where "x" captured a row position rather than an account identity:
// a SetRows refresh between "x" and "y" (e.g. the registry reloaded after
// a CLI remove) can shift a different account into the confirmed row.
func TestAccountsManager_RemoveTargetsByIdentityNotPosition(t *testing.T) {
	a := NewAccountsManager([]AccountRow{
		{Name: "default", IsDefault: true},
		{Name: "max-2"},
		{Name: "max-3"},
	})
	press(a, "j", "x") // select max-2, ask to remove it
	assert.Contains(t, a.Render(), `Remove account "max-2"`)

	// max-2 was removed elsewhere (e.g. the CLI); max-3 has shifted into
	// its old row.
	a.SetRows([]AccountRow{
		{Name: "default", IsDefault: true},
		{Name: "max-3"},
	})
	assert.NotContains(t, a.Render(), "Remove account", "a vanished target cancels the pending confirmation")

	press(a, "y")
	_, ok := a.TakeRequest()
	assert.False(t, ok, "y must not fire against whatever now occupies the old row")
}

// TestAccountsManager_SetRowsKeepsCursorOnTheSameAccount pins the
// companion fix: a refresh must not silently move the cursor onto a
// different account by position, or "enter" could set the wrong default.
func TestAccountsManager_SetRowsKeepsCursorOnTheSameAccount(t *testing.T) {
	a := NewAccountsManager([]AccountRow{
		{Name: "default", IsDefault: true},
		{Name: "max-2"},
		{Name: "max-3"},
	})
	press(a, "j", "j") // cursor on max-3 (index 2)

	// max-2 removed elsewhere: max-3 shifts to index 1.
	a.SetRows([]AccountRow{
		{Name: "default", IsDefault: true},
		{Name: "max-3"},
	})

	press(a, "enter")
	req, ok := a.TakeRequest()
	require.True(t, ok)
	assert.Equal(t, AccountRequest{Kind: AccountRequestSetDefault, Name: "max-3"}, req,
		"the cursor must follow max-3 by identity, not stay pinned to its old index")
}

// TestSettingsOverlay_SetSizePropagatesToOpenAccountsScreen pins the
// resize-forwarding fix: without it, the Accounts sub-screen keeps
// rendering at whatever width it was opened with, so a terminal resize
// (or a narrower real overlay width than the 60 default) never reaches
// its wrap-avoidance budget.
func TestSettingsOverlay_SetSizePropagatesToOpenAccountsScreen(t *testing.T) {
	s := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	s.SetAccountRows(accountRows())
	s.cursor = int(settingsFieldAccounts)
	s.activateRow()
	require.NotNil(t, s.accounts)

	s.SetSize(90, 20)
	assert.Equal(t, 90, s.accounts.width)
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
