package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedAccountExec answers `auth status`, which the login's report runs
// in this process.
type scriptedAccountExec struct{ auth string }

func (s *scriptedAccountExec) out(c *exec.Cmd) ([]byte, error) {
	if slices.Contains(c.Args, "status") {
		return []byte(s.auth), nil
	}
	return nil, errors.New("unexpected: " + strings.Join(c.Args, " "))
}
func (s *scriptedAccountExec) Run(c *exec.Cmd) error                      { _, err := s.out(c); return err }
func (s *scriptedAccountExec) Output(c *exec.Cmd) ([]byte, error)         { return s.out(c) }
func (s *scriptedAccountExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return s.out(c) }

const cliAuthJSON = `{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"/main"}`
const cliUsageJSON = `{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12},"seven_day":{"utilization":31}}}}}`

// accountsFixture is a test's global dir, the main Claude config dir its
// accounts link to, the one its fake Claude reports (main, or an unsafe
// one), the fake Claude's dir, and the daemon.
type accountsFixture struct {
	global, main, reported, bin string
	d                           *testDaemon
}

// fakeClaude writes the Claude the daemon runs for its auth reads and
// usage probes: `auth status` reports a logged-in identity whose config
// dir is CLAUDE_CONFIG_DIR, else reported (the default account's main
// dir); `-p` answers the usage request, after a while when a file named
// slow sits beside it.
func fakeClaude(t *testing.T, reported string) string {
	t.Helper()
	bin := t.TempDir()
	auth := `{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"'"${CLAUDE_CONFIG_DIR:-` + reported + `}"'"}`
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in\n" +
		"    status) printf '%s\\n' '" + auth + "'; exit 0;;\n" +
		"    -p) cat >/dev/null; [ -e '" + filepath.Join(bin, "slow") + "' ] && sleep 2; printf '%s\\n' '" + cliUsageJSON + "'; exit 0;;\n" +
		"  esac\n" +
		"done\n" +
		"exit 1\n"
	path := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// accountsEnv points the account commands at throwaway dirs, hermetic
// against the shell running the tests, with the daemon's fake Claude
// reporting the fixture's main dir as the main config dir, or, when
// unsafe, the global dir, which holds the accounts dir. The test lays out
// what the daemon boots from, then calls serve.
func accountsEnv(t *testing.T, unsafe bool) *accountsFixture {
	t.Helper()
	fx := &accountsFixture{global: t.TempDir(), main: t.TempDir()}
	t.Setenv("LOOM_GLOBAL_DIR", fx.global)
	t.Setenv("LOOM_HOME", t.TempDir())
	fx.reported = fx.main
	if unsafe {
		fx.reported = fx.global
	}
	// The default account's config dir is the main one the fake Claude
	// reports, and what account.MainDir falls back to: never the
	// developer's. Tests exercising guardAgainstAccountEnv set their own
	// value with t.Setenv, which layers cleanly over this.
	t.Setenv("CLAUDE_CONFIG_DIR", fx.reported)
	// Likewise for the credential-override vars: a developer's own
	// ANTHROPIC_API_KEY (say, set for other tools) must not make every
	// test here print an unexpected warning line. Tests exercising
	// the credential warnings set their own value with t.Setenv.
	for _, v := range account.CredentialOverrides {
		t.Setenv(v, "")
	}
	require.NoError(t, os.WriteFile(filepath.Join(fx.main, "CLAUDE.md"), []byte("x"), 0o600))
	claude := fakeClaude(t, fx.reported)
	fx.bin = filepath.Dir(claude)
	origLogin, origExec, origWait := accountLogin, accountExec, accountWait
	accountLogin = func(string, []string) error { return nil }
	accountExec = &scriptedAccountExec{auth: cliAuthJSON}
	accountWait = 5 * time.Second
	t.Cleanup(func() {
		accountLogin, accountExec, accountWait = origLogin, origExec, origWait
		accountNoLogin, accountForce, accountYes = false, false, false
	})
	return fx
}

// serve starts the daemon over the fixture's disk, running its fake
// Claude, and checks that the daemon learned its main config dir from it:
// a fallback would be no dir of the fixture's.
func (fx *accountsFixture) serve(t *testing.T) {
	t.Helper()
	fx.d = serveDaemon(t, filepath.Join(fx.bin, "claude"))
	require.Equal(t, fx.reported, fx.d.client.RCAuthFor(account.DefaultName).Identity.ConfigDir,
		"the daemon's main config dir is the fixture's")
}

// isolateAccounts is accountsEnv with its daemon serving.
func isolateAccounts(t *testing.T) *accountsFixture {
	t.Helper()
	fx := accountsEnv(t, false)
	fx.serve(t)
	return fx
}

// writeNew writes a new file at path, failing rather than following a link
// already there.
func writeNew(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// account is the daemon's account name, as it serves it now. Its links
// point into the fixture's main dir (linksIntoMain), which a test checks
// before it writes into the account dir.
func (fx *accountsFixture) account(t *testing.T, name string) (account.Account, bool) {
	t.Helper()
	require.NoError(t, fx.d.client.FlushForTest())
	fx.d.client.Sync()
	acct, ok := fx.d.client.Account(name)
	if ok {
		fx.linksIntoMain(t, acct)
	}
	return acct, ok
}

// linksIntoMain fails the test unless every link in acct's dir points into
// the fixture's main dir: a link elsewhere (the developer's ~/.claude) is
// one a test's write would follow.
func (fx *accountsFixture) linksIntoMain(t *testing.T, acct account.Account) {
	t.Helper()
	entries, err := os.ReadDir(acct.Dir)
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(acct.Dir, e.Name()))
		require.NoError(t, err)
		require.True(t, within(fx.main, target), "%s links to %s, outside the fixture's main dir %s", e.Name(), target, fx.main)
	}
}

