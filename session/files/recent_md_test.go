package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("# x\n"), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

func TestMostRecentMarkdown_PicksNewest(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(root, "old.md"), base)
	touch(t, filepath.Join(root, "docs", "new.md"), base.Add(30*time.Minute))
	touch(t, filepath.Join(root, "not-md.txt"), base.Add(50*time.Minute))

	path, mtime, ok, err := MostRecentMarkdown(root)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, "docs", "new.md"), path)
	assert.WithinDuration(t, base.Add(30*time.Minute), mtime, time.Second)
}

func TestMostRecentMarkdown_SkipsPrunedDirs(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(root, "real.md"), base)
	touch(t, filepath.Join(root, ".git", "hidden.md"), base.Add(time.Minute))
	touch(t, filepath.Join(root, "node_modules", "pkg", "README.md"), base.Add(2*time.Minute))
	touch(t, filepath.Join(root, "vendor", "dep.md"), base.Add(3*time.Minute))

	path, _, ok, err := MostRecentMarkdown(root)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, "real.md"), path)
}

func TestMostRecentMarkdown_EmptyTree(t *testing.T) {
	_, _, ok, err := MostRecentMarkdown(t.TempDir())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMostRecentMarkdown_EmptyRootRejected(t *testing.T) {
	_, _, _, err := MostRecentMarkdown("")
	assert.Error(t, err)
}
