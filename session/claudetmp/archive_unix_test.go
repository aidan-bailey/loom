//go:build unix

package claudetmp

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArchive_SkipsFIFOs: opening a FIFO would block the archive forever.
func TestArchive_SkipsFIFOs(t *testing.T) {
	src := filepath.Join(t.TempDir(), "proj")
	writeTree(t, src, map[string]string{"s1/scratchpad/a.txt": "a"})
	require.NoError(t, syscall.Mkfifo(filepath.Join(src, "s1", "pipe"), 0o600))
	zipPath := filepath.Join(t.TempDir(), "proj.zip")

	_, err := Archive(src, zipPath, Manifest{})

	require.NoError(t, err)
	entries := zipEntries(t, zipPath)
	assert.Contains(t, entries, "s1/scratchpad/a.txt")
	assert.NotContains(t, entries, "s1/pipe")
}
