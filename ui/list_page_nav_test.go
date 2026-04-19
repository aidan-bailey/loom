package ui

import (
	"claude-squad/log"
	"claude-squad/session"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/assert"
)

// List height that yields exactly maxVisibleItems() == 3
// (formula: n = (height - 3) / 5, so height = 18 gives n = 3).
const pageNavTestHeight = 18

// newPageNavList builds a list containing n instances and sizes it so
// maxVisibleItems() returns pageSize. Every item starts as Running;
// callers can mutate individual items to Deleting to cover skip paths.
func newPageNavList(n int) *List {
	log.Initialize("", false)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&sp, false)
	l.SetSize(40, pageNavTestHeight)
	for i := 0; i < n; i++ {
		inst := &session.Instance{Title: fmt.Sprintf("inst-%02d", i)}
		_ = inst.TransitionTo(session.Running)
		l.AddInstance(inst)()
	}
	return l
}

func markDeleting(t *testing.T, l *List, idx int) {
	t.Helper()
	inst := l.items[idx]
	if err := inst.TransitionTo(session.Deleting); err != nil {
		t.Fatalf("transition to Deleting at idx=%d: %v", idx, err)
	}
}

func TestListPageNav_MaxVisibleItemsMatchesFixture(t *testing.T) {
	l := newPageNavList(0)
	assert.Equal(t, 3, l.maxVisibleItems(),
		"fixture height %d should yield page size 3; formula drifted",
		pageNavTestHeight)
}

func TestListPageNav_EmptyListNoOp(t *testing.T) {
	l := newPageNavList(0)
	l.PageUp()
	l.PageDown()
	l.Top()
	l.Bottom()
	assert.Equal(t, 0, l.selectedIdx)
}

func TestListPageNav_PageDownAdvancesByPageSize(t *testing.T) {
	l := newPageNavList(10)
	l.selectedIdx = 0

	l.PageDown()
	assert.Equal(t, 3, l.selectedIdx, "PageDown from 0 with page=3 lands on 3")
	l.PageDown()
	assert.Equal(t, 6, l.selectedIdx, "second PageDown lands on 6")
	l.PageDown()
	assert.Equal(t, 9, l.selectedIdx, "third PageDown lands on 9 (last item)")
	l.PageDown()
	assert.Equal(t, 9, l.selectedIdx, "PageDown at last item is a no-op")
}

func TestListPageNav_PageUpRetreatsByPageSize(t *testing.T) {
	l := newPageNavList(10)
	l.selectedIdx = 9

	l.PageUp()
	assert.Equal(t, 6, l.selectedIdx)
	l.PageUp()
	assert.Equal(t, 3, l.selectedIdx)
	l.PageUp()
	assert.Equal(t, 0, l.selectedIdx, "PageUp past the top clamps to 0")
	l.PageUp()
	assert.Equal(t, 0, l.selectedIdx, "PageUp at top is a no-op")
}

func TestListPageNav_PageDownSkipsDeleting(t *testing.T) {
	l := newPageNavList(10)
	l.selectedIdx = 0
	// Target after a PageDown from idx=0 is idx=3. Mark 3..5 Deleting so
	// the forward-walk skip advances past them.
	markDeleting(t, l, 3)
	markDeleting(t, l, 4)
	markDeleting(t, l, 5)

	l.PageDown()
	assert.Equal(t, 6, l.selectedIdx,
		"PageDown must advance past a run of Deleting items, not land on one")
}

func TestListPageNav_PageUpWalksUpwardThroughDeleting(t *testing.T) {
	l := newPageNavList(10)
	l.selectedIdx = 9
	// Target after a PageUp from idx=9 is idx=6. Mark 4..6 Deleting so
	// the backward-walk skip retreats past them without overshooting 0.
	markDeleting(t, l, 4)
	markDeleting(t, l, 5)
	markDeleting(t, l, 6)

	l.PageUp()
	assert.Equal(t, 3, l.selectedIdx,
		"PageUp must retreat past a run of Deleting items by walking upward")
}

func TestListPageNav_TopSkipsLeadingDeleting(t *testing.T) {
	l := newPageNavList(6)
	markDeleting(t, l, 0)
	markDeleting(t, l, 1)
	l.selectedIdx = 5

	l.Top()
	assert.Equal(t, 2, l.selectedIdx,
		"Top must land on the first non-Deleting item, not index 0")
}

func TestListPageNav_BottomSkipsTrailingDeleting(t *testing.T) {
	l := newPageNavList(6)
	markDeleting(t, l, 4)
	markDeleting(t, l, 5)
	l.selectedIdx = 0

	l.Bottom()
	assert.Equal(t, 3, l.selectedIdx,
		"Bottom must land on the last non-Deleting item")
}

func TestListPageNav_AllDeletingKeepsSelection(t *testing.T) {
	l := newPageNavList(4)
	for i := range l.items {
		markDeleting(t, l, i)
	}
	l.selectedIdx = 2

	l.PageUp()
	assert.Equal(t, 2, l.selectedIdx, "PageUp in all-Deleting list stays put")
	l.PageDown()
	assert.Equal(t, 2, l.selectedIdx, "PageDown in all-Deleting list stays put")
	l.Top()
	assert.Equal(t, 2, l.selectedIdx, "Top in all-Deleting list stays put")
	l.Bottom()
	assert.Equal(t, 2, l.selectedIdx, "Bottom in all-Deleting list stays put")
}
