package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAtomicWriteFile_WritesContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	err := AtomicWriteFile(path, []byte(`{"ok":true}`), 0644)
	assert.NoError(t, err)

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(data))
}

func TestAtomicWriteFile_LeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	assert.NoError(t, AtomicWriteFile(path, []byte(`x`), 0644))

	entries, err := os.ReadDir(dir)
	assert.NoError(t, err)
	assert.Len(t, entries, 1, "only the final file should remain")
	assert.Equal(t, "state.json", entries[0].Name())
}

func TestAtomicWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	assert.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	assert.NoError(t, AtomicWriteFile(path, []byte("new"), 0644))

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "new", string(data))
}
