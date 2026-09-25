package account

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeState(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o644))
}

func TestCountUsers(t *testing.T) {
	a, b, missing := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "none")
	writeState(t, a, `{"instances":[{"title":"x","account":"max-2"},{"title":"y"}]}`)
	writeState(t, b, `{"instances":[{"title":"z","account":"max-2"}]}`)

	n, err := CountUsers([]string{a, b, missing, a}, "max-2")

	require.NoError(t, err)
	assert.Equal(t, 2, n, "a missing file counts zero and a repeated dir counts once")
}

func TestCountUsers_CorruptStateIsAnError(t *testing.T) {
	a := t.TempDir()
	writeState(t, a, "not json")
	_, err := CountUsers([]string{a}, "max-2")
	assert.Error(t, err, "in-use must not be answered with a guess")
	data, _ := os.ReadFile(filepath.Join(a, "state.json"))
	assert.Equal(t, "not json", string(data), "read-only: never quarantined")
}

func TestKnownStateDirs(t *testing.T) {
	global, home := t.TempDir(), t.TempDir()
	t.Setenv("LOOM_GLOBAL_DIR", global)
	t.Setenv("LOOM_HOME", home)
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(global, "workspaces.json"),
		[]byte(`{"workspaces":[{"name":"r","path":"`+repo+`"}]}`), 0o644))

	dirs, err := KnownStateDirs()

	require.NoError(t, err)
	assert.Equal(t, []string{global, home, filepath.Join(repo, ".loom")}, dirs)
}
