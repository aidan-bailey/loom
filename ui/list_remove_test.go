package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
)

// TestList_RemoveKeepsSelectionOnItsRow: removing a row above the
// selection used to leave selectedIdx unchanged, silently moving the
// selection to the next row — and inline attach, which looks the
// selection up per key, then typed into that other session.
func TestList_RemoveKeepsSelectionOnItsRow(t *testing.T) {
	t.Run("an earlier row, by identity", func(t *testing.T) {
		l, src := newPageNavList(5)
		selected := src.items[3]
		l.SetSelectedInstance(3)
		src.remove(src.items[1])
		assert.Equal(t, selected.ID, selectedID(l))
	})
	t.Run("the first row", func(t *testing.T) {
		l, src := newPageNavList(5)
		selected := src.items[3]
		l.SetSelectedInstance(3)
		src.remove(src.items[0])
		assert.Equal(t, selected.ID, selectedID(l))
	})
	t.Run("a later row", func(t *testing.T) {
		l, src := newPageNavList(5)
		selected := src.items[1]
		l.SetSelectedInstance(1)
		src.remove(src.items[3])
		assert.Equal(t, selected.ID, selectedID(l))
	})
	t.Run("the selected last row clamps to the new last", func(t *testing.T) {
		l, src := newPageNavList(3)
		l.SetSelectedInstance(2)
		src.remove(src.items[2])
		assert.Equal(t, 1, l.SelectedIdx())
	})
	t.Run("the only row", func(t *testing.T) {
		l, src := newPageNavList(1)
		src.remove(src.items[0])
		assert.Equal(t, 0, l.SelectedIdx())
		assert.Nil(t, l.GetSelectedInstance())
	})
}

// TestList_PrependedWorkspaceTerminalKeepsSelection: workspace terminals
// are pinned at index 0, which used to shift the selection onto the row
// above it.
func TestList_PrependedWorkspaceTerminalKeepsSelection(t *testing.T) {
	l, src := newPageNavList(3)
	selected := src.items[1]
	l.SetSelectedInstance(1)
	src.prepend(core.InstanceView{ID: 100, Title: "ws", IsWorkspaceTerminal: true})
	assert.Equal(t, selected.ID, selectedID(l))

	empty, emptySrc := newPageNavList(0)
	emptySrc.prepend(core.InstanceView{ID: 100, Title: "ws", IsWorkspaceTerminal: true})
	assert.Equal(t, 0, empty.SelectedIdx())
}

// TestList_PrependedWorkspaceTerminalKeepsSelectionVisible: the prepend
// shifts the selection down a row, which can push it past the bottom of
// the visible window, so the list must scroll to it as a removal does.
func TestList_PrependedWorkspaceTerminalKeepsSelectionVisible(t *testing.T) {
	l, src := newPageNavList(5) // 3 rows visible
	l.SetSelectedInstance(4)
	selected := l.GetSelectedInstance()
	src.prepend(core.InstanceView{ID: 100, Title: "ws", IsWorkspaceTerminal: true})

	assert.Equal(t, selected.ID, selectedID(l))
	assert.GreaterOrEqual(t, l.SelectedIdx(), l.scrollOffset)
	assert.Less(t, l.SelectedIdx(), l.scrollOffset+l.maxVisibleItems(),
		"the selected row must stay inside the visible window")
}

// TestList_ReplacedRowKeepsTheSelection: a recover swaps the placeholder
// for the recovered instance in place, and the selection follows the row.
func TestList_ReplacedRowKeepsTheSelection(t *testing.T) {
	l, src := newPageNavList(3)
	l.SetSelectedInstance(1)
	replacement := core.InstanceView{ID: 100, Title: "recovered"}
	src.items[1] = replacement
	assert.Equal(t, replacement.ID, selectedID(l))
	assert.Equal(t, 1, l.SelectedIdx())
}

// TestList_TwoRemovalsBetweenReadsKeepTheRowRule: an unfocused list is not
// read between edits, so a kill above the selection and a kill of the
// selection itself can both land before its next read. The selection must
// end where the list's own removals left it, one at a time: on the row
// that slid into the selected row's place (d), not on whatever row now
// sits at the stale index (e).
func TestList_TwoRemovalsBetweenReadsKeepTheRowRule(t *testing.T) {
	l, src := newPageNavList(5) // a b c d e
	a, c, d := src.items[0], src.items[2], src.items[3]
	l.SetSelectedInstance(2) // c
	src.remove(a)
	src.remove(c)
	assert.Equal(t, d.ID, selectedID(l))
	assert.Equal(t, 1, l.SelectedIdx())
}

// TestList_SelectedAndEveryLaterRowRemovedSelectsTheLastRow: with nothing
// left after the selected row, the selection moves to the new last row.
func TestList_SelectedAndEveryLaterRowRemovedSelectsTheLastRow(t *testing.T) {
	l, src := newPageNavList(5) // a b c d e
	b := src.items[1]
	l.SetSelectedInstance(2) // c
	for _, inst := range append([]core.InstanceView(nil), src.items[2:]...) {
		src.remove(inst)
	}
	assert.Equal(t, b.ID, selectedID(l))
	assert.Equal(t, 1, l.SelectedIdx())
}
