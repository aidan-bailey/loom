package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedAccountExec answers `auth status` and the usage probe.
type scriptedAccountExec struct{ auth, usage string }

func (s *scriptedAccountExec) out(c *exec.Cmd) ([]byte, error) {
	switch {
	case slices.Contains(c.Args, "status"):
		return []byte(s.auth), nil
	case slices.Contains(c.Args, "-p"):
		return []byte(s.usage), nil
	}
	return nil, errors.New("unexpected: " + strings.Join(c.Args, " "))
}
func (s *scriptedAccountExec) Run(c *exec.Cmd) error                      { _, err := s.out(c); return err }
func (s *scriptedAccountExec) Output(c *exec.Cmd) ([]byte, error)         { return s.out(c) }
func (s *scriptedAccountExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.out(c) }

const cliAuthJSON = `{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"/main"}`
const cliUsageJSON = `{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12},"seven_day":{"utilization":31}}}}}`

// isolateAccounts points the account commands at throwaway dirs and a fake
// CLI. Returns the global dir.
func isolateAccounts(t *testing.T) string {
	t.Helper()
	global, main := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", t.TempDir())
	// Hermetic against whatever shell runs the test suite: a real
	// CLAUDE_CONFIG_DIR in the environment must never leak into these
	// tests. Tests exercising guardAgainstAccountEnv (item 7) set their
	// own value with t.Setenv, which layers cleanly over this.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	require.NoError(t, os.WriteFile(filepath.Join(main, "CLAUDE.md"), []byte("x"), 0o600))
	origMain, origLogin, origExec := accountMainDir, accountLogin, accountExec
	accountMainDir = func(string) string { return main }
	accountLogin = func(string, []string) error { return nil }
	accountExec = &scriptedAccountExec{auth: cliAuthJSON, usage: cliUsageJSON}
	t.Cleanup(func() {
		accountMainDir, accountLogin, accountExec = origMain, origLogin, origExec
		accountNoLogin, accountForce, accountYes = false, false, false
	})
	return global
}

func runAccount(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	AccountCmd.SetOut(&out)
	AccountCmd.SetErr(&out)
	AccountCmd.SetIn(strings.NewReader(stdin))
	AccountCmd.SetArgs(args)
	err := AccountCmd.Execute()
	return out.String(), err
}

func TestAccountAdd_NoLoginCreatesALinkedAccount(t *testing.T) {
	global := isolateAccounts(t)

	out, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.NoError(t, err)
	assert.Contains(t, out, "Created")
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	_, err = os.Readlink(filepath.Join(acct.Dir, "CLAUDE.md"))
	assert.NoError(t, err)
}

func TestAccountAdd_LogsInAsTheNewAccount(t *testing.T) {
	isolateAccounts(t)
	var gotEnv []string
	accountLogin = func(_ string, env []string) error { gotEnv = env; return nil }

	out, err := runAccount(t, "", "add", "max-2")

	require.NoError(t, err)
	require.Len(t, gotEnv, 1)
	assert.True(t, strings.HasPrefix(gotEnv[0], "CLAUDE_CONFIG_DIR="))
	assert.Contains(t, out, "Logged in max-2 as you@example.com (max)")
}

func TestAccountUse_SetsTheDefault(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	_, err = runAccount(t, "", "use", "max-2")

	require.NoError(t, err)
	assert.Equal(t, "max-2", account.LoadRegistry(global).Default())
}

func TestAccountList_ShowsUsage(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, "12%")
	assert.Contains(t, out, "31%")
	assert.Contains(t, out, "*")
}

func TestAccountList_ShowsAccountDirMissing(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(acct.Dir))

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, account.ErrAccountDirMissing.Error())
}

func TestAccountRemove_RefusedWhileInUse(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(global, "state.json"),
		[]byte(`{"instances":[{"title":"t","account":"max-2"}]}`), 0o644))

	_, err = runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 session(s) use max-2")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok)
}

func TestAccountRemove_ConfirmsAndDeletes(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, _ := account.LoadRegistry(global).Get("max-2")

	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Removed max-2")
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAccountRemove_AbortsWithoutYes(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok)
}

