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

// peerTestInstance builds an unstarted instance (status Ready) for
// peer-classification tests.
func peerTestInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	return inst
}

// TestRefreshPeerSections_TwoSlots_Classification verifies that with two
// workspace slots the focused list gains one peer summary for the
// non-focused slot, and that each classification branch lands in the
// right bucket: Prompting and bell-pending → Attention, Running →
// Running, unstarted (Ready) → Idle.
func TestRefreshPeerSections_TwoSlots_Classification(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	listA := ui.NewList(&s)
	listB := ui.NewList(&s)

	prompting := peerTestInstance(t, "prompting")
	require.NoError(t, prompting.TransitionTo(session.Prompting))
	belled := peerTestInstance(t, "belled")
	belled.SetBellPending(true)
	running := peerTestInstance(t, "running")
	require.NoError(t, running.TransitionTo(session.Running))
	idle := peerTestInstance(t, "idle")

	for _, inst := range []*session.Instance{prompting, belled, running, idle} {
		listB.AddInstance(inst)
	}

	h := &home{
		list:        listA,
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "ws-a"}, list: listA},
			{wsCtx: &config.WorkspaceContext{Name: "ws-b"}, list: listB},
		},
	}

	h.refreshPeerSections()

	assert.Equal(t, []ui.PeerSection{
		{Name: "ws-b", Attention: 2, Running: 1, Idle: 1},
	}, listA.PeerSections(), "focused slot skipped; peer counts classified")
}

// TestRefreshPeerSections_SingleSlotClears verifies that dropping to a
// single slot clears any previously set peer sections.
func TestRefreshPeerSections_SingleSlotClears(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	listA := ui.NewList(&s)
	listA.SetPeerSections([]ui.PeerSection{{Name: "stale", Idle: 1}})

	h := &home{
		list:        listA,
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "ws-a"}, list: listA},
		},
	}

	h.refreshPeerSections()

	assert.Nil(t, listA.PeerSections(), "single-slot home must clear peer sections")
}
