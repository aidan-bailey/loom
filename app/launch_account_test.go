package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bareOpts(acct string) overlay.LaunchOptions {
	return overlay.LaunchOptions{PermissionMode: "default", Model: "default", Effort: "default", Account: acct}
}

func TestNewLaunchOptionsOverlay_NoAccountRowWithoutExtras(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	lo := m.newLaunchOptionsOverlay(bareOpts(""), "claude")
	assert.NotContains(t, lo.Render(), "Account")
}

func TestNewLaunchOptionsOverlay_PreselectsTheRegistryDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.accounts.SetDefault("max-2"))

	lo := m.newLaunchOptionsOverlay(bareOpts(""), "claude")

	assert.Equal(t, "max-2", lo.Options().Account)
	assert.Contains(t, lo.Render(), "Account")
}

func TestNewLaunchOptionsOverlay_AnUnknownAccountFallsBackToTheDefault(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	lo := m.newLaunchOptionsOverlay(bareOpts("gone"), "claude")
	assert.Equal(t, account.DefaultName, lo.Options().Account,
		"R on a session whose account was removed must not relaunch as it again")
}

// TestNewLaunchOptionsOverlay_NoAccountRowForANonClaudeProgram: accounts
// are Claude config dirs, so an aider session gets no Account row and
// records no account.
func TestNewLaunchOptionsOverlay_NoAccountRowForANonClaudeProgram(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	require.NoError(t, m.accounts.SetDefault("max-2"))

	lo := m.newLaunchOptionsOverlay(bareOpts("max-2"), "aider")

	assert.NotContains(t, lo.Render(), "Account")
	assert.False(t, lo.AccountsShown())
	assert.Equal(t, "", lo.Options().Account)
}

// TestNewLaunchOptionsOverlay_AnUnloadableRegistryKeepsTheSessionsAccount:
// a registry that failed to load lists no accounts, so the row is hidden.
// Rewriting the session's account to default there would relaunch it on
// another subscription without the user ever seeing a choice; keeping it
// makes the launch fail closed (session.RegistryLoadError) instead.
func TestNewLaunchOptionsOverlay_AnUnloadableRegistryKeepsTheSessionsAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m)
	m.accounts = account.Unavailable(errors.New("accounts.json is corrupt"))

	lo := m.newLaunchOptionsOverlay(bareOpts("max-2"), "claude")

	assert.False(t, lo.AccountsShown())
	assert.Equal(t, "max-2", lo.Options().Account)
}

func TestRefreshAccountViews_LeavesANonClaudeModalAlone(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	lo := m.newLaunchOptionsOverlay(bareOpts(""), "aider")
	m.setOverlay(lo, overlayLaunchOptions)

	m.refreshAccountViews()

	assert.False(t, lo.AccountsShown(), "a refresh must not add the Account row to a non-Claude launch")
	assert.Equal(t, "", lo.Options().Account)
}

// TestRefreshAccountViews_KeepsTheModalsChoiceWhenTheRegistryFailsToLoad:
// a reload that fails lists no accounts; refreshing the open modal from
// that would hide the row and quietly reset the choice to default.
func TestRefreshAccountViews_KeepsTheModalsChoiceWhenTheRegistryFailsToLoad(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	lo := m.newLaunchOptionsOverlay(bareOpts("max-2"), "claude")
	m.setOverlay(lo, overlayLaunchOptions)
	require.Equal(t, "max-2", lo.Options().Account)

	m.accounts = account.Unavailable(errors.New("accounts.json is corrupt"))
	m.refreshAccountViews()

	assert.True(t, lo.AccountsShown())
	assert.Equal(t, "max-2", lo.Options().Account, "kept, so the launch fails closed")
}

func TestRefreshAccountViews_FollowsAnOpenClaudeModal(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	lo := m.newLaunchOptionsOverlay(bareOpts("max-2"), "claude")
	m.setOverlay(lo, overlayLaunchOptions)
	require.NotContains(t, lo.Render(), "12%")

	m.ensureAccountMaps()
	m.usage["max-2"] = accountUsage{last: account.Usage{Available: true, At: time.Now(), FiveHour: &account.Window{Pct: 12}}}
	m.refreshAccountViews()

	assert.Contains(t, lo.Render(), "12%")
}

func TestApplyChosenLaunch_RecordsTheAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-launch", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)

	m.applyChosenLaunch(inst, bareOpts("max-2"), "claude")
	assert.Equal(t, "max-2", inst.Account())

	m.applyChosenLaunch(inst, bareOpts(account.DefaultName), "claude")
	assert.Equal(t, "", inst.Account())
}

func TestApplyChosenLaunch_UsesTheAccountsRemoteControlAuth(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthOK}
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out"}}
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-rc", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)

	opts := bareOpts("max-2")
	opts.RemoteControl = true
	m.applyChosenLaunch(inst, opts, "claude")
	assert.NotContains(t, inst.Program(), "--remote-control")
	assert.True(t, m.remoteControlBlockedOn("max-2", true, "claude"))

	opts.Account = account.DefaultName
	m.applyChosenLaunch(inst, opts, "claude")
	assert.Contains(t, inst.Program(), "--remote-control")
	assert.False(t, m.remoteControlBlockedOn(account.DefaultName, true, "claude"))
}

func TestRestartWithOptions_PresetsTheSessionsAccount(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2", "max-3")
	inst, err := session.NewInstance(session.InstanceOptions{Title: "acct-restart", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("max-3")
	m.list.AddInstance(inst)
	require.Equal(t, inst, m.list.GetSelectedInstance())

	runRestartWithOptionsSelected(m)

	lo := m.launchOptionsOverlay()
	require.NotNil(t, lo)
	assert.Equal(t, "max-3", lo.Options().Account)
}
