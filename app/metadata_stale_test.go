package app

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetadataReadyMsg_StaleDeadResultLeavesAnInstanceAFlowOwns: a probe
// dispatched while the instance ran can be answered Dead after Pause closed
// its tmux session. By then the instance is Loading and Pause still owns it
// (it is archiving Claude's temp dir), so the result must not mark it
// Paused; a kill's Deleting is just as much its own flow's. deadVerifiedMsg
// already drops results for such instances; the health tick's results get
// the same guard.
func TestMetadataReadyMsg_StaleDeadResultLeavesAnInstanceAFlowOwns(t *testing.T) {
	for _, status := range []session.Status{session.Loading, session.Deleting} {
		t.Run(status.String(), func(t *testing.T) {
			m, inst := setupPtmxDeadFixture(t)
			require.NoError(t, inst.TransitionTo(status)) // as the pause's confirm / the kill's preAction does

			_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
				{instance: inst, tmuxLive: tmux.LivenessDead},
			}})

			assert.Equal(t, status, inst.GetStatus(), "the flow still owns the instance")
		})
	}
}

// TestMetadataReadyMsg_DeadResultStillPausesARunningInstance: the guard
// must not block the transition it exists beside: a Running instance whose
// session died is marked Paused by the tick.
func TestMetadataReadyMsg_DeadResultStillPausesARunningInstance(t *testing.T) {
	m, inst := setupPtmxDeadFixture(t)

	_, _ = m.Update(metadataReadyMsg{results: []metadataResult{
		{instance: inst, tmuxLive: tmux.LivenessDead},
	}})

	assert.Equal(t, session.Paused, inst.GetStatus())
}
