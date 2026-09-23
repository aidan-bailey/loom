package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
)

// TestList_RemoveKeepsSelectionOnItsRow: removing a row above the
// selection used to leave selectedIdx unchanged, silently moving the
// selection to the next row — and inline attach, which looks the
// selection up per key, then typed into that other session.
func TestList_RemoveKeepsSelectionOnItsRow(t *testing.T) {
	t.Run("an earlier row, by identity", func(t *testing.T) {
		l := newPageNavList(5)
		selected := l.items[3]
		l.SetSelectedInstance(3)
		l.RemoveInstance(l.items[1])
		assert.Same(t, selected, l.GetSelectedInstance())
	})
	t.Run("an earlier row, by title", func(t *testing.T) {
		l := newPageNavList(5)
		selected := l.items[3]
		l.SetSelectedInstance(3)
		l.RemoveInstanceByTitle(l.items[0].Title)
		assert.Same(t, selected, l.GetSelectedInstance())
	})
	t.Run("a later row", func(t *testing.T) {
		l := newPageNavList(5)
		selected := l.items[1]
		l.SetSelectedInstance(1)
		l.RemoveInstance(l.items[3])
		assert.Same(t, selected, l.GetSelectedInstance())
	})
	t.Run("the selected last row clamps to the new last", func(t *testing.T) {
		l := newPageNavList(3)
		l.SetSelectedInstance(2)
		l.RemoveInstance(l.items[2])
		assert.Equal(t, 1, l.SelectedIdx())
	})
	t.Run("the only row", func(t *testing.T) {
		l := newPageNavList(1)
		l.RemoveInstance(l.items[0])
		assert.Equal(t, 0, l.SelectedIdx())
		assert.Nil(t, l.GetSelectedInstance())
	})
}

// TestList_PrependedWorkspaceTerminalKeepsSelection: workspace terminals
// are pinned at index 0, which used to shift the selection onto the row
// above it.
func TestList_PrependedWorkspaceTerminalKeepsSelection(t *testing.T) {
	l := newPageNavList(3)
	selected := l.items[1]
	l.SetSelectedInstance(1)
	l.AddInstance(&session.Instance{Title: "ws", IsWorkspaceTerminal: true})
	assert.Same(t, selected, l.GetSelectedInstance())

	empty := newPageNavList(0)
	empty.AddInstance(&session.Instance{Title: "ws", IsWorkspaceTerminal: true})
	assert.Equal(t, 0, empty.SelectedIdx())
}
