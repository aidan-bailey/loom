package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRefresh runs a dispatched accounts refresh and returns its result.
// The program must not resolve to a real binary.
func runRefresh(t *testing.T, cmd tea.Cmd) accountsRefreshedMsg {
	t.Helper()
	require.NotNil(t, cmd)
	gm, ok := cmd().(gatedMsg)
	require.True(t, ok, "the refresh is gated")
	require.Equal(t, gateAccountsRefresh, gm.kind)
	msg, ok := gm.msg.(accountsRefreshedMsg)
	require.True(t, ok)
	return msg
}

func TestAccountsRefresh_AReloadFindingAnAccountWithoutAuthReadsIt(t *testing.T) {
	m := newTestHome(t)
	main := withAccounts(t, m)
	_, _, err := otherTerminal(t, m).Create("max-2", main)
	require.NoError(t, err)

	m.maybeReloadAccounts()

	assert.True(t, m.gate(gateAccountsRefresh).inFlight, "a new account's auth is read, not left Unknown")
}

func TestAccountsRefresh_AReloadWithEveryAuthKnownReadsNothing(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthOK}}
	require.NoError(t, otherTerminal(t, m).SetDefault("max-2"))

	m.maybeReloadAccounts()

	assert.Equal(t, "max-2", m.accounts.Default(), "the reload happened")
	assert.False(t, m.gate(gateAccountsRefresh).inFlight)
}

func TestAccountsRefresh_IncludesTheDefaultWhileItsIdentityIsUnknown(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // account.MainDir's fallback
	m := newTestHome(t)
	m.program = "/nonexistent/loom-test/claude"
	main := withAccounts(t, m, "max-2")

	msg := runRefresh(t, m.requestAccountsRefresh(false))
	assert.NotNil(t, msg.defaultAuth, "an RC-off startup never read the default account")

	m.Update(gatedMsg{kind: gateAccountsRefresh, msg: msg})
	m.rcAuth.Identity.ConfigDir = main
	msg = runRefresh(t, m.requestAccountsRefresh(false))
	assert.Nil(t, msg.defaultAuth, "known: not reread")
}

func TestRequestAccountsRefresh_DoesNotStack(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")

	require.NotNil(t, m.requestAccountsRefresh(false))
	assert.Nil(t, m.requestAccountsRefresh(false), "one refresh in flight at a time")
	assert.True(t, m.gate(gateAccountsRefresh).pending, "the second runs once the first lands")
}

func TestUsageReady_LosingAccessRereadsTheAuth(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	m.ensureAccountMaps()
	m.usage["max-2"] = accountUsage{last: account.Usage{Available: true, At: time.Now().Add(-time.Minute)}}

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).inFlight, "an expired login can then show as logged out")
}

func TestUsageReady_AFirstProbeWithoutAccessWhileLoggedInRereadsTheAuth(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: true}}}

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).inFlight)
}

func TestUsageReady_StillNoAccessRereadsNothing(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	acct, _ := m.accounts.Get("max-2")
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: true}}}
	m.ensureAccountMaps()
	m.usage["max-2"] = accountUsage{last: account.Usage{At: time.Now().Add(-time.Minute)}}

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": {At: time.Now()}}}})

	assert.False(t, m.gate(gateAccountsRefresh).inFlight, "an API-key account is n/a every round; one reread is enough")
}

func TestUsageReady_TheDefaultLosingAccessRereadsTheDefault(t *testing.T) {
	m := homeWithAppState(t)
	main := withAccounts(t, m, "max-2")
	m.rcAuth.Identity = account.Identity{ConfigDir: main, LoggedIn: true}
	m.ensureAccountMaps()
	m.usage[account.DefaultName] = accountUsage{last: account.Usage{Available: true, At: time.Now().Add(-time.Minute)}}
	m.gate(gateAccountsRefresh).inFlight = true // a refresh already running

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{account.DefaultName: {At: time.Now()}}}})

	assert.True(t, m.gate(gateAccountsRefresh).pending, "queued behind the running one")
	assert.True(t, m.refreshDefaultAuth, "and it covers the default account")
}

func TestAccountLoginDone_RereadsTheAuth(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	m.gate(gateAccountsRefresh).inFlight = true

	m.Update(accountLoginDoneMsg{name: account.DefaultName})

	assert.True(t, m.gate(gateAccountsRefresh).pending)
	assert.True(t, m.refreshDefaultAuth)
}
