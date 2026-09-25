package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageProbe_NotDispatchedWithoutAnExtraAccount(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m)

	assert.Nil(t, m.maybeUsageProbe())
	assert.False(t, m.gate(gateUsage).inFlight, "no Cmd, nothing armed")
}

func TestUsageProbe_NotDispatchedWithoutAClaudeProgram(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "aider"
	withAccounts(t, m, "max-2")

	assert.Nil(t, m.maybeUsageProbe())
}

func TestUsageProbe_DispatchesOnceAndThrottles(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")

	require.NotNil(t, m.maybeUsageProbe())
	assert.True(t, m.gate(gateUsage).inFlight)
	assert.Nil(t, m.maybeUsageProbe(), "one probe in flight at a time")
}

// TestUsageProbe_ReloadsTheRegistryFirst: an account another terminal
// removed is not probed (its dir is gone), and the removal is published.
func TestUsageProbe_ReloadsTheRegistryFirst(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")
	other := account.LoadRegistry(filepath.Dir(m.accounts.AccountsDir()))
	_, err := other.Remove("max-2", false)
	require.NoError(t, err)

	assert.Nil(t, m.maybeUsageProbe(), "no extra account left: nothing to probe")
	assert.False(t, m.hasExtraAccounts())
	assert.False(t, ui.ShowAccounts())
}

func TestUsageReady_KeepsTheLastGoodSampleOnError(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	good := account.Usage{Available: true, At: time.Now(), FiveHour: &account.Window{Pct: 12}}

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": good}}})
	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{errs: map[string]error{"max-2": errors.New("timeout")}}})

	got := m.usage["max-2"]
	assert.Equal(t, good, got.last, "display-only: a failed probe keeps the sample")
	assert.Error(t, got.err)
	assert.False(t, m.gate(gateUsage).inFlight)
	st := m.accountStatuses()
	assert.True(t, st[1].Failing)
}

// TestUsageReady_LoggedOutOutranksAProbedSample: a logged-out account's
// probe succeeds with Available false, which alone would render "n/a";
// the auth read saying it is logged out wins.
func TestUsageReady_LoggedOutOutranksAProbedSample(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	acct, ok := m.accounts.Get("max-2")
	require.True(t, ok)
	m.accountAuth = map[string]session.RemoteControlAuth{"max-2": {
		State:    session.RemoteControlAuthBlocked,
		Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: false},
	}}
	now := time.Now()

	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{results: map[string]account.Usage{"max-2": {At: now}}}})

	st := m.accountStatuses()
	require.Len(t, st, 2)
	assert.Equal(t, "logged out", ui.AccountUsageText(st[1], now))
}

func TestRequestUsageProbe_BringsTheNextProbeForward(t *testing.T) {
	m := homeWithAppState(t)
	m.program = "claude"
	withAccounts(t, m, "max-2")
	require.NotNil(t, m.maybeUsageProbe())
	m.Update(gatedMsg{kind: gateUsage, msg: usageReadyMsg{}})
	assert.Nil(t, m.maybeUsageProbe(), "throttled by usageInterval")

	assert.NotNil(t, m.requestUsageProbe())
}
