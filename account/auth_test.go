package account

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExec answers every command with out/err and records the last
// one, including what it would have read on stdin.
type recordingExec struct {
	out   []byte
	err   error
	cmd   *exec.Cmd
	stdin string
}

func (f *recordingExec) record(c *exec.Cmd) {
	f.cmd = c
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		f.stdin = string(b)
	}
}
func (f *recordingExec) Run(c *exec.Cmd) error                      { f.record(c); return f.err }
func (f *recordingExec) Output(c *exec.Cmd) ([]byte, error)         { f.record(c); return f.out, f.err }
func (f *recordingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { f.record(c); return f.out, f.err }

// sleepingExec blocks for sleep before answering, so a caller's own
// context (given a short enough timeout) is already past its deadline by
// the time Output returns — without needing the fake to be context-aware
// itself.
type sleepingExec struct {
	sleep time.Duration
	err   error
}

func (f *sleepingExec) Run(c *exec.Cmd) error { time.Sleep(f.sleep); return f.err }
func (f *sleepingExec) Output(c *exec.Cmd) ([]byte, error) {
	time.Sleep(f.sleep)
	return nil, f.err
}
func (f *sleepingExec) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	time.Sleep(f.sleep)
	return nil, f.err
}

// envValue returns key's effective value in env: the last one, as os/exec
// resolves duplicates.
func envValue(env []string, key string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			val, found = strings.TrimPrefix(kv, key+"="), true
		}
	}
	return val, found
}

// authJSON is `claude auth status` output from Claude Code 2.1.281.
const authJSON = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","analyticsDisabled":false,"projectsDirectory":"/home/u/.claude/projects","configDirectory":"/home/u/.claude","email":"you@example.com","orgId":"o","orgName":"Org","subscriptionType":"max"}`

func TestAuthStatus_DecodesTheIdentityUnderTheAccountEnv(t *testing.T) {
	dir := t.TempDir()
	f := &recordingExec{out: []byte(authJSON)}

	id, err := AuthStatus("/nix/store/x/bin/claude --model opus", EnvFor(dir), f)

	require.NoError(t, err)
	assert.Equal(t, Identity{LoggedIn: true, AuthMethod: "claude.ai", Email: "you@example.com", OrgName: "Org", Plan: "max", ConfigDir: "/home/u/.claude"}, id)
	assert.Equal(t, []string{"/nix/store/x/bin/claude", "auth", "status"}, f.cmd.Args)
	got, ok := envValue(f.cmd.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, dir, got)
	assert.Equal(t, execWaitDelay, f.cmd.WaitDelay, "bounds a grandchild holding stdout open past the context deadline")
}

func TestAuthStatus_DefaultAccountInheritsLoomsEnv(t *testing.T) {
	f := &recordingExec{out: []byte(authJSON)}
	_, err := AuthStatus("claude", nil, f)
	require.NoError(t, err)
	assert.Nil(t, f.cmd.Env, "nil Env inherits loom's own environment unchanged")
}

func TestAuthStatus_LoggedOutStillDecodes(t *testing.T) {
	dir := t.TempDir()
	// Logged out, the CLI prints its JSON and exits non-zero.
	f := &recordingExec{out: []byte(`{"loggedIn":false,"authMethod":"none","configDirectory":"/acct/x"}`), err: errors.New("exit status 1")}
	id, err := AuthStatus("claude", EnvFor(dir), f)
	require.NoError(t, err)
	assert.False(t, id.LoggedIn)
	assert.Equal(t, "/acct/x", id.ConfigDir)
}

func TestAuthStatus_RefusesAMissingAccountDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	f := &recordingExec{out: []byte(authJSON)}

	_, err := AuthStatus("claude", EnvFor(gone), f)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountDirMissing)
	assert.Nil(t, f.cmd, "must not run claude against a dir the CLI would recreate fresh")
}

func TestAuthStatus_TimeoutIsReportedClearly(t *testing.T) {
	orig := authTimeout
	authTimeout = 10 * time.Millisecond
	t.Cleanup(func() { authTimeout = orig })

	_, err := AuthStatus("claude", nil, &sleepingExec{sleep: 60 * time.Millisecond, err: errors.New("signal: killed")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 10ms")
}

func TestAuthStatus_Failures(t *testing.T) {
	_, err := AuthStatus("", nil, &recordingExec{})
	assert.Error(t, err, "no program")
	_, err = AuthStatus("claude", nil, &recordingExec{err: errors.New("not found")})
	assert.Error(t, err, "failed with no output")
	_, err = AuthStatus("claude", nil, &recordingExec{out: []byte("not json")})
	assert.Error(t, err, "unparseable output")
}

func TestLoginCmd(t *testing.T) {
	c := LoginCmd("claude --model opus", EnvFor("/acct/max-2"))
	assert.Equal(t, []string{"claude", "auth", "login"}, c.Args)
	dir, ok := envValue(c.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, "/acct/max-2", dir)
}

func TestBinary(t *testing.T) {
	assert.Equal(t, "/nix/store/x/bin/claude", Binary("/nix/store/x/bin/claude --model opus"))
	assert.Equal(t, "", Binary("  "))
}

func TestMainDir(t *testing.T) {
	assert.Equal(t, "/reported", MainDir(Identity{ConfigDir: "/reported"}))

	t.Setenv("CLAUDE_CONFIG_DIR", "/from-env")
	assert.Equal(t, "/from-env", MainDir(Identity{}))

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".claude"), MainDir(Identity{}))
}
