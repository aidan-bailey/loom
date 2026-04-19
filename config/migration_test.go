package config

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written. Used to verify the pre-init stderr notices
// emitted by MigrateLegacyHome.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	require.NoError(t, w.Close())
	os.Stderr = original
	return <-done
}

func withTempHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("HOME override via $HOME only works on unix")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_HOME", "")
	t.Setenv("CLAUDE_SQUAD_HOME", "")
	return home
}

func TestMigrateLegacyHome_RenamesLegacyToLoom(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claude-squad")
	newDir := filepath.Join(home, ".loom")

	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "worktrees"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "marker.txt"), []byte("payload"), 0o644))

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.NoDirExists(t, legacy, "legacy dir should be gone after rename")
	assert.DirExists(t, newDir)
	assert.DirExists(t, filepath.Join(newDir, "worktrees"), "subdir should survive rename")

	data, err := os.ReadFile(filepath.Join(newDir, "marker.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload", string(data))

	assert.Contains(t, stderr, "migrated")
	assert.Contains(t, stderr, legacy)
	assert.Contains(t, stderr, newDir)
}

func TestMigrateLegacyHome_IdempotentWhenNewExists(t *testing.T) {
	home := withTempHome(t)
	newDir := filepath.Join(home, ".loom")
	require.NoError(t, os.MkdirAll(newDir, 0o755))

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.Empty(t, strings.TrimSpace(stderr), "no notice should be emitted when only new dir exists")
	assert.DirExists(t, newDir)
}

func TestMigrateLegacyHome_NoLegacyIsNoop(t *testing.T) {
	home := withTempHome(t)

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.Empty(t, strings.TrimSpace(stderr))
	assert.NoDirExists(t, filepath.Join(home, ".loom"))
	assert.NoDirExists(t, filepath.Join(home, ".claude-squad"))
}

func TestMigrateLegacyHome_BothPresentWarnsAndPreservesBoth(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claude-squad")
	newDir := filepath.Join(home, ".loom")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "a.txt"), []byte("legacy"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "b.txt"), []byte("new"), 0o644))

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.DirExists(t, legacy, "legacy dir must not be removed when both exist")
	assert.DirExists(t, newDir)
	assert.FileExists(t, filepath.Join(legacy, "a.txt"))
	assert.FileExists(t, filepath.Join(newDir, "b.txt"))
	assert.Contains(t, stderr, "both")
	assert.Contains(t, stderr, legacy)
	assert.Contains(t, stderr, newDir)
}

func TestMigrateLegacyHome_SkipsWhenLoomHomeSet(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claude-squad")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	t.Setenv("LOOM_HOME", t.TempDir())

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.Empty(t, strings.TrimSpace(stderr))
	assert.DirExists(t, legacy, "legacy dir must survive when LOOM_HOME is set")
	assert.NoDirExists(t, filepath.Join(home, ".loom"))
}

func TestMigrateLegacyHome_SkipsWhenClaudeSquadHomeSet(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claude-squad")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	t.Setenv("CLAUDE_SQUAD_HOME", t.TempDir())

	stderr := captureStderr(t, func() {
		require.NoError(t, MigrateLegacyHome())
	})

	assert.Empty(t, strings.TrimSpace(stderr))
	assert.DirExists(t, legacy)
	assert.NoDirExists(t, filepath.Join(home, ".loom"))
}
