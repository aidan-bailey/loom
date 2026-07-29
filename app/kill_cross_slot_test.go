package app

import (
	"errors"
	"testing"

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

// TestKillInstanceMsg_RemovesFromNonFocusedSlot delivers a kill completion
// for an instance owned by a non-focused slot and asserts the row is gone.
func TestKillInstanceMsg_RemovesFromNonFocusedSlot(t *testing.T) {
	m := fleetHome(t) // slot 0 "afocus" (f1,f2) focused; slot 1 "bpeer" (b1)
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	_, _ = m.Update(killInstanceMsg{inst: b1, title: "b1"})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"),
		"kill completion must remove the row from the slot that owns it, not the focused slot")
}

// TestKillInstanceMsg_DuplicateTitleAcrossSlots ensures removal is by
// identity: a same-titled instance in the focused slot must survive a kill
// completion that targets the peer slot's instance.
func TestKillInstanceMsg_DuplicateTitleAcrossSlots(t *testing.T) {
	m := fleetHome(t)
	dup := &session.Instance{Title: "b1", Status: session.Ready}
	m.list.AddInstance(dup) // focused slot now also has a "b1"
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	_, _ = m.Update(killInstanceMsg{inst: b1, title: "b1"})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"), "peer slot's b1 removed")
	assert.Same(t, dup, m.list.GetInstanceByTitle("b1"),
		"focused slot's same-titled instance must survive")
}

// TestTransitionFailedMsg_RevertsInstanceInNonFocusedSlot delivers a failed
// kill for a non-focused slot's instance and asserts its status reverts so
// the user can retry, instead of being stuck Deleting forever.
func TestTransitionFailedMsg_RevertsInstanceInNonFocusedSlot(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox() // transitionFailedMsg surfaces the error
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	_, _ = m.Update(transitionFailedMsg{
		inst:           b1,
		title:          "b1",
		op:             "delete",
		previousStatus: session.Ready,
		err:            errors.New("boom"),
	})

	assert.Equal(t, session.Ready, b1.GetStatus(),
		"failed background op must revert the owning instance's status")
}
