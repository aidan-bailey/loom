package overlay

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func newTestFileExplorer(root string, files []string) *FileExplorerOverlay {
	o := NewFileExplorerOverlay(root, files, nil)
	o.SetSize(60, 20)
	return o
}

func TestFileExplorerEmptyQueryShowsAllFiles(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{"a.go", "b.go", "c.go"})
	assert.Equal(t, 3, o.ResultCount())
}

func TestFileExplorerTypingFilters(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{
		"main.go", "commander.go", "utils/main_helper.go", "README.md",
	})

	// Type 'm' then 'a'; all paths containing "ma" should remain.
	for _, r := range "ma" {
		_, _ = o.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	assert.Greater(t, o.ResultCount(), 0)
	assert.Less(t, o.ResultCount(), 4, "typing should narrow the list")

	// "README.md" lacks the subsequence "ma".
	view := o.View()
	assert.NotContains(t, view, "README.md")
}

func TestFileExplorerCursorClampsToResults(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{"a.go", "b.go"})

	// Press Down repeatedly past the end.
	for range 5 {
		_, _ = o.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	// Selection is the last result, "b.go" after alphabetical sort
	// (empty query preserves input order, so still "b.go").
	assert.Equal(t, filepath.Join("/tmp/repo", "b.go"), o.SelectedPath())
}

func TestFileExplorerCursorCannotGoNegative(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{"a.go", "b.go"})
	for range 3 {
		_, _ = o.HandleKey(tea.KeyMsg{Type: tea.KeyUp})
	}
	assert.Equal(t, filepath.Join("/tmp/repo", "a.go"), o.SelectedPath())
}

func TestFileExplorerEnterReturnsOpenCmd(t *testing.T) {
	var opened string
	o := NewFileExplorerOverlay("/tmp/repo", []string{"main.go"}, func(abs string) tea.Cmd {
		opened = abs
		return func() tea.Msg { return nil }
	})
	o.SetSize(60, 20)

	closed, cmd := o.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, closed, "Enter should close the overlay")
	assert.NotNil(t, cmd, "Enter should return a command")
	assert.Equal(t, "/tmp/repo/main.go", opened)
}

func TestFileExplorerEnterWithNoResultsDoesNotClose(t *testing.T) {
	opened := false
	o := NewFileExplorerOverlay("/tmp/repo", []string{"main.go"}, func(abs string) tea.Cmd {
		opened = true
		return nil
	})
	o.SetSize(60, 20)

	// Type a query that matches nothing.
	for _, r := range "zzz" {
		_, _ = o.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	closed, cmd := o.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, closed)
	assert.Nil(t, cmd)
	assert.False(t, opened)
}

func TestFileExplorerEscClosesWithoutCmd(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{"a.go"})
	closed, cmd := o.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

func TestFileExplorerViewContainsCounter(t *testing.T) {
	o := newTestFileExplorer("/tmp/repo", []string{"a.go", "b.go", "c.go"})
	view := o.View()
	assert.Contains(t, view, "Files (3 / 3)")
}

func TestFileExplorerViewportScrollsWhenCursorOffscreen(t *testing.T) {
	files := make([]string, 50)
	for i := range files {
		files[i] = fakeName(i)
	}
	o := newTestFileExplorer("/tmp/repo", files)

	// Move cursor past the initial viewport window.
	for range 30 {
		_, _ = o.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	view := o.View()
	// The selected line should appear in the rendered view (i.e. the
	// viewport scrolled to keep it visible).
	assert.Contains(t, view, fakeName(30))
	assert.True(t, strings.Contains(view, "›"), "selection marker should render")
}

func fakeName(i int) string {
	// Distinguishable, non-overlapping names so Contains checks are
	// unambiguous.
	return "aaa" + string(rune('a'+i%26)) + "bbb" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + ".go"
}
