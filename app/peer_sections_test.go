package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefreshPeerSections_TwoSlots verifies that with two workspace
// slots, the focused list gains a peer summary for the non-focused
// slot (rendered as an uppercased footer line), built from that slot's
// live list.
func TestRefreshPeerSections_TwoSlots(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	listA := ui.NewList(&s)
	listB := ui.NewList(&s)
	listA.SetSize(40, 30)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "b1",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	_ = listB.AddInstance(inst)

	h := &home{
		list:        listA,
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "ws-a"}, list: listA},
			{wsCtx: &config.WorkspaceContext{Name: "ws-b"}, list: listB},
		},
	}

	h.refreshPeerSections()

	out := listA.String()
	assert.Contains(t, out, "WS-B", "non-focused slot must appear as a peer line")
	assert.Contains(t, out, "·1", "unstarted instance counts as Idle")
	assert.NotContains(t, out, "WS-A", "focused slot is not its own peer")
}

// TestRefreshPeerSections_SingleSlotClears verifies that dropping to a
// single slot clears any previously set peer sections.
func TestRefreshPeerSections_SingleSlotClears(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	listA := ui.NewList(&s)
	listA.SetSize(40, 30)
	listA.SetPeerSections([]ui.PeerSection{{Name: "stale", Idle: 1}})

	h := &home{
		list:        listA,
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "ws-a"}, list: listA},
		},
	}

	h.refreshPeerSections()

	assert.NotContains(t, listA.String(), "STALE", "single-slot home must clear peer sections")
}