// TestAccountRemove_RefusedWithUnsharedFiles is the Amendments (binding)
// test: a real (non-symlink) file written into the account dir after
// creation makes plain `remove` refuse, answered "y" or not, and
// `--force` still deletes it.
func TestAccountRemove_RefusedWithUnsharedFiles(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, "settings.json"), []byte("{}"), 0o600))

	// The unshared check runs (and refuses) before the confirmation
	// prompt, so "y\n" is never even consumed: refused outright, nothing
	// printed to confirm.
	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "settings.json")
	assert.NotContains(t, out, "[y/N]")
	_, ok = account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok, "account must still be registered")
	assert.FileExists(t, filepath.Join(acct.Dir, "settings.json"))

	// --force overrides the unshared refusal but not the confirmation
	// prompt (that's --yes's job), so it still needs answering.
	out, err = runAccount(t, "y\n", "remove", "max-2", "--force")

	require.NoError(t, err)
	assert.Contains(t, out, "overriding unshared files: settings.json")
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
	_, ok = account.LoadRegistry(global).Get("max-2")
	assert.False(t, ok)
}

// --- Fix round: --yes/--force split (item 1), reordering (item 2) ---

func TestAccountRemove_YesStillRefusesInUse(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(global, "state.json"),
		[]byte(`{"instances":[{"title":"t","account":"max-2"}]}`), 0o644))

	_, err = runAccount(t, "", "remove", "max-2", "--yes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 session(s) use max-2")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok)
}

func TestAccountRemove_ForceWithoutYesStillPrompts(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "\n", "remove", "max-2", "--force")

	require.NoError(t, err)
	assert.Contains(t, out, "[y/N]")
	assert.Contains(t, out, "Aborted")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok, "aborting a forced removal must still keep the account")
}

func TestAccountRemove_ForceYesRemovesInUseAndReportsOverride(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(global, "state.json"),
		[]byte(`{"instances":[{"title":"t","account":"max-2"}]}`), 0o644))

	out, err := runAccount(t, "", "remove", "max-2", "--force", "--yes")

	require.NoError(t, err)
	assert.Contains(t, out, "overriding 1 session(s) using max-2")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.False(t, ok)
}

// --- Prompt for a non-owned dir (item 3) ---

func TestAccountRemove_PromptDiffersForANonOwnedDir(t *testing.T) {
	global := isolateAccounts(t)
	outside := t.TempDir()
	data := fmt.Sprintf(`{"accounts":[{"name":"byo","dir":%q}]}`, outside)
	require.NoError(t, os.WriteFile(filepath.Join(global, "accounts.json"), []byte(data), 0o644))

	out, err := runAccount(t, "\n", "remove", "byo")

	require.NoError(t, err)
	assert.Contains(t, out, "not loom's")
	assert.Contains(t, out, outside)
	assert.NotContains(t, out, "delete "+outside, "an unowned dir must never be offered for deletion")
	_, statErr := os.Stat(outside)
	assert.NoError(t, statErr, "a dir loom does not own must never be at risk from this command")
	_, ok := account.LoadRegistry(global).Get("byo")
	assert.True(t, ok, "declining the prompt must keep it registered")

	out, err = runAccount(t, "y\n", "remove", "byo")

	require.NoError(t, err)
	assert.Contains(t, out, "left")
	assert.Contains(t, out, "loom did not create it")
	_, statErr = os.Stat(outside)
	assert.NoError(t, statErr, "loom only deletes dirs it created")
	_, ok = account.LoadRegistry(global).Get("byo")
	assert.False(t, ok)
}

// --- Login (item 4) ---

func TestRunAccountLogin_ReturnsTheChildsResult(t *testing.T) {
	// Exercises the signal.Ignore/signal.Reset wrapping around the child
	// without depending on real signal delivery timing: `true`/`false`
	// ignore the "auth login" args LoginCmd appends and just report their
	// own exit status, which must still come through unaffected.
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no `true` binary on PATH")
	}
	assert.NoError(t, runAccountLogin("true", nil))

	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("no `false` binary on PATH")
	}
	assert.Error(t, runAccountLogin("false", nil))
}

