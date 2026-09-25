package account

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommand_RefusesEmptyProgram(t *testing.T) {
	c, err := Command(context.Background(), "", nil, "auth", "status")
	assert.Nil(t, c)
	assert.EqualError(t, err, "no claude program configured")
}

func TestCommand_RefusesAMissingAccountDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	c, err := Command(context.Background(), "claude", EnvFor(gone), "auth", "status")
	assert.Nil(t, c)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountDirMissing)
}

func TestCommand_BuildsUnderTheAccountEnv(t *testing.T) {
	dir := t.TempDir()
	c, err := Command(context.Background(), "/nix/store/x/bin/claude --model opus", EnvFor(dir), "auth", "status")
	require.NoError(t, err)
	assert.Equal(t, []string{"/nix/store/x/bin/claude", "auth", "status"}, c.Args)
	got, ok := envValue(c.Env, "CLAUDE_CONFIG_DIR")
	assert.True(t, ok)
	assert.Equal(t, dir, got)
}

func TestCommand_DefaultAccountInheritsLoomsEnv(t *testing.T) {
	c, err := Command(context.Background(), "claude", nil, "agents", "--json")
	require.NoError(t, err)
	assert.Nil(t, c.Env, "nil Env inherits loom's own environment unchanged")
}
