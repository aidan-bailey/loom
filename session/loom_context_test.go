package session

import (
	"bytes"
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
	// Each written file is its variant's head, the shared fragment, then the
	// sessions fragment; every fragment ends in exactly one newline, so each
	// seam is one blank line.
	for _, frag := range [][]byte{loomContextWorktreeHead, loomContextMainHead, loomContextShared, loomContextSessions, loomContextSubagentHead} {
		assert.True(t, bytes.HasSuffix(frag, []byte("\n")) && !bytes.HasSuffix(frag, []byte("\n\n")),
			"a loom-context fragment must end in exactly one newline")
	}
	for path, head := range map[string][]byte{wt: loomContextWorktreeHead, ws: loomContextMainHead} {
		b, err := os.ReadFile(path)
		assert.NoError(t, err)
		assert.Contains(t, string(b), "Loom session context")
		assert.Equal(t, string(head)+"\n"+string(loomContextShared)+"\n"+string(loomContextSessions), string(b), path)
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

// A subagent's cut is the subagent head, then its parent's head and the
// shared fragment. The sessions fragment is the parent's alone: messaging
// and starting sessions is its job.
func TestSubagentContext_IsTheParentsWithoutOtherSessions(t *testing.T) {
	for name, head := range map[string][]byte{"worktree": loomContextWorktreeHead, "workspace": loomContextMainHead} {
		parent := string(loomContextFile(head))
		cut := string(subagentContextFile(head))

		assert.Equal(t, string(loomContextSubagentHead)+"\n"+string(head)+"\n"+string(loomContextShared), cut, name)
		assert.Contains(t, cut, "**Heavy work goes through loom's work queue.**", name)
		assert.Contains(t, parent, "## Other sessions", name)
		assert.NotContains(t, cut, "## Other sessions", name)
		assert.NotContains(t, cut, "loom session send", name)
	}
}

// Claude passes a SubagentStart hook's additionalContext inline only up to
// 10,000 characters; past that it writes the text to a file and hands the
// subagent a 2,000-character preview. A byte count bounds the characters.
func TestSubagentContext_FitsClaudesInlineCap(t *testing.T) {
	for name, head := range map[string][]byte{"worktree": loomContextWorktreeHead, "workspace": loomContextMainHead} {
		assert.Less(t, len(subagentContextFile(head)), 10000, name)
	}
}

// A launch's subagents get the cut of the prompt the launch itself gets,
// and none when it gets none: the feature off, its file missing, or a
// program other than Claude.
func TestSubagentBrief_FollowsTheParentsContext(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))
	SetLoomContextEnabled(dir, true)

	assert.Equal(t, string(subagentContextFile(loomContextWorktreeHead)), subagentBrief("claude", dir, false))
	assert.Equal(t, string(subagentContextFile(loomContextMainHead)), subagentBrief("claude", dir, true))
	assert.Empty(t, subagentBrief("aider", dir, false))

	missing := t.TempDir()
	SetLoomContextEnabled(missing, true)
	assert.Empty(t, subagentBrief("claude", missing, false), "no prompt file, no flag, no brief")

	SetLoomContextEnabled(dir, false)
	assert.Empty(t, subagentBrief("claude", dir, false))
}
