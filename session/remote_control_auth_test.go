package session

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/stretchr/testify/assert"
)

// fakeAuthExecutor returns canned output/err for `auth status` and records
// the argv it was asked to run.
type fakeAuthExecutor struct {
	out     []byte
	err     error
	gotArgs []string
	called  bool
}

func (f *fakeAuthExecutor) Run(c *exec.Cmd) error { return f.err }
func (f *fakeAuthExecutor) Output(c *exec.Cmd) ([]byte, error) {
	f.called = true
	f.gotArgs = c.Args
	return f.out, f.err
}
func (f *fakeAuthExecutor) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.out, f.err }

// clearOverrideEnv neutralizes any ambient API-key env vars so exec-based
// cases aren't short-circuited to Blocked.
func clearOverrideEnv(t *testing.T) {
	t.Helper()
	for _, env := range remoteControlOverrideEnv {
		t.Setenv(env, "")
	}
}

func TestIsClaudeProgram(t *testing.T) {
	assert.True(t, IsClaudeProgram("claude"))
	assert.True(t, IsClaudeProgram("claude --model sonnet"))
	assert.True(t, IsClaudeProgram("/usr/bin/claude"))
	assert.False(t, IsClaudeProgram("aider"))
	assert.False(t, IsClaudeProgram("claudette"))
	assert.False(t, IsClaudeProgram(""))
}

func TestDetectClaudeRemoteControlAuth(t *testing.T) {
	cases := []struct {
		name      string
		program   string
		out       string
		err       error
		wantState RemoteControlAuthState
		wantExec  bool
	}{
		{"non-claude skips detection", "aider --model x", "", nil, RemoteControlAuthUnknown, false},
		{"logged in via claude.ai", "claude", `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`, nil, RemoteControlAuthOK, true},
		{"not logged in", "claude", `{"loggedIn":false,"authMethod":""}`, errors.New("exit 1"), RemoteControlAuthBlocked, true},
		{"console account", "claude", `{"loggedIn":true,"authMethod":"console"}`, nil, RemoteControlAuthBlocked, true},
		{"unparseable output", "claude", "not json", nil, RemoteControlAuthUnknown, true},
		{"subcommand missing (err, no stdout)", "claude", "", errors.New("unknown command"), RemoteControlAuthUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearOverrideEnv(t)
			fake := &fakeAuthExecutor{out: []byte(tc.out), err: tc.err}
			got := DetectClaudeRemoteControlAuth(tc.program, fake)
			assert.Equal(t, tc.wantState, got.State)
			assert.Equal(t, tc.wantExec, fake.called)
			if tc.wantExec {
				assert.Contains(t, fake.gotArgs, "auth")
				assert.Contains(t, fake.gotArgs, "status")
			}
			if got.State == RemoteControlAuthBlocked {
				assert.NotEmpty(t, got.Reason, "blocked result must carry a reason")
			}
		})
	}
}

func TestDetectClaudeRemoteControlAuth_EnvOverride(t *testing.T) {
	for _, env := range remoteControlOverrideEnv {
		t.Run(env, func(t *testing.T) {
			clearOverrideEnv(t)
			t.Setenv(env, "sk-secret")
			fake := &fakeAuthExecutor{out: []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`)}
			got := DetectClaudeRemoteControlAuth("claude", fake)
			assert.Equal(t, RemoteControlAuthBlocked, got.State)
			assert.Contains(t, got.Reason, env)
			assert.False(t, fake.called, "env override should short-circuit before running auth status")
		})
	}
}

func TestDetectClaudeRemoteControlAuthEnv_CarriesTheIdentity(t *testing.T) {
	clearOverrideEnv(t)
	dir := t.TempDir()
	f := &envRecordingExec{out: []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"you@example.com","subscriptionType":"max","configDirectory":"/acct/max-2"}`)}

	got := DetectClaudeRemoteControlAuthEnv("claude", account.EnvFor(dir), f)

	assert.True(t, got.OK())
	assert.Equal(t, "you@example.com", got.Identity.Email)
	assert.Equal(t, "/acct/max-2", got.Identity.ConfigDir)
	assert.Contains(t, f.env, "CLAUDE_CONFIG_DIR="+dir)
}
