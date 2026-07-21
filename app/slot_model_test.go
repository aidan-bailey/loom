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
