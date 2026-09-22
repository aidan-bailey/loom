package exec

import (
	"context"
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

func TestGitCommand_AddsDirAndForcesCLocale(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	args := []string{"branch", "-D", "x"}

	c := GitCommand(context.Background(), "/repo", args...)

	assert.Equal(t, []string{"git", "-C", "/repo", "branch", "-D", "x"}, c.Args)
	assert.Equal(t, []string{"branch", "-D", "x"}, args, "caller's args must not be modified")
	v, ok := lastEnv(c.Env, "LC_ALL")
	require.True(t, ok, "LC_ALL must be set")
	assert.Equal(t, "C", v, "LC_ALL=C must outrank the user's exported LC_ALL")
	assert.Contains(t, c.Env, "LC_ALL=de_DE.UTF-8", "the rest of the environment is inherited")
}

func TestGitCommand_EmptyDirAddsNoFlag(t *testing.T) {
	c := GitCommand(context.Background(), "", "rev-parse", "HEAD")

	assert.Equal(t, []string{"git", "rev-parse", "HEAD"}, c.Args)
	v, _ := lastEnv(c.Env, "LC_ALL")
	assert.Equal(t, "C", v)
}

func TestGitCommand_CallerEnvSurvives(t *testing.T) {
	c := GitCommand(context.Background(), "/repo", "write-tree")
	c.Env = append(c.Env, "GIT_INDEX_FILE=/tmp/idx")

	v, ok := lastEnv(c.Env, "GIT_INDEX_FILE")
	require.True(t, ok)
	assert.Equal(t, "/tmp/idx", v)
	v, _ = lastEnv(c.Env, "LC_ALL")
	assert.Equal(t, "C", v, "appending caller env must not displace LC_ALL=C")
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
