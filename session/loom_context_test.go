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
	SetLoomContextEnabled(false)
	assert.Equal(t, "claude", loomContextProgram("claude", dir, false))

	SetLoomContextEnabled(true)
	t.Cleanup(func() { SetLoomContextEnabled(false) })

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
