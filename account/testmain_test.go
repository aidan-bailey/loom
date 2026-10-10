package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain points LOOM_HOME, LOOM_GLOBAL_DIR and CLAUDE_CONFIG_DIR at
// throwaway directories, so no test here can resolve the developer's real
// ~/.loom or ~/.claude. Tests that need directories of their own still
// t.Setenv over them.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// TestMainDir_IsTheIsolatedOneInTests: under the isolation every
// config-reaching test binary runs with (testenv.IsolateLoomDirs), the
// main config dir MainDir falls back to when the default account's
// identity is unknown is the throwaway one, never under the developer's
// home: an account linked against ~/.claude once let a test's write into
// the account dir land in the developer's settings.json.
func TestMainDir_IsTheIsolatedOneInTests(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	main := MainDir(Identity{})

	assert.Equal(t, os.Getenv(testenv.EnvClaudeConfigDir), main, "MainDir reads the variable testenv isolates")
	for _, real := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".loom")} {
		rel, err := filepath.Rel(real, main)
		assert.True(t, err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)),
			"MainDir %s is under the real %s", main, real)
	}
}
