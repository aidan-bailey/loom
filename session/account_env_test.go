package session

import (
	"errors"
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

// withAccountDirs publishes dirs for one test.
func withAccountDirs(t *testing.T, dirs map[string]string) {
	t.Helper()
	SetAccountDirs(dirs)
	t.Cleanup(func() { SetAccountDirs(nil) })
}

func TestAccountDir(t *testing.T) {
	withAccountDirs(t, map[string]string{"max-2": "/acct/max-2"})

	dir, err := accountDir("")
	require.NoError(t, err)
	assert.Empty(t, dir)
	dir, err = accountDir("max-2")
	require.NoError(t, err)
	assert.Equal(t, "/acct/max-2", dir)

	_, err = accountDir("gone")
	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, "gone", missing.Name)
	assert.Contains(t, err.Error(), "press R")
}

func TestLaunchEnv_MissingAccountFailsOnlyWhenLaunching(t *testing.T) {
	withAccountDirs(t, nil)
	inst := &Instance{program: "claude"}
	inst.SetAccount("gone")

	_, err := inst.launchEnv(true)
	assert.Error(t, err, "a launch never falls back to the default account")

	env, err := inst.launchEnv(false)
	require.NoError(t, err, "building a detached session object launches nothing")
	assert.Empty(t, env.ClaudeConfigDir)
}

func TestLaunchEnv_NonClaudeIgnoresTheAccount(t *testing.T) {
	withAccountDirs(t, nil)
	inst := &Instance{program: "aider"}
	inst.SetAccount("gone")
	_, err := inst.launchEnv(true)
	assert.NoError(t, err)
}

func TestRecoveryLaunch_RunsAsTheAccount(t *testing.T) {
	withAccountDirs(t, map[string]string{"max-2": "/acct/max-2"})
	inst := hooksInstance(t, "claude")
	inst.SetAccount("max-2")

	_, env, err := inst.recoveryLaunch()

	require.NoError(t, err)
	assert.Contains(t, env, "CLAUDE_CONFIG_DIR=/acct/max-2")
}

func TestRecoveryLaunch_MissingAccountFails(t *testing.T) {
	withAccountDirs(t, nil)
	inst := hooksInstance(t, "claude")
	inst.SetAccount("gone")

	_, _, err := inst.recoveryLaunch()

	var missing *MissingAccountError
	assert.True(t, errors.As(err, &missing))
}

func TestStart_MissingAccountFailsBeforeAnySetup(t *testing.T) {
	withAccountDirs(t, nil)
	inst, err := NewInstance(InstanceOptions{Title: "acct-missing", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetAccount("gone")

	err = inst.Start(true)

	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.False(t, inst.Started(), "the failed start releases its reservation")
}
