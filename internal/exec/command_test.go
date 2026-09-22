package exec

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lastEnv returns the value of the last key=value entry for key in env —
// the one os/exec actually passes to the child when a key repeats.
func lastEnv(env []string, key string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			val, found = v, true
		}
	}
	return val, found
}

// keysOf returns the variable names in env, in order.
func keysOf(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	return keys
}

func TestGitEnv_LCAllIsSpreadOverTheOtherCategories(t *testing.T) {
	in := []string{"PATH=/bin", "LC_CTYPE=fr_FR.UTF-8", "LC_ALL=de_DE.UTF-8", "LANG=en_US.UTF-8"}
	orig := append([]string(nil), in...)

	env := gitEnv(in)

	assert.Equal(t, orig, in, "environ must not be modified")
	assert.NotContains(t, keysOf(env), "LC_ALL")
	for _, cat := range localeCategories {
		v, ok := lastEnv(env, cat)
		require.True(t, ok, "%s must be pinned", cat)
		assert.Equal(t, "de_DE.UTF-8", v, "%s must carry the user's LC_ALL, which outranked their own %s", cat, cat)
	}
	assert.Contains(t, env, "PATH=/bin")
	assert.Contains(t, env, "LANG=en_US.UTF-8", "LANG is left alone")
	assert.NotContains(t, env, "LC_CTYPE=fr_FR.UTF-8", "LC_ALL was overriding it, so it must not resurface")
	assert.Equal(t, "LC_MESSAGES=C", env[len(env)-1])
}

func TestGitEnv_LangAndMessagesWithoutLCAll(t *testing.T) {
	env := gitEnv([]string{"LANG=de_DE.UTF-8", "LC_MESSAGES=de_DE.UTF-8"})

	assert.Equal(t, []string{"LANG=de_DE.UTF-8", "LC_MESSAGES=C"}, env,
		"LANG untouched, no categories pinned, LC_MESSAGES forced to C")
}

func TestGitEnv_DropsLanguage(t *testing.T) {
	env := gitEnv([]string{"LANGUAGE=de:fr", "LANG=de_DE.UTF-8"})

	assert.NotContains(t, keysOf(env), "LANGUAGE")
	assert.Equal(t, "LC_MESSAGES=C", env[len(env)-1])
}

func TestGitEnv_NoLocaleVars(t *testing.T) {
	env := gitEnv([]string{"PATH=/bin", "HOME=/home/u"})

	assert.Equal(t, []string{"PATH=/bin", "HOME=/home/u", "LC_MESSAGES=C"}, env)
}

func TestGitEnv_EmptyLCAllPinsNothing(t *testing.T) {
	// An empty LC_ALL counts as unset, so the user's own LC_CTYPE was in
	// effect and must survive unchanged.
	env := gitEnv([]string{"LC_ALL=", "LC_CTYPE=ja_JP.UTF-8"})

	assert.Equal(t, []string{"LC_CTYPE=ja_JP.UTF-8", "LC_MESSAGES=C"}, env)
}

func TestGitCommand_AddsDirAndForcesMessages(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	args := []string{"branch", "-D", "x"}

	c := GitCommand(context.Background(), "/repo", args...)

	assert.Equal(t, []string{"git", "-C", "/repo", "branch", "-D", "x"}, c.Args)
	assert.Equal(t, []string{"branch", "-D", "x"}, args, "caller's args must not be modified")
	assert.NotContains(t, keysOf(c.Env), "LC_ALL")
	v, _ := lastEnv(c.Env, "LC_CTYPE")
	assert.Equal(t, "de_DE.UTF-8", v, "the user's character set is kept")
	assert.Equal(t, "LC_MESSAGES=C", c.Env[len(c.Env)-1])
}

func TestGitCommand_EmptyDirAddsNoFlag(t *testing.T) {
	c := GitCommand(context.Background(), "", "rev-parse", "HEAD")

	assert.Equal(t, []string{"git", "rev-parse", "HEAD"}, c.Args)
	assert.Equal(t, "LC_MESSAGES=C", c.Env[len(c.Env)-1])
}

func TestGitCommand_CallerEnvSurvives(t *testing.T) {
	c := GitCommand(context.Background(), "/repo", "write-tree")
	c.Env = append(c.Env, "GIT_INDEX_FILE=/tmp/idx")

	v, ok := lastEnv(c.Env, "GIT_INDEX_FILE")
	require.True(t, ok)
	assert.Equal(t, "/tmp/idx", v)
	v, _ = lastEnv(c.Env, "LC_MESSAGES")
	assert.Equal(t, "C", v, "appending caller env must not displace LC_MESSAGES=C")
}

// TestGitCommand_ChildProcessesKeepCharset runs a real git and has it spawn
// a shell (a `!` alias, standing in for a hook or filter) that prints its
// environment: the child must see the user's LC_ALL spread over LC_CTYPE,
// with only LC_MESSAGES forced. The values need not be installed locales —
// they are only passed through.
func TestGitCommand_ChildProcessesKeepCharset(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")

	c := GitCommand(context.Background(), t.TempDir(), "-c", "alias.envdump=!env", "envdump")
	out, err := c.Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	assert.Contains(t, lines, "LC_CTYPE=de_DE.UTF-8")
	assert.Contains(t, lines, "LC_COLLATE=de_DE.UTF-8")
	assert.Contains(t, lines, "LC_MESSAGES=C")
	keys := keysOf(lines)
	assert.NotContains(t, keys, "LC_ALL")
	assert.NotContains(t, keys, "LANGUAGE")
}

func TestGhCommand_DisablesPromptsAndUpdateNotifier(t *testing.T) {
	t.Setenv("GH_PROMPT_DISABLED", "")
	c := GhCommand(context.Background(), "auth", "status")

	assert.Equal(t, []string{"gh", "auth", "status"}, c.Args)
	v, ok := lastEnv(c.Env, "GH_PROMPT_DISABLED")
	require.True(t, ok)
	assert.Equal(t, "1", v)
	v, ok = lastEnv(c.Env, "GH_NO_UPDATE_NOTIFIER")
	require.True(t, ok)
	assert.Equal(t, "1", v)
}
