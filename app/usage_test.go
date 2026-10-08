package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageReady_KeepsTheLastGoodSampleOnError(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	// Round(0): over the wire a time keeps its instant but not its
	// monotonic reading, which reflect.DeepEqual compares.
	good := account.Usage{Available: true, At: time.Now().Round(0), FiveHour: &account.Window{Pct: 12}}

	deliver(t, m, core.UsageResultForTest(map[string]account.Usage{"max-2": good}, nil))
	deliver(t, m, core.UsageResultForTest(nil, map[string]error{"max-2": errors.New("timeout")}))

	last, probeErr := m.core.AccountUsage("max-2")
	assert.Equal(t, good, last, "display-only: a failed probe keeps the sample")
	assert.Error(t, probeErr)
	inFlight, _, _ := testModel(m).GateForTest("usage", time.Now())
	assert.False(t, inFlight)
	st := m.accountStatuses()
	assert.True(t, st[1].Failing)
}

// TestUsageReady_LoggedOutOutranksAProbedSample: a logged-out account's
// probe succeeds with Available false, which alone would render "n/a";
// the auth read saying it is logged out wins.
func TestUsageReady_LoggedOutOutranksAProbedSample(t *testing.T) {
	m := homeWithAppState(t)
	withAccounts(t, m, "max-2")
	acct, ok := m.core.Account("max-2")
	require.True(t, ok)
	testModel(m).SetAccountAuthForTest(map[string]session.RemoteControlAuth{"max-2": {
		State:    session.RemoteControlAuthBlocked,
		Identity: account.Identity{ConfigDir: acct.Dir, LoggedIn: false},
	}})
	now := time.Now()

	deliver(t, m, core.UsageResultForTest(map[string]account.Usage{"max-2": {At: now}}, nil))

	st := m.accountStatuses()
	require.Len(t, st, 2)
	assert.Equal(t, "logged out", ui.AccountUsageText(st[1], now))
}
