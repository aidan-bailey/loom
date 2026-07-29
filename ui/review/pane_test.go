package reviewui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
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

// The exit path must persist EVERY tab, not just the active one.
func TestPane_QPersistsAllTabs(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a_first.md")
	second := filepath.Join(root, "b_second.md")
	require.NoError(t, os.WriteFile(first, []byte("# First\n\nalpha\n"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("# Second\n\nbeta\n"), 0o644))

	p := &Pane{m: AppModel{
		title:     "sess",
		root:      root,
		multiFile: true,
		tabs: []FileTab{
			{path: first, display: "a_first.md", cursorLine: 1},
			{path: second, display: "b_second.md", cursorLine: 1},
		},
		contentViewport: viewport.New(),
		commentViewport: viewport.New(),
		modalTextarea:   newTextarea(),
	}}
	p.SetSize(100, 40)
	p.HandleMsg(p.LoadCmd()())

	// Comment on tab 0, switch to tab 1, comment there too.
	p.HandleKey(code(tea.KeyEnter))
	typeText(t, p, "note one")
	p.HandleKey(ctrl('s'))

	p.HandleKey(code(tea.KeyTab)) // next tab
	require.Equal(t, 1, p.m.activeTab)
	p.HandleKey(code(tea.KeyEnter))
	typeText(t, p, "note two")
	p.HandleKey(ctrl('s'))

	require.Equal(t, 2, p.CommentCount())

	// q returns a tea.Batch of one persist Cmd per tab.
	cmd, handled, exit := p.HandleKey(kp('q'))
	require.True(t, handled)
	require.True(t, exit)
	require.NotNil(t, cmd)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "exit persists via a tea.Batch")
	require.Len(t, batch, 2, "one persist Cmd per tab")

	for _, c := range batch {
		saved, ok := c().(SavedMsg)
		require.True(t, ok)
		require.NoError(t, saved.Err)
		assert.Equal(t, "sess", saved.Title)
	}

	// Both review files exist on disk with their comment intact.
	for path, body := range map[string]string{first: "note one", second: "note two"} {
		st, err := review.Load(root, path)
		require.NoError(t, err)
		require.Len(t, st.Comments, 1, "review for %s", path)
		assert.Equal(t, body, st.Comments[0].Body)
	}
}

// A background save failure must not blank a loaded review.
func TestPane_SaveErrorIsNonFatal(t *testing.T) {
	p := loadedDocPane(t)
	require.Contains(t, p.View(), "body line")

	p.HandleMsg(SavedMsg{Title: "sess", Err: errors.New("disk full")})
	v := p.View()
	assert.Contains(t, v, "body line", "the review stays visible")
	assert.Contains(t, v, "save failed")
	assert.Contains(t, v, "disk full")

	// A later successful save clears the flag.
	p.HandleMsg(SavedMsg{Title: "sess"})
	v = p.View()
	assert.Contains(t, v, "body line")
	assert.NotContains(t, v, "save failed")
}

// A pane built from an empty file set must not panic on keypresses.
func TestPane_EmptyPaneSwallowsKeys(t *testing.T) {
	root := t.TempDir()
	p := NewCodePane("t", root, nil, "HEAD")
	p.SetSize(100, 40)
	p.HandleMsg(p.LoadCmd()())

	for _, msg := range []tea.KeyPressMsg{kp('j'), kp('v'), code(tea.KeyEnter), kp('q'), code(tea.KeyEscape)} {
		cmd, _, exit := p.HandleKey(msg)
		assert.Nil(t, cmd)
		assert.False(t, exit)
	}
	assert.False(t, p.Busy())
	assert.Equal(t, 0, p.CommentCount())
	assert.Contains(t, p.View(), "Loading")
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

// A single unreadable file in multi-file mode must degrade to a
// placeholder tab, not blank the whole pane with a global error.
func TestPane_UnreadableFileDoesNotAbortLoad(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "a_good.md")
	require.NoError(t, os.WriteFile(good, []byte("# Good\n\nkept\n"), 0o644))
	missing := filepath.Join(root, "b_missing.md")

	p := &Pane{m: AppModel{
		title:     "sess",
		root:      root,
		multiFile: true,
		tabs: []FileTab{
			{path: good, display: "a_good.md", cursorLine: 1},
			{path: missing, display: "b_missing.md", cursorLine: 1},
		},
		contentViewport: viewport.New(),
		commentViewport: viewport.New(),
		modalTextarea:   newTextarea(),
	}}
	p.SetSize(100, 40)

	msg, ok := p.LoadCmd()().(LoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err, "one bad file must not fail the whole load")
	p.HandleMsg(msg)

	v := p.View()
	assert.NotContains(t, v, "Error:")
	assert.Contains(t, v, "a_good.md")
	assert.Contains(t, v, "kept", "the readable file still renders")

	// Both tabs carry review state; only the good one has a document.
	require.Len(t, p.States(), 2)
	assert.NotNil(t, p.m.tabs[0].doc)
	assert.Nil(t, p.m.tabs[1].doc)
	assert.Nil(t, p.m.tabs[1].chromaLines, "no highlight cache for a nil doc")
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
