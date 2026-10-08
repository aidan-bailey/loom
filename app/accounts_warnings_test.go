package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/charmbracelet/x/ansi"
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

// globalAccounts registers names in a registry at a fresh LOOM_GLOBAL_DIR,
// where InitAccounts finds it, and undoes InitAccounts' publication at
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

// initAccounts loads the account registry as the model's boot does
// (core.Model.Boot), then drains: the strip, then the model's load, whose
// notices land at once.
func initAccounts(m *home) {
	m.accountStrip = ui.NewAccountStrip()
	testModel(m).InitAccounts()
	m.drainCore()
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

	initAccounts(m)

	toast := m.errBox.String()
	assert.Contains(t, toast, `account "max-2"`)
	assert.Contains(t, toast, "CLAUDE_CONFIG_DIR")

	m.errBox.Clear()
	require.NoError(t, reg.SetDefault("max-2"))
	m.core.ReloadAccounts()
	m.drainCore()
	assert.NotContains(t, m.errBox.String(), "CLAUDE_CONFIG_DIR", "said once, at startup")
}

func TestInitAccounts_NoRunningAsWarningFromTheMainDir(t *testing.T) {
	noCredentialOverride(t)
	globalAccounts(t, "max-2")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	m := newTestHome(t)

	initAccounts(m)

	assert.NotContains(t, m.errBox.String(), "CLAUDE_CONFIG_DIR")
}

// TestRefreshAccountViews_ACredentialOverrideWarnsOnTheStrip: with one in
// loom's environment every account runs and bills as it, and the probes
// show its usage under every name.
func TestRefreshAccountViews_ACredentialOverrideWarnsOnTheStrip(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.accountStrip.SetWidth(200)

	m.refreshAccountViews()

	assert.Contains(t, ansi.Strip(m.accountStrip.String()), "⚠ $CLAUDE_CODE_OAUTH_TOKEN set: all accounts use it")
}

func TestRefreshAccountViews_NoOverrideNoWarning(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.accountStrip.SetWidth(200)

	m.refreshAccountViews()

	assert.NotContains(t, ansi.Strip(m.accountStrip.String()), "⚠")
}

func TestInitAccounts_ACredentialOverrideWarnsFromTheStart(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	globalAccounts(t, "max-2")
	m := newTestHome(t)

	initAccounts(m)
	m.accountStrip.SetWidth(200)

	assert.Contains(t, ansi.Strip(m.accountStrip.String()), "⚠ $ANTHROPIC_API_KEY set: all accounts use it")
}

// openAccountsScreen opens Settings and its Accounts screen by keys.
func openAccountsScreen(t *testing.T, m *home) {
	t.Helper()
	runOpenSettings(m)
	require.NotNil(t, m.settingsOverlay())
	for i := 0; i < 20; i++ {
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyDown}) // clamps at Accounts, the last row
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Contains(t, m.settingsOverlay().Render(), "x remove", "the Accounts screen is open")
}

func TestAccountsScreen_ACredentialOverrideIsNoticed(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	m := newTestHome(t)
	withAccounts(t, m, "max-2")

	openAccountsScreen(t, m)

	assert.Contains(t, ansi.Strip(m.settingsOverlay().Render()), "⚠ $CLAUDE_CODE_OAUTH_TOKEN set")
}

func TestAccountsScreen_TheNoticeFollowsARefresh(t *testing.T) {
	noCredentialOverride(t)
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	openAccountsScreen(t, m)
	require.NotContains(t, ansi.Strip(m.settingsOverlay().Render()), "⚠")

	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok")
	m.refreshAccountViews()

	assert.Contains(t, ansi.Strip(m.settingsOverlay().Render()), "⚠ $ANTHROPIC_AUTH_TOKEN set")
}

// TestAccountsScreen_NoNoticeWithoutAnExtraAccount: with default alone,
// there is no account selection for the credential to void.
func TestAccountsScreen_NoNoticeWithoutAnExtraAccount(t *testing.T) {
	noCredentialOverride(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	m := newTestHome(t)
	withAccounts(t, m)

	openAccountsScreen(t, m)

	assert.NotContains(t, ansi.Strip(m.settingsOverlay().Render()), "⚠")
}
