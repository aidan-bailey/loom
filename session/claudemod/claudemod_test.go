package claudemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTo_WritesTheWholeMod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks", "mod")

	require.NoError(t, WriteTo(dir))

	for _, f := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts"} {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)))
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	var manifest struct{ Name, Version string }
	require.NoError(t, json.Unmarshal(b, &manifest))
	assert.Equal(t, "loom", manifest.Name)
	assert.Equal(t, fmt.Sprint(Version), strings.SplitN(manifest.Version, ".", 2)[0], "the manifest's major version is the protocol's")
}

// The module and the Go side speak one protocol: the version its hello says,
// the API path, and the environment a launch gives it.
func TestModule_SpeaksThisProtocol(t *testing.T) {
	b, err := files.ReadFile("mod/hooks/register.ts")
	require.NoError(t, err)
	src := string(b)
	assert.Contains(t, src, fmt.Sprintf("const MOD_VERSION = %d", Version))
	assert.Contains(t, src, fmt.Sprintf("'http://loom/v%d'", Version))
	for _, env := range []string{EnvSocket, EnvLaunch} {
		assert.Contains(t, src, "'"+env+"'")
	}
}
