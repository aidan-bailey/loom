package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
)

func TestApplyWorkspaceToggle_FleetEngagedDemotesInsteadOfTeardown(t *testing.T) {
	m := newTestHome(t)
	m.registry = &config.WorkspaceRegistry{}
	m.fleetEngaged = true
	// Two fully-wired slots; focused is "a".
	m.slots = []workspaceSlot{fleetSlot(t, "a"), fleetSlot(t, "b")}
	m.focusedSlot = 0
	m.list = m.slots[0].list

	// Desired = only "a" foreground. "b" must DEMOTE (stay a slot), not be removed.
	_ = m.applyWorkspaceToggle([]config.Workspace{{Name: "a"}})
	assert.Len(t, m.slots, 2, "fleet-engaged: b demoted, not torn down")
	for _, sl := range m.slots {
		if sl.wsCtx.Name == "b" {
			assert.True(t, sl.background, "b is now background")
		}
	}
	assert.Equal(t, []string{"a"}, m.foregroundSlotNames())
}