// created registers account name on disk, linked against the main dir,
// before the daemon boots.
func (fx *accountsFixture) created(t *testing.T, name string) account.Account {
	t.Helper()
	acct, _, err := account.LoadRegistry(fx.global).Create(name, fx.main)
	require.NoError(t, err)
	fx.linksIntoMain(t, acct)
	return acct
}

// inUse stores a session on account name in the global workspace, before
// the daemon boots, which then serves it.
func (fx *accountsFixture) inUse(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(fx.global, "state.json"),
		[]byte(`{"instances":[{"title":"t","status":3,"program":"true","account":"`+name+`","worktree":{"worktree_path":"/tmp/wt-t"}}]}`), 0o644))
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

// `add --no-login` has the daemon create the account, linked against its
// main config dir, and runs no login.
func TestAccountAdd_NoLoginCreatesALinkedAccount(t *testing.T) {
	fx := isolateAccounts(t)
	accountLogin = func(string, []string) error { t.Error("--no-login ran a login"); return nil }

	out, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.NoError(t, err)
	assert.Contains(t, out, "Created")
	assert.Contains(t, out, "from "+fx.main)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok, "the daemon serves the new account")
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

// The daemon validates the main config dir it links a new account
// against (account.ValidateMainDir): one holding the accounts dir would
// link it into the account, and the add is refused with nothing created.
func TestAccountAdd_RefusesAnUnsafeMainDir(t *testing.T) {
	fx := accountsEnv(t, true)
	fx.serve(t)

	_, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.Error(t, err)
	_, ok := fx.account(t, "max-2")
	assert.False(t, ok)
	assert.NoDirExists(t, filepath.Join(fx.global, "accounts", "max-2"))
}

func TestAccountUse_SetsTheDefault(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	_, err = runAccount(t, "", "use", "max-2")

	require.NoError(t, err)
	require.NoError(t, fx.d.client.FlushForTest())
	assert.Equal(t, "max-2", fx.d.client.AccountNames().Default)

	_, err = runAccount(t, "", "use", "nope")
	assert.Error(t, err)
}

// `list` prints the usage the daemon probed for it.
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
	assert.NotContains(t, out, "as of", "the round list asked for landed")
}

// A round that does not land in time: the daemon's last sample is shown
// with its age.
func TestAccountList_ShowsAnOlderSampleWithItsAge(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_ = fx.d.client.FlushForTest()
		fx.d.client.Sync()
		u, _ := fx.d.client.AccountUsage("max-2")
		return u.Available
	}, 5*time.Second, 10*time.Millisecond, "fixture: the add's round lands")
	require.NoError(t, os.WriteFile(filepath.Join(fx.bin, "slow"), nil, 0o644))
	accountWait = 100 * time.Millisecond

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "12%")
	assert.Contains(t, out, "(as of <1m ago)")
}

func TestAccountList_ShowsAccountDirMissing(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(acct.Dir))

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, account.ErrAccountDirMissing.Error())
}

