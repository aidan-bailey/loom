package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for the stuck-"deleting" bug: a kill's async I/O runs for
// seconds (pump wait timeouts, git cleanup), and its completion message used
// to resolve the row via the focused m.list only. If the user switched
// workspace tabs mid-kill, the removal silently no-oped and the row stayed
// Deleting until restart. The completion messages now carry the instance
// pointer and act on it wherever it lives.

// TestKillResult_RemovesFromNonFocusedSlot delivers a kill completion
// for an instance owned by a non-focused slot and asserts the row is gone.
func TestKillResult_RemovesFromNonFocusedSlot(t *testing.T) {
	m := fleetHome(t) // slot 0 "afocus" (f1,f2) focused; slot 1 "bpeer" (b1)
	b1 := instByTitle(m, m.slots[1].list, "b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	deliver(t, m, core.KillResult{Instance: b1, Title: "b1"})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"),
		"kill completion must remove the row from the slot that owns it, not the focused slot")
}

// TestKillResult_DuplicateTitleAcrossSlots ensures removal is by
// identity: a same-titled instance in the focused slot must survive a kill
// completion that targets the peer slot's instance.
func TestKillResult_DuplicateTitleAcrossSlots(t *testing.T) {
	m := fleetHome(t)
	dup := &session.Instance{Title: "b1", Status: session.Ready}
	m.ws.AddForTest(dup) // focused slot now also has a "b1"
	m.syncViews()
	b1 := instByTitle(m, m.slots[1].list, "b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	deliver(t, m, core.KillResult{Instance: b1, Title: "b1"})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"), "peer slot's b1 removed")
	assert.Equal(t, idOf(m, dup), titleID(m.list, "b1"),
		"focused slot's same-titled instance must survive")
}

// TestOpFailed_RevertsInstanceInNonFocusedSlot delivers a failed
// kill for a non-focused slot's instance and asserts its status reverts so
// the user can retry, instead of being stuck Deleting forever.
func TestOpFailed_RevertsInstanceInNonFocusedSlot(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox() // a failed operation surfaces the error
	b1 := instByTitle(m, m.slots[1].list, "b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	deliver(t, m, core.OpFailed{
		Instance: b1,
		Title:    "b1",
		Op:       "delete",
		Previous: session.Ready,
		Err:      errors.New("boom"),
	})

	assert.Equal(t, session.Ready, b1.GetStatus(),
		"failed background op must revert the owning instance's status")
}
