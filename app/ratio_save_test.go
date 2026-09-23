package app

import (
	"testing"

	"github.com/aidan-bailey/loom/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResizeSplit_SurvivesInstanceChanged pins the pending-over-persisted
// precedence in applyStoredRatio. handleScriptDone applies the deferred
// resizeSplit action and then unconditionally calls instanceChanged(),
// which re-applies the selected instance's stored ratio — if that lookup
// consulted only the persisted SplitRatios, the freshly adjusted ratio
// (living solely in pendingRatioSaves until the throttled flush) would be
// reverted to the stale/default value in the same Update pass, before
// View ever rendered it.
func TestResizeSplit_SurvivesInstanceChanged(t *testing.T) {
	m := newTestHome(t) // real appState via config.LoadStateFrom(t.TempDir())
	mustAddInstance(t, m, "a")

	base := m.splitPane.AgentRatio()
	m.resizeSplit(+0.05)
	adjusted := m.splitPane.AgentRatio()
	require.NotEqual(t, base, adjusted, "resizeSplit must change the ratio")

	// Simulate handleScriptDone's order: deferred action applied, then
	// instanceChanged. The unpersisted adjustment must survive.
	_ = m.instanceChanged()
	assert.Equal(t, adjusted, m.splitPane.AgentRatio(),
		"pending (unflushed) ratio must win over persisted/default in applyStoredRatio")
}

// TestRatioSaveMsg_FlushesAndPrunes drives the throttled flush through
// Update: pending entries land in persisted SplitRatios in one write,
// entries for instances no longer in the list are pruned, and the
// gate disarms so the next resize burst can re-arm.
func TestRatioSaveMsg_FlushesAndPrunes(t *testing.T) {
	m := newTestHome(t)
	mustAddInstance(t, m, "live")

	// Stale persisted entry whose instance is not in the list anymore.
	require.NoError(t, m.appState.SetUIPrefs(config.UIPrefs{
		SplitRatios: map[string]float64{"gone": 0.4},
	}))

	m.pendingRatioSaves = map[string]float64{"live": 0.8}
	m.gate(gateRatioSave).inFlight = true

	_, _ = m.Update(gatedMsg{kind: gateRatioSave, msg: ratioSaveMsg{}})

	got := m.appState.GetUIPrefs().SplitRatios
	assert.Equal(t, 0.8, got["live"], "pending ratio must be persisted")
	_, stale := got["gone"]
	assert.False(t, stale, "titles absent from the live list must be pruned")
	assert.False(t, m.gate(gateRatioSave).inFlight, "flush must disarm the throttle tick")
	assert.Empty(t, m.pendingRatioSaves, "pending map must be drained")
}

// TestMaybeArmRatioSave_OneTickAndEmptyDeliveryDisarms pins the throttle's
// shape: nothing pending arms nothing, a pending ratio arms exactly one
// tick however many resizes follow, and a tick that lands after a
// synchronous flush (slot switch, quit) found the map empty still
// disarms, or the next burst would never persist.
func TestMaybeArmRatioSave_OneTickAndEmptyDeliveryDisarms(t *testing.T) {
	m := newTestHome(t)
	mustAddInstance(t, m, "a")

	assert.Nil(t, m.maybeArmRatioSave(), "nothing pending: no tick")
	assert.False(t, m.gate(gateRatioSave).inFlight, "no tick means nothing armed")

	m.resizeSplit(+0.05)
	require.NotNil(t, m.maybeArmRatioSave())
	m.resizeSplit(+0.05)
	assert.Nil(t, m.maybeArmRatioSave(), "a tick in flight must not be stacked")

	m.flushPendingRatioSaves() // leaveFocusedSlot/handleQuit path
	_, _ = m.Update(gatedMsg{kind: gateRatioSave, msg: ratioSaveMsg{}})
	assert.False(t, m.gate(gateRatioSave).inFlight)

	m.resizeSplit(+0.05)
	assert.NotNil(t, m.maybeArmRatioSave(), "the next burst re-arms")
}