func TestAccountRemove_RefusedWhileInUse(t *testing.T) {
	fx := accountsEnv(t, false)
	fx.created(t, "max-2")
	fx.inUse(t, "max-2")
	fx.serve(t)

	_, err := runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 session(s) use max-2")
	_, ok := fx.account(t, "max-2")
	assert.True(t, ok)
}

// The daemon removes the account, which it stops serving, and deletes its
// dir.
func TestAccountRemove_ConfirmsAndDeletes(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)

	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Removed max-2")
	_, ok = fx.account(t, "max-2")
	assert.False(t, ok)
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAccountRemove_AbortsWithoutYes(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "\n", "remove", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	_, ok := fx.account(t, "max-2")
	assert.True(t, ok)
}

// TestAccountRemove_RefusedWithUnsharedFiles is the Amendments (binding)
// test: a real (non-symlink) file written into the account dir after
// creation makes plain `remove` refuse, answered "y" or not, and
// `--force` still deletes it.
func TestAccountRemove_RefusedWithUnsharedFiles(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	writeNew(t, filepath.Join(acct.Dir, "settings.json"))

	// The unshared check runs (and refuses) before the confirmation
	// prompt, so "y\n" is never even consumed: refused outright, nothing
	// printed to confirm.
	out, err := runAccount(t, "y\n", "remove", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "settings.json")
	assert.NotContains(t, out, "[y/N]")
	_, ok = fx.account(t, "max-2")
	assert.True(t, ok, "account must still be registered")
	assert.FileExists(t, filepath.Join(acct.Dir, "settings.json"))

	// --force overrides the unshared refusal but not the confirmation
	// prompt (that's --yes's job), so it still needs answering.
	out, err = runAccount(t, "y\n", "remove", "max-2", "--force")

	require.NoError(t, err)
	assert.Contains(t, out, "overriding unshared files: settings.json")
	_, statErr := os.Stat(acct.Dir)
	assert.True(t, os.IsNotExist(statErr))
	_, ok = fx.account(t, "max-2")
	assert.False(t, ok)
}

// --- Fix round: --yes/--force split (item 1), reordering (item 2) ---

func TestAccountRemove_YesStillRefusesInUse(t *testing.T) {
	fx := accountsEnv(t, false)
	fx.created(t, "max-2")
	fx.inUse(t, "max-2")
	fx.serve(t)

	_, err := runAccount(t, "", "remove", "max-2", "--yes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 session(s) use max-2")
	_, ok := fx.account(t, "max-2")
	assert.True(t, ok)
}

func TestAccountRemove_ForceWithoutYesStillPrompts(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)

	out, err := runAccount(t, "\n", "remove", "max-2", "--force")

	require.NoError(t, err)
	assert.Contains(t, out, "[y/N]")
	assert.Contains(t, out, "Aborted")
	_, ok := fx.account(t, "max-2")
	assert.True(t, ok, "aborting a forced removal must still keep the account")
}

// --force reaches the daemon, which checks the sessions it serves again
// and removes the account anyway.
func TestAccountRemove_ForceYesRemovesInUseAndReportsOverride(t *testing.T) {
	fx := accountsEnv(t, false)
	fx.created(t, "max-2")
	fx.inUse(t, "max-2")
	fx.serve(t)

	out, err := runAccount(t, "", "remove", "max-2", "--force", "--yes")

	require.NoError(t, err)
	assert.Contains(t, out, "overriding 1 session(s) using max-2")
	_, ok := fx.account(t, "max-2")
	assert.False(t, ok)
}

// --- A hand-edited registry pointing an account outside AccountsDir ---

// TestAccountRemove_RefusesWhenTheRegistryHoldsAForeignDir used to cover a
// "non-owned dir" prompt (item 3): remove would show a different message
// and only unregister, never delete, an account whose stored dir loom did
// not create. account.LoadRegistry now refuses to load such an entry at
// all — the stored dir is also what would be launched as
// CLAUDE_CONFIG_DIR, so catching it only at removal time was not enough —
// so every account command now fails closed on a hand-edited accounts.json
// like this, before the "non-owned dir" branch (still exercised directly
// against an in-memory account.Registry in account/link_test.go) is ever
// reached.
func TestAccountRemove_RefusesWhenTheRegistryHoldsAForeignDir(t *testing.T) {
	fx := accountsEnv(t, false)
	outside := t.TempDir()
	data := fmt.Sprintf(`{"accounts":[{"name":"byo","dir":%q}]}`, outside)
	require.NoError(t, os.WriteFile(filepath.Join(fx.global, "accounts.json"), []byte(data), 0o644))
	fx.serve(t)

	_, err := runAccount(t, "\n", "remove", "byo")

	require.Error(t, err)
	_, statErr := os.Stat(outside)
	assert.NoError(t, statErr, "a dir loom does not own must never be at risk from this command")
}

