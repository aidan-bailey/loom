package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteLoomContextFiles(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))

	wt := filepath.Join(dir, "claude-loom-context.md")
	ws := filepath.Join(dir, "claude-loom-context-workspace.md")
	for _, p := range []string{wt, ws} {
		b, err := os.ReadFile(p)
		assert.NoError(t, err)
		assert.Contains(t, string(b), "Loom session context")
	}

	// skip-if-current: a second write with already-current content must not
	// rewrite the file. AtomicWriteFile renames a fresh temp into place, so a
	// real rewrite yields a new inode regardless of mtime resolution; assert
	// via os.SameFile (inode-robust) plus an mtime-unchanged signal.
	before, err := os.Stat(ws)
	assert.NoError(t, err)
	assert.NoError(t, WriteLoomContextFiles(dir))
	after, err := os.Stat(ws)
	assert.NoError(t, err)
	assert.True(t, os.SameFile(before, after),
		"WriteLoomContextFiles rewrote an up-to-date file (inode changed)")
	assert.Equal(t, before.ModTime(), after.ModTime(),
		"WriteLoomContextFiles rewrote an up-to-date file (mtime changed)")

	// stale content is rewritten
	assert.NoError(t, os.WriteFile(wt, []byte("stale"), 0o644))
	assert.NoError(t, WriteLoomContextFiles(dir))
	b, _ := os.ReadFile(wt)
	assert.NotEqual(t, "stale", string(b))

	// empty configDir is a no-op (no error)
	assert.NoError(t, WriteLoomContextFiles(""))
}

func TestLoomContextProgram(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))
	wtPath := filepath.Join(dir, "claude-loom-context.md")
	wsPath := filepath.Join(dir, "claude-loom-context-workspace.md")

	// disabled => unchanged
	SetLoomContextEnabled(dir, false)
	assert.Equal(t, "claude", loomContextProgram("claude", dir, false))

	SetLoomContextEnabled(dir, true)

	// worktree instance => worktree file
	assert.Equal(t,
		"claude --append-system-prompt-file '"+wtPath+"'",
		loomContextProgram("claude", dir, false))

	// workspace terminal => workspace file
	assert.Equal(t,
		"claude --append-system-prompt-file '"+wsPath+"'",
		loomContextProgram("claude", dir, true))

	// non-claude program => unchanged
	assert.Equal(t, "aider", loomContextProgram("aider", dir, false))

	// empty configDir => unchanged
	assert.Equal(t, "claude", loomContextProgram("claude", "", false))

	// missing file => unchanged (fail-safe)
	assert.Equal(t, "claude", loomContextProgram("claude", t.TempDir(), false))
}

// Each workspace has its own config, and several are loaded at once: a
// launch reads the toggle of its own config dir, not the last one set.
func TestLoomContextProgram_ReadsItsOwnConfigDirsToggle(t *testing.T) {
	on, off := t.TempDir(), t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(on))
	assert.NoError(t, WriteLoomContextFiles(off))
	SetLoomContextEnabled(on, true)
	SetLoomContextEnabled(off, false)

	assert.Contains(t, loomContextProgram("claude", on, false), "--append-system-prompt-file")
	assert.Equal(t, "claude", loomContextProgram("claude", off, false))
	assert.Equal(t, "claude", loomContextProgram("claude", t.TempDir(), false), "a dir never set reads as off")
}
