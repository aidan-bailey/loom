package account

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	f := &recordingExec{out: []byte(authJSON)}

	id, err := AuthStatus("/nix/store/x/bin/claude --model opus", EnvFor("/acct/max-2"), f)

	require.NoError(t, err)
	assert.Equal(t, Identity{LoggedIn: true, AuthMethod: "claude.ai", Email: "you@example.com", OrgName: "Org", Plan: "max", ConfigDir: "/home/u/.claude"}, id)
	assert.Equal(t, []string{"/nix/store/x/bin/claude", "auth", "status"}, f.cmd.Args)
	dir, ok := envValue(f.cmd.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, "/acct/max-2", dir)
}

func TestAuthStatus_DefaultAccountInheritsLoomsEnv(t *testing.T) {
	f := &recordingExec{out: []byte(authJSON)}
	_, err := AuthStatus("claude", nil, f)
	require.NoError(t, err)
	assert.Nil(t, f.cmd.Env, "nil Env inherits loom's own environment unchanged")
}

func TestAuthStatus_LoggedOutStillDecodes(t *testing.T) {
	// Logged out, the CLI prints its JSON and exits non-zero.
	f := &recordingExec{out: []byte(`{"loggedIn":false,"authMethod":"none","configDirectory":"/acct/x"}`), err: errors.New("exit status 1")}
	id, err := AuthStatus("claude", EnvFor("/acct/x"), f)
	require.NoError(t, err)
	assert.False(t, id.LoggedIn)
	assert.Equal(t, "/acct/x", id.ConfigDir)
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