// --- Login (item 4) ---

func TestRunAccountLogin_ReturnsTheChildsResult(t *testing.T) {
	// Exercises the signal.Notify/signal.Stop wrapping (runWithChildSignals)
	// around the child without depending on real signal delivery timing:
	// `true`/`false` ignore the "auth login" args LoginCmd appends and just
	// report their own exit status, which must still come through
	// unaffected.
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no `true` binary on PATH")
	}
	assert.NoError(t, runAccountLogin("true", nil))

	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("no `false` binary on PATH")
	}
	assert.Error(t, runAccountLogin("false", nil))
}

// TestRunWithChildSignals_DoesNotLeakSignalIgnoreIntoTheChild is the fix
// for the review finding that signal.Ignore(os.Interrupt) sets SIG_IGN at
// the OS level, which (unlike a caught signal) survives exec: the login
// child would start with SIGINT permanently ignored, so Ctrl-C would
// reach neither loom nor claude. runWithChildSignals catches the signal
// (signal.Notify) instead, which POSIX resets to its default disposition
// across exec. /proc/self/status's SigIgn is a per-process bitmask, one
// bit per signal (bit N-1 for signal N; SIGINT is signal 2, so bit 1,
// value 0x2) — asserting it clear in a real child is a direct check of
// the OS-level disposition the reviewer verified was leaking.
func TestRunWithChildSignals_DoesNotLeakSignalIgnoreIntoTheChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc/self/status's SigIgn is Linux-specific")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no /bin/sh")
	}
	if _, err := exec.LookPath("grep"); err != nil {
		t.Skip("no grep")
	}

	var out bytes.Buffer
	c := exec.Command(sh, "-c", "grep SigIgn /proc/self/status")
	c.Stdout = &out
	require.NoError(t, runWithChildSignals(c))

	line := strings.TrimSpace(out.String())
	require.True(t, strings.HasPrefix(line, "SigIgn:"), "unexpected /proc/self/status line: %q", line)
	mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "SigIgn:")), 16, 64)
	require.NoError(t, err)
	assert.Zero(t, mask&0x2, "SIGINT (bit 2) must not be ignored in the login child")
}

func TestAccountAdd_FailedLoginKeepsTheAccountAndSaysHowToFinish(t *testing.T) {
	fx := isolateAccounts(t)
	accountLogin = func(string, []string) error { return errors.New("boom") }

	out, err := runAccount(t, "", "add", "max-2")

	require.Error(t, err)
	assert.Contains(t, out, "max-2 was created; finish with: loom account login max-2")
	_, ok := fx.account(t, "max-2")
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

// After a login the daemon rereads the account's auth, as after the TUI's:
// it serves the identity the login gave the account.
func TestAccountLogin_TheDaemonRereadsTheAccountsAuth(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)

	_, err = runAccount(t, "", "login", "max-2")

	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_ = fx.d.client.FlushForTest()
		fx.d.client.Sync()
		return fx.d.client.RCAuthFor("max-2").Identity.ConfigDir == acct.Dir
	}, 5*time.Second, 10*time.Millisecond)
}

// `login default` logs in the main config dir of the shell it runs in
// (its CLAUDE_CONFIG_DIR, else ~/.claude); when that is not the one the
// daemon runs its default account in, the login would go to a dir no
// session uses: it is refused, naming both.
func TestAccountLogin_DefaultRefusesAnotherMainDirThanTheDaemons(t *testing.T) {
	fx := isolateAccounts(t)
	elsewhere := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", elsewhere)
	accountLogin = func(string, []string) error { t.Error("logged in another main dir"); return nil }

	_, err := runAccount(t, "", "login", "default")

	require.Error(t, err)
	assert.Contains(t, err.Error(), elsewhere)
	assert.Contains(t, err.Error(), fx.main)
}

// The same main dir, by another spelling (a symlink to it), is the
// daemon's: the login runs.
func TestAccountLogin_DefaultRunsInTheDaemonsMainDir(t *testing.T) {
	fx := isolateAccounts(t)
	link := filepath.Join(t.TempDir(), "main")
	require.NoError(t, os.Symlink(fx.main, link))
	t.Setenv("CLAUDE_CONFIG_DIR", link)
	ran := false
	accountLogin = func(string, []string) error { ran = true; return nil }

	_, err := runAccount(t, "", "login", "default")

	require.NoError(t, err)
	assert.True(t, ran)
}

