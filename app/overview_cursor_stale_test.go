package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In overview mode, `]`/`[` move the overview cursor to the next waiting
// card — no focus switch, no OpenWorkspaces write. Committing is
// enter/D/r's job via focusCursorSlot.
func TestJumpWaiting_OverviewMovesCursorOnly(t *testing.T) {
	m := fleetHome(t) // viewOverview; focused "afocus" (f1,f2), peer "bpeer" (b1)
	waiter := &session.Instance{Title: "b-wait", Status: session.Ready}
	require.NoError(t, waiter.TransitionTo(session.Prompting))
	m.slots[1].list.AddInstance(waiter)

	m.jumpWaiting(1)

	assert.Equal(t, overviewCursor{slot: 1, inst: 1}, m.overviewCursor,
		"cursor moved to the waiting card")
	assert.Equal(t, 0, m.focusedSlot, "no focus switch while browsing the overview")
}

// Collapsing the cursor's group must re-anchor the cursor to the first
// visible card at render time — a cursor inside a collapsed (1-line)
// block would otherwise drive the combined window to a bogus offset.
func TestOverviewData_ReanchorsCursorInCollapsedGroup(t *testing.T) {
	m := fleetHome(t)
	m.overviewCursor = overviewCursor{slot: 1, inst: 0}
	m.overview.ToggleCollapse("bpeer")

	d := m.overviewData()

	assert.Equal(t, overviewCursor{slot: 0, inst: 0}, m.overviewCursor,
		"cursor re-anchored to first visible card")
	assert.Equal(t, ui.OverviewCursor{Group: 0, Item: 0}, d.Cursor)
}

// A stale cursor (its instance was killed) re-anchors to the first
// visible card on the next nav keypress rather than stepping from a
// phantom position.
func TestMoveCursor_StaleCursorReanchorsWithoutStepping(t *testing.T) {
	m := fleetHome(t)
	m.overviewCursor = overviewCursor{slot: 1, inst: 99}

	m.moveCursor(1)

	assert.Equal(t, overviewCursor{slot: 0, inst: 0}, m.overviewCursor)
}
