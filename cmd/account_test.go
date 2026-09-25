package cmd

import (
	"bytes"
	"errors"
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
	require.NoError(t, os.WriteFile(filepath.Join(main, "CLAUDE.md"), []byte("x"), 0o600))
	origMain, origLogin, origExec := accountMainDir, accountLogin, accountExec
	accountMainDir = func(string) string { return main }
	accountLogin = func(string, []string) error { return nil }
	accountExec = &scriptedAccountExec{auth: cliAuthJSON, usage: cliUsageJSON}
	t.Cleanup(func() {
		accountMainDir, accountLogin, accountExec = origMain, origLogin, origExec
		accountNoLogin, accountForce = false, false
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

	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, out, "")
	_, ok = account.LoadRegistry(global).Get("max-2")
	assert.True(t, ok, "account must still be registered")
	assert.FileExists(t, filepath.Join(acct.Dir, "settings.json"))

	_, err = runAccount(t, "", "remove", "max-2", "--force")

	require.NoError(t, err)
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
	_, ok = account.LoadRegistry(global).Get("max-2")
	assert.False(t, ok)
}
