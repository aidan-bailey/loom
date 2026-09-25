package session

import (
	"errors"
	"path/filepath"
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

// TestFromInstanceData_NormalizesDefaultAccountName pins that a record
// whose Account field literally holds "default" — a hand-edited
// state.json, say, since SetAccount itself never persists that literal —
// decodes the same as one with Account == "": Instance.Account() keeps
// its "" means default" invariant regardless of what's on disk.
func TestFromInstanceData_NormalizesDefaultAccountName(t *testing.T) {
	data := InstanceData{Title: "acct-normalize", Program: "claude", Account: account.DefaultName}

	restored, err := FromInstanceData(data, t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, "", restored.Account())
}

// withAccountDirs publishes dirs for one test, with no registry load error.
func withAccountDirs(t *testing.T, dirs map[string]string) {
	t.Helper()
	SetAccountDirs(dirs, nil)
	t.Cleanup(func() { SetAccountDirs(nil, nil) })
}

func TestAccountDir(t *testing.T) {
	dir := t.TempDir()
	withAccountDirs(t, map[string]string{"max-2": dir})

	got, err := accountDir("")
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = accountDir("max-2")
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	_, err = accountDir("gone")
	var missing *MissingAccountError
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, "gone", missing.Name)
	assert.Contains(t, err.Error(), "Session Launch Options")
}

// TestAccountDir_MissingDirFailsClosed pins that a registered account
// whose config directory has been deleted out from under loom gets its
// own error, never MissingAccountError's "not registered" text — the two
// mean different things to the user (pick another account vs. this one's
// state is gone).
func TestAccountDir_MissingDirFailsClosed(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	withAccountDirs(t, map[string]string{"max-2": gone})

	_, err := accountDir("max-2")

	var missingDir *AccountDirMissingError
	require.True(t, errors.As(err, &missingDir))
	assert.Equal(t, "max-2", missingDir.Name)
	assert.Equal(t, gone, missingDir.Dir)
	assert.ErrorIs(t, err, account.ErrAccountDirMissing)
	var missingAcct *MissingAccountError
	assert.False(t, errors.As(err, &missingAcct), "a dir-missing error must not be mistaken for an unregistered account")
}

// TestAccountDir_RegistryLoadFailureIsDistinctFromUnregistered pins that a
// registry that failed to load reports its own error rather than telling
// the user the account isn't registered — loom genuinely doesn't know.
func TestAccountDir_RegistryLoadFailureIsDistinctFromUnregistered(t *testing.T) {
	registryErr := errors.New("accounts.json: corrupt")
	SetAccountDirs(nil, registryErr)
	t.Cleanup(func() { SetAccountDirs(nil, nil) })

	_, err := accountDir("max-2")

	var loadErr *RegistryLoadError
	require.True(t, errors.As(err, &loadErr))
	assert.Equal(t, "max-2", loadErr.Name)
	assert.ErrorIs(t, err, registryErr)
	var missing *MissingAccountError
	assert.False(t, errors.As(err, &missing), "a registry that failed to load must not be reported as the account being unregistered")

	// The default account never needs the registry.
	got, err := accountDir("")
	require.NoError(t, err)
	assert.Empty(t, got)
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

// TestLaunchEnv_MissingAccountDirFailsWhenLaunching pins that a registered
// account whose config dir vanished fails a real launch with its own
// error, not the "not registered" MissingAccountError.
func TestLaunchEnv_MissingAccountDirFailsWhenLaunching(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	withAccountDirs(t, map[string]string{"max-2": gone})
	inst := &Instance{program: "claude"}
	inst.SetAccount("max-2")

	_, err := inst.launchEnv(true)

	var missingDir *AccountDirMissingError
	assert.True(t, errors.As(err, &missingDir))
}

func TestRecoveryLaunch_RunsAsTheAccount(t *testing.T) {
	dir := t.TempDir()
	withAccountDirs(t, map[string]string{"max-2": dir})
	inst := hooksInstance(t, "claude")
	inst.SetAccount("max-2")

	_, env, err := inst.recoveryLaunch()

	require.NoError(t, err)
	assert.Contains(t, env, "CLAUDE_CONFIG_DIR="+dir)
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
