package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
)

func TestAccountLoginDone_RereadsTheAuth(t *testing.T) {
	m := newTestHome(t)
	withAccounts(t, m, "max-2")
	testModel(m).SetGateForTest("accounts_refresh", true, time.Now())

	m.Update(accountLoginDoneMsg{name: account.DefaultName})

	_, pending, _ := testModel(m).GateForTest("accounts_refresh", time.Now())
	assert.True(t, pending)
	assert.True(t, testModel(m).RefreshDefaultAuthForTest())
}
