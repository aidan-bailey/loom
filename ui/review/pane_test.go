package reviewui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/review"
)

// key builds a printable-rune keypress in loom's idiom.
func kp(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// code builds a non-printable keypress (enter, esc, ...).
func code(c rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: c}
}

// ctrl builds a ctrl-modified keypress.
func ctrl(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func typeText(t *testing.T, p *Pane, s string) {
	t.Helper()
	for _, r := range s {
		p.HandleKey(kp(r))
	}
}

func loadedDocPane(t *testing.T) *Pane {
	t.Helper()
	root := t.TempDir()
	doc := filepath.Join(root, "plan.md")
	require.NoError(t, os.WriteFile(doc, []byte("# Title\n\nbody line\n"), 0o644))
	p := NewDocPane("sess", root, doc)
	p.SetSize(100, 40)
	msg := p.LoadCmd()() // run the load Cmd synchronously
	p.HandleMsg(msg)
	return p
}

func TestPane_LoadsAndRenders(t *testing.T) {
	p := loadedDocPane(t)
	v := p.View()
	assert.Contains(t, v, "plan.md")
	assert.NotContains(t, v, "Loading")
}

func TestPane_EscFallsThroughWhenIdle(t *testing.T) {
	p := loadedDocPane(t)
	_, handled, _ := p.HandleKey(code(tea.KeyEscape))
	assert.False(t, handled, "idle esc belongs to the workbench")
}

func TestPane_EscHandledWhileBusy(t *testing.T) {
	p := loadedDocPane(t)
	// 'v' enters visual selection — a capture-all state.
	p.HandleKey(kp('v'))
	require.True(t, p.Busy())
	_, handled, _ := p.HandleKey(code(tea.KeyEscape))
	assert.True(t, handled, "esc cancels the selection instead of exiting")
	assert.False(t, p.Busy())
}

func TestPane_CommentRoundtripPersists(t *testing.T) {
	p := loadedDocPane(t)

	// enter opens the comment modal on the cursor line (line 1)
	_, handled, _ := p.HandleKey(code(tea.KeyEnter))
	require.True(t, handled)
	require.True(t, p.Busy(), "comment modal is a capture-all state")

	typeText(t, p, "looks wrong")

	cmd, handled, exit := p.HandleKey(ctrl('s'))
	require.True(t, handled)
	assert.False(t, exit)
	require.NotNil(t, cmd, "submit returns a persist Cmd")

	saved, ok := cmd().(SavedMsg)
	require.True(t, ok, "persist Cmd returns SavedMsg")
	require.NoError(t, saved.Err)
	assert.Equal(t, "sess", saved.Title)

	require.Len(t, p.States(), 1)
	state, err := review.Load(p.Root(), p.States()[0].File)
	require.NoError(t, err)
	require.Len(t, state.Comments, 1)
	assert.Equal(t, "looks wrong", state.Comments[0].Body)
	assert.Equal(t, 1, state.Comments[0].Line)

	assert.Equal(t, 1, p.CommentCount())
}

func TestPane_VisualSelectionSpansLines(t *testing.T) {
	p := loadedDocPane(t)

	p.HandleKey(kp('v')) // enter visual mode, anchor at line 1
	p.HandleKey(kp('j')) // extend to line 2
	_, _, _ = p.HandleKey(code(tea.KeyEnter))
	typeText(t, p, "range note")
	cmd, _, _ := p.HandleKey(ctrl('s'))
	require.NotNil(t, cmd)
	saved, ok := cmd().(SavedMsg)
	require.True(t, ok)
	require.NoError(t, saved.Err)

	state, err := review.Load(p.Root(), p.States()[0].File)
	require.NoError(t, err)
	require.Len(t, state.Comments, 1)
	assert.Equal(t, 1, state.Comments[0].Line)
	assert.Equal(t, 2, state.Comments[0].EndLine)
	assert.False(t, p.Busy(), "submitting clears the selection")
}

func TestPane_QExitsWithPersist(t *testing.T) {
	p := loadedDocPane(t)
	cmd, handled, exit := p.HandleKey(kp('q'))
	assert.True(t, handled)
	assert.True(t, exit)
	require.NotNil(t, cmd)
}

func TestPane_CommentCount(t *testing.T) {
	p := loadedDocPane(t)
	assert.Equal(t, 0, p.CommentCount())
}

// Ported from upstream app_test.go: a pane must surface comments that
// were already on disk when it loaded.
func TestPane_LoadsExistingComments(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, "test.go")
	require.NoError(t, os.WriteFile(docPath, []byte("package main\n\nfunc main() {}\n"), 0o644))

	state := &review.ReviewState{
		File: docPath,
		Comments: []review.Comment{{
			ID:             "test-comment-1",
			Line:           1,
			ContentSnippet: "package main",
			Body:           "This is a test comment",
			CreatedAt:      time.Now(),
		}},
	}
	require.NoError(t, review.Save(root, state))

	p := NewDocPane("sess", root, docPath)
	p.SetSize(100, 40)
	p.HandleMsg(p.LoadCmd()())

	require.Len(t, p.States(), 1)
	got := p.States()[0]
	require.Len(t, got.Comments, 1)
	assert.Equal(t, "test-comment-1", got.Comments[0].ID)
	assert.Equal(t, "This is a test comment", got.Comments[0].Body)
	assert.Equal(t, 1, p.CommentCount())
}

func TestPane_LoadErrorSurfaces(t *testing.T) {
	root := t.TempDir()
	p := NewDocPane("sess", root, filepath.Join(root, "missing.md"))
	p.SetSize(100, 40)
	msg, ok := p.LoadCmd()().(LoadedMsg)
	require.True(t, ok)
	require.Error(t, msg.Err)
	p.HandleMsg(msg)
	assert.Contains(t, p.View(), "Error:")
}