func TestAccountAdd_FailedLoginKeepsTheAccountAndSaysHowToFinish(t *testing.T) {
	global := isolateAccounts(t)
	accountLogin = func(string, []string) error { return errors.New("boom") }

	out, err := runAccount(t, "", "add", "max-2")

	require.Error(t, err)
	assert.Contains(t, out, "max-2 was created; finish with: loom account login max-2")
	_, ok := account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok, "a failed login during add must not un-create the account")
}

func TestAccountLogin_LogsInAndReports(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	var gotEnv []string
	accountLogin = func(_ string, env []string) error { gotEnv = env; return nil }

	out, err := runAccount(t, "", "login", "max-2")

	require.NoError(t, err)
	require.Len(t, gotEnv, 1)
	assert.True(t, strings.HasPrefix(gotEnv[0], "CLAUDE_CONFIG_DIR="))
	assert.Contains(t, out, "Logged in max-2 as you@example.com (max)")
}

func TestAccountLogin_RefusesWhenTheAccountDirIsMissing(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(acct.Dir))

	_, err = runAccount(t, "", "login", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loom account remove max-2")
	assert.Contains(t, err.Error(), "loom account add max-2")
}

// --- list (item 5) ---

func TestAccountList_OrdersRowsByNamesRegardlessOfProbeTiming(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "a-acct", "--no-login")
	require.NoError(t, err)
	_, err = runAccount(t, "", "add", "b-acct", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	iDefault, iA, iB := strings.Index(out, "default"), strings.Index(out, "a-acct"), strings.Index(out, "b-acct")
	require.True(t, iDefault >= 0 && iA >= 0 && iB >= 0, "all three rows must appear")
	assert.True(t, iDefault < iA && iA < iB, "rows must print in reg.Names() order regardless of which probe finishes first")
}

func TestAccountList_SkipsDefaultProbeWhenMainDirIsUnknown(t *testing.T) {
	isolateAccounts(t)
	accountMainDir = func(string) string { return "" }

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "no main config dir found")
}

// --- sync guards (item 6) ---

func TestAccountAdd_RefusesWhenMainDirIsUnknown(t *testing.T) {
	isolateAccounts(t)
	accountMainDir = func(string) string { return "" }

	_, err := runAccount(t, "", "add", "max-2", "--no-login")

	assert.Error(t, err)
}

func TestAccountSync_RefusesWhenMainDirIsUnknown(t *testing.T) {
	isolateAccounts(t)
	accountMainDir = func(string) string { return "" }

	_, err := runAccount(t, "", "sync")

	assert.Error(t, err)
}

func TestAccountSync_LinksNewEntriesForEveryAccount(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	main := accountMainDir("")
	require.NoError(t, os.WriteFile(filepath.Join(main, "NEW.md"), []byte("x"), 0o600))

	out, err := runAccount(t, "", "sync")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2: linked 1 new")
	_, err = os.Readlink(filepath.Join(acct.Dir, "NEW.md"))
	assert.NoError(t, err)
}

// --- CLAUDE_CONFIG_DIR leak from an account's own shell (item 7) ---

func TestAccountCmds_RefuseWhenRunningAsAnAccount(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	t.Setenv("CLAUDE_CONFIG_DIR", acct.Dir)

	_, err = runAccount(t, "", "list")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `account "max-2"`)

	_, err = runAccount(t, "", "sync")
	assert.Error(t, err)

	_, err = runAccount(t, "", "add", "max-3", "--no-login")
	assert.Error(t, err)

	_, err = runAccount(t, "", "login", "default")
	assert.Error(t, err)
}

func TestAccountCmds_NamedAccountOpsStillWorkUnderAccountEnv(t *testing.T) {
	global := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := account.LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	t.Setenv("CLAUDE_CONFIG_DIR", acct.Dir)

	_, err = runAccount(t, "", "login", "max-2")
	require.NoError(t, err, "logging in the very account the shell runs as must not be blocked")

	_, err = runAccount(t, "", "use", "max-2")
	require.NoError(t, err)

	_, err = runAccount(t, "y\n", "remove", "max-2")
	require.NoError(t, err)
}