func TestAccountLogin_RefusesWhenTheAccountDirIsMissing(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	require.NoError(t, os.RemoveAll(acct.Dir))
	accountLogin = func(string, []string) error { t.Error("logged in to a missing dir"); return nil }

	_, err = runAccount(t, "", "login", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loom account remove max-2")
	assert.Contains(t, err.Error(), "loom account add max-2")
}

// TestAccountLogin_NamesAnUnexpectedStatError is the review's second fix:
// a stat failure that is not "not exist" (here, a parent dir with its
// execute bit stripped, so the account dir can't even be reached) must be
// reported as what it is, not misreported as the account's dir having
// been removed — the "remove, then add" advice would be actively wrong
// for a transient permissions problem.
func TestAccountLogin_NamesAnUnexpectedStatError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	parent := filepath.Dir(acct.Dir)
	require.NoError(t, os.Chmod(parent, 0o000))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err = runAccount(t, "", "login", "max-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking its config dir")
	assert.NotContains(t, err.Error(), "loom account remove", `a real stat error must not be misreported as "missing"`)
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
	assert.True(t, iDefault < iA && iA < iB, "rows must print in the daemon's AccountNames order")
}

// --- sync ---

// The daemon links nothing from a main dir that holds the accounts dir,
// and sync says why rather than print the last reports.
func TestAccountSync_RefusesAnUnsafeMainDir(t *testing.T) {
	fx := accountsEnv(t, true)
	acct := fx.created(t, "max-2")
	writeNew(t, filepath.Join(fx.main, "NEW.md"))
	fx.serve(t)

	_, err := runAccount(t, "", "sync")

	require.Error(t, err)
	_, err = os.Lstat(filepath.Join(acct.Dir, "NEW.md"))
	assert.True(t, os.IsNotExist(err), "nothing linked")
}

// sync has the daemon re-link every account and prints its reports; a
// second sync, with nothing new to link, still hears its refresh land.
func TestAccountSync_LinksNewEntriesForEveryAccount(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	writeNew(t, filepath.Join(fx.main, "NEW.md"))

	out, err := runAccount(t, "", "sync")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2: linked 1 new")
	_, err = os.Readlink(filepath.Join(acct.Dir, "NEW.md"))
	assert.NoError(t, err)
	require.NoError(t, fx.d.client.FlushForTest())
	rep, _ := fx.d.client.AccountSync("max-2")
	assert.Equal(t, []string{"NEW.md"}, rep.Linked, "the daemon's own report")

	out, err = runAccount(t, "", "sync")

	require.NoError(t, err)
	assert.Contains(t, out, "max-2: linked 0 new")
}

// --- CLAUDE_CONFIG_DIR leak from an account's own shell (item 7) ---

func TestAccountCmds_RefuseWhenRunningAsAnAccount(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	t.Setenv("CLAUDE_CONFIG_DIR", acct.Dir)

	_, err = runAccount(t, "", "list")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `account "max-2"`)

	_, err = runAccount(t, "", "sync")
	assert.Error(t, err)

	_, err = runAccount(t, "", "add", "max-3", "--no-login")
	assert.Error(t, err)
	_, ok = fx.account(t, "max-3")
	assert.False(t, ok)

	_, err = runAccount(t, "", "login", "default")
	assert.Error(t, err)
}

func TestAccountCmds_NamedAccountOpsStillWorkUnderAccountEnv(t *testing.T) {
	fx := isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	acct, ok := fx.account(t, "max-2")
	require.True(t, ok)
	t.Setenv("CLAUDE_CONFIG_DIR", acct.Dir)

	_, err = runAccount(t, "", "login", "max-2")
	require.NoError(t, err, "logging in the very account the shell runs as must not be blocked")

	_, err = runAccount(t, "", "use", "max-2")
	require.NoError(t, err)

	_, err = runAccount(t, "y\n", "remove", "max-2")
	require.NoError(t, err)
}

// --- Credential override warning (fix round part 2, item 1) ---

