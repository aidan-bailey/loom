package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
)

func TestForegroundSlotNames_ExcludesBackground(t *testing.T) {
	m := &home{
		focusedSlot: 0,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "a"}},
			{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
			{wsCtx: &config.WorkspaceContext{Name: "c"}},
		},
	}
	assert.Equal(t, []string{"a", "c"}, m.foregroundSlotNames())
}

func TestForegroundSlotsAndSelected_RemapsFocusIndex(t *testing.T) {
	// Foreground slots are a=0 (bg b skipped) and c=2. Focused is c
	// (m.slots index 2) → foreground-subset index 1.
	m := &home{
		focusedSlot: 2,
		slots: []workspaceSlot{
			{wsCtx: &config.WorkspaceContext{Name: "a"}},
			{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
			{wsCtx: &config.WorkspaceContext{Name: "c"}},
		},
	}
	names, sel := m.foregroundSlotsAndSelected()
	assert.Equal(t, []string{"a", "c"}, names)
	assert.Equal(t, 1, sel)
}

func TestPromoteSlot_ClearsBackgroundAndPersists(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{} // in-memory registry
	m.slots = []workspaceSlot{
		{wsCtx: &config.WorkspaceContext{Name: "a"}},
		{wsCtx: &config.WorkspaceContext{Name: "b"}, background: true},
	}
	m.focusedSlot = 0

	m.promoteSlot(1)
	assert.False(t, m.slots[1].background, "promoted slot is no longer background")
	assert.Equal(t, []string{"a", "b"}, m.foregroundSlotNames())

	m.demoteSlot(1)
	assert.True(t, m.slots[1].background, "demoted slot is background again")
	assert.Equal(t, []string{"a"}, m.foregroundSlotNames())
}
