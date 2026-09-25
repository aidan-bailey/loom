package session

import (
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceAccount_DefaultNameIsStoredEmpty(t *testing.T) {
	inst := &Instance{}
	inst.SetAccount("max-2")
	assert.Equal(t, "max-2", inst.Account())
	inst.SetAccount(account.DefaultName)
	assert.Equal(t, "", inst.Account())
}

func TestInstanceAccount_SurvivesSnapshotAndRestore(t *testing.T) {
	inst := &Instance{Title: "acct-roundtrip", program: "claude"}
	inst.SetAccount("max-2")

	data := inst.Snapshot()
	require.Equal(t, "max-2", data.Account)
	restored, err := FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "max-2", restored.Account())
}
