package session

import (
	"errors"
	"os"
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

// TestFromInstanceData_CarriesTheAccountsConfigDirOncePublished pins that
// a Paused record's rehydrated tmux session object carries the account's
// CLAUDE_CONFIG_DIR — this is what a resume or a crash-recovery relaunch
// starts from best-effort (bestEffortAccountDir), not a session built
// with no account env at all.
func TestFromInstanceData_CarriesTheAccountsConfigDirOncePublished(t *testing.T) {
	dir := t.TempDir()
	withAccountDirs(t, map[string]string{"max-2": dir})
	data := InstanceData{Title: "acct-paused", Program: "claude", Status: Paused, Account: "max-2"}

	inst, err := FromInstanceData(data, t.TempDir())

	require.NoError(t, err)
	require.NotNil(t, inst.TmuxSession())
	assert.Contains(t, inst.TmuxSession().Env(), "CLAUDE_CONFIG_DIR="+dir)
}

// TestFromInstanceDataPaused_CarriesTheAccountsConfigDirOncePublished is
// TestFromInstanceData_CarriesTheAccountsConfigDirOncePublished for the
// fromInstanceDataPaused variant reconcile.go uses for restart paths
// (ActionRestart/ActionRestartWsTerminal), which builds its own detached
// TmuxSession when FromInstanceData did not (a non-Paused, non-Recoverable
// record).
func TestFromInstanceDataPaused_CarriesTheAccountsConfigDirOncePublished(t *testing.T) {
	dir := t.TempDir()
	withAccountDirs(t, map[string]string{"max-2": dir})
	data := InstanceData{Title: "acct-restart", Program: "claude", Status: Running, Account: "max-2"}

	inst, err := fromInstanceDataPaused(data, t.TempDir())

	require.NoError(t, err)
	require.NotNil(t, inst.TmuxSession())
	assert.Contains(t, inst.TmuxSession().Env(), "CLAUDE_CONFIG_DIR="+dir)
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
	assert.Contains(t, err.Error(), "R on an existing session",
		"the text must name the key that relaunches an existing session on a different account")
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
	assert.Contains(t, err.Error(), "loom account remove max-2", "the recovery differs from an unregistered account: clear the stale registration first")
	assert.Contains(t, err.Error(), "R on an existing session")
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

// TestRecoveryLaunch_ResumesAcrossAnAccountSwitch pins the design's
// switching-accounts guarantee end to end at the recoveryLaunch level: a
// conversation recorded under one account (its sessionID/transcriptPath,
// as a real SessionStart hook would set them) still resumes with
// --resume <id> after the instance is switched to a different account,
// because both accounts' projects/ is a real symlink into the same
// shared main dir (built with account.Sync, exactly as a real account is
// created) — the transcript's path, recorded through the old account's
// dir, keeps resolving. The launch itself runs under the NEW account's
// CLAUDE_CONFIG_DIR, not the old one's.
func TestRecoveryLaunch_ResumesAcrossAnAccountSwitch(t *testing.T) {
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"

	// The shared main dir every account's projects/ links into.
	mainDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mainDir, "projects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainDir, "projects", id+".jsonl"), []byte("{}\n"), 0o600))

	// The account the conversation was originally recorded under.
	oldAcctDir := filepath.Join(t.TempDir(), "old")
	require.NoError(t, os.MkdirAll(oldAcctDir, 0o700))
	_, err := account.Sync(oldAcctDir, mainDir)
	require.NoError(t, err)
	transcriptViaOldAccount := filepath.Join(oldAcctDir, "projects", id+".jsonl")
	require.FileExists(t, transcriptViaOldAccount, "precondition: the transcript resolves through the old account's projects link")

	// The account the user switches the instance to before relaunching.
	newAcctDir := filepath.Join(t.TempDir(), "new")
	require.NoError(t, os.MkdirAll(newAcctDir, 0o700))
	withAccountDirs(t, map[string]string{"max-2": newAcctDir})

	inst := hooksInstance(t, "claude")
	inst.claude = claudeState{sessionID: id, transcriptPath: transcriptViaOldAccount}
	inst.SetAccount("max-2")

	launch, env, err := inst.recoveryLaunch()

	require.NoError(t, err)
	assert.Contains(t, launch, "--resume "+id,
		"the conversation recorded under the old account must still be found via its shared projects link")
	assert.NotContains(t, launch, "--continue")
	assert.Contains(t, env, "CLAUDE_CONFIG_DIR="+newAcctDir, "the relaunch must run under the newly chosen account, not the old one")
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