func TestAccountList_WarnsWhenACredentialOverrideIsSet(t *testing.T) {
	isolateAccounts(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $ANTHROPIC_API_KEY is set")
	assert.Contains(t, out, "NAME", "list must still print its table despite the warning")
}

func TestAccountAdd_WarnsWhenACredentialOverrideIsSet(t *testing.T) {
	isolateAccounts(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")

	out, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $CLAUDE_CODE_OAUTH_TOKEN is set")
	assert.Contains(t, out, "Created")
}

func TestAccountLogin_WarnsWhenACredentialOverrideIsSet(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok")

	out, err := runAccount(t, "", "login", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $ANTHROPIC_AUTH_TOKEN is set")
	assert.Contains(t, out, "Logged in max-2")
}

// add and list warn about the daemon's environment, which sessions run
// in, not this command's: a credential set only in the command's own
// shell overrides no session.
func TestAccountList_WarnsAboutTheDaemonsCredentialOverride(t *testing.T) {
	isolateAccounts(t)
	stubLocalOverride(t, "", false)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $ANTHROPIC_API_KEY is set in the loom daemon's environment")

	t.Setenv("ANTHROPIC_API_KEY", "")
	stubLocalOverride(t, "ANTHROPIC_API_KEY", true)

	out, err = runAccount(t, "", "list")

	require.NoError(t, err)
	assert.NotContains(t, out, "warning:", "the command's own environment runs no session")
}

func TestAccountAdd_WarnsAboutTheDaemonsCredentialOverride(t *testing.T) {
	isolateAccounts(t)
	stubLocalOverride(t, "", false)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")

	out, err := runAccount(t, "", "add", "max-2", "--no-login")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $CLAUDE_CODE_OAUTH_TOKEN is set in the loom daemon's environment")
}

// login warns about its own environment, which its claude runs in.
func TestAccountLogin_WarnsAboutItsOwnCredentialOverride(t *testing.T) {
	isolateAccounts(t)
	_, err := runAccount(t, "", "add", "max-2", "--no-login")
	require.NoError(t, err)
	stubLocalOverride(t, "ANTHROPIC_AUTH_TOKEN", true)

	out, err := runAccount(t, "", "login", "max-2")

	require.NoError(t, err)
	assert.Contains(t, out, "warning: $ANTHROPIC_AUTH_TOKEN is set —")
}

// stubLocalOverride makes this command's own environment name name (ok)
// as its credential override, until the test ends.
func stubLocalOverride(t *testing.T, name string, ok bool) {
	t.Helper()
	prev := localCredentialOverride
	localCredentialOverride = func() (string, bool) { return name, ok }
	t.Cleanup(func() { localCredentialOverride = prev })
}

func TestAccountList_NoWarningWhenNoCredentialOverrideIsSet(t *testing.T) {
	isolateAccounts(t)

	out, err := runAccount(t, "", "list")

	require.NoError(t, err)
	assert.NotContains(t, out, "warning:")
}

// --- claudeProgram reads the global config dir, not LOOM_HOME (part 2, item 2) ---

// TestClaudeProgram_PrefersTheGlobalConfigDirOverLoomHome is a direct unit
// test of the fix: config.GetGlobalConfigDir() (LOOM_GLOBAL_DIR) is where
// the account registry and the TUI's classic/global context both read
// config.json from, not config.GetConfigDir() (LOOM_HOME) — a distinct
// var that can be set to something else entirely (a workspace-mode
// developer setup, say). The two dirs here hold different programs so a
// regression (reading LOOM_HOME) is unambiguous, not just "returns
// something".
func TestClaudeProgram_PrefersTheGlobalConfigDirOverLoomHome(t *testing.T) {
	global, home := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(global, "config.json"),
		[]byte(`{"default_program":"/opt/claude-custom/claude --model opus"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"default_program":"/opt/wrong/claude"}`), 0o644))

	assert.Equal(t, "/opt/claude-custom/claude --model opus", claudeProgram())
}

// TestAccountLogin_UsesTheProgramConfiguredInTheGlobalDir exercises the
// same fix through the CLI: accountsEnv (like every fixture in this file)
// points LOOM_HOME at a directory distinct from LOOM_GLOBAL_DIR, which is
// exactly the configuration the review found broken — a config.json
// written under the global dir must be the one the login runs.
func TestAccountLogin_UsesTheProgramConfiguredInTheGlobalDir(t *testing.T) {
	fx := isolateAccounts(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.global, "config.json"),
		[]byte(`{"default_program":"/opt/claude-custom/claude --model opus"}`), 0o644))
	var gotProgram string
	accountLogin = func(p string, _ []string) error { gotProgram = p; return nil }

	_, err := runAccount(t, "", "login", "default")

	require.NoError(t, err)
	assert.Equal(t, "/opt/claude-custom/claude --model opus", gotProgram)
}
