package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestWorkbench() *Workbench {
	w := NewWorkbench(NewDiffPane(), NewTerminalPane())
	w.SetSize(50, 20)
	return w
}

func TestWorkbench_DefaultsToMarkdownTab(t *testing.T) {
	w := newTestWorkbench()
	assert.Equal(t, WbTabMarkdown, w.Tab())
	out := w.String()
	assert.Contains(t, out, "markdown")
	assert.Contains(t, out, "diff")
	assert.Contains(t, out, "files")
	assert.Contains(t, out, "terminal")
}

func TestWorkbench_TabSwitchAndRender(t *testing.T) {
	w := newTestWorkbench()
	w.SetTab(WbTabFiles)
	assert.Equal(t, WbTabFiles, w.Tab())
	w.SetTab(WbTabMarkdown)
	w.Markdown.SetDocument("/tmp/p.md", "# Hello\n", time.Now())
	assert.Contains(t, w.String(), "Hello")
}

func TestWorkbench_StringNeverExceedsHeight(t *testing.T) {
	w := newTestWorkbench()
	long := strings.Repeat("x\n\n", 300)
	w.Markdown.SetDocument("/tmp/p.md", long, time.Now())
	lines := strings.Split(w.String(), "\n")
	assert.LessOrEqual(t, len(lines), 20, "panel must cap at its height")
}

func TestWorkbench_FilesCursorNavAndSelect(t *testing.T) {
	w := newTestWorkbench()
	w.SetFiles("/repo", []string{"README.md", "main.go", "docs/plan.md"})
	w.SetTab(WbTabFiles)

	// cursor 0 = README.md
	path, ok := w.FileUnderCursor()
	require.True(t, ok)
	assert.Equal(t, "/repo/README.md", path)
	assert.True(t, IsMarkdownPath(path))

	w.FilesDown() // main.go — resolvable, but not markdown
	path, ok = w.FileUnderCursor()
	require.True(t, ok)
	assert.False(t, IsMarkdownPath(path))

	w.FilesDown() // docs/plan.md
	path, _ = w.FileUnderCursor()
	assert.Equal(t, "/repo/docs/plan.md", path)
	w.FilesDown() // clamp at end
	path, _ = w.FileUnderCursor()
	assert.Equal(t, "/repo/docs/plan.md", path)
}

func TestWorkbench_SetSessionTitleResetsMarkdownAndFiles(t *testing.T) {
	w := newTestWorkbench()
	w.Markdown.SetDocument("/tmp/p.md", "# A\n", time.Now())
	w.SetFiles("/repo", []string{"a.md"})
	w.SetSession("other-session", "/repo2")
	assert.Equal(t, "", w.Markdown.Path(), "session switch clears the document")
	assert.True(t, w.Markdown.Following())
	_, ok := w.FileUnderCursor()
	assert.False(t, ok, "session switch clears the file list")
	assert.Equal(t, "/repo2", w.Root())
}
