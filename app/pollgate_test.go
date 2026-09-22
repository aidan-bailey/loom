package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pollResultMsg stands in for a gated job's result.
type pollResultMsg struct{ n int }

func resultCmd(n int) tea.Cmd {
	return func() tea.Msg { return pollResultMsg{n: n} }
}

func TestNewPollGatesUsesEachJobsInterval(t *testing.T) {
	g := newPollGates()
	assert.Equal(t, rosterInterval, g[gateRoster].interval)
	assert.Equal(t, subagentInterval, g[gateSubagent].interval)
	assert.Equal(t, ghInterval, g[gateGH].interval)
	assert.Zero(t, g[gateRatioSave].interval, "the ratio flush paces itself with its own tick")
}

func TestPollGateDueRespectsIntervalAndInFlight(t *testing.T) {
	now := time.Now()
	g := pollGate{interval: time.Second}
	assert.True(t, g.due(now), "a gate that never dispatched is due")

	g.last = now
	assert.False(t, g.due(now.Add(500*time.Millisecond)), "inside the interval")
	assert.True(t, g.due(now.Add(time.Second)), "the interval has elapsed")

	g.inFlight = true
	assert.False(t, g.due(now.Add(time.Hour)), "an outstanding dispatch blocks the next one")
}

func TestPollGateZeroIntervalOnlyDedupes(t *testing.T) {
	now := time.Now()
	g := pollGate{last: now}
	assert.True(t, g.due(now))
	g.inFlight = true
	assert.False(t, g.due(now))
}

func TestPollGateExpedite(t *testing.T) {
	now := time.Now()
	g := pollGate{interval: time.Hour, last: now}
	require.False(t, g.due(now))

	g.expedite()
	assert.True(t, g.due(now), "an expedited gate is due at once")

	g.last = now
	g.inFlight = true
	g.expedite()
	assert.False(t, g.due(now), "expedite does not cut an in-flight dispatch short")
	g.inFlight = false
	assert.True(t, g.due(now), "it polls again as soon as that one is delivered")
}

func TestDispatchGatedNilBuildArmsNothing(t *testing.T) {
	m := &home{gates: newPollGates()}

	assert.Nil(t, m.dispatchGated(gateRoster, time.Now(), func() tea.Cmd { return nil }))
	assert.False(t, m.gate(gateRoster).inFlight, "no Cmd means no delivery to disarm it")
	assert.True(t, m.gate(gateRoster).last.IsZero(), "nor does it start the interval")
}

func TestDispatchGatedArmsAndWrapsResult(t *testing.T) {
	m := &home{gates: newPollGates()}
	now := time.Now()

	cmd := m.dispatchGated(gateGH, now, func() tea.Cmd { return resultCmd(7) })
	require.NotNil(t, cmd)
	assert.True(t, m.gate(gateGH).inFlight)
	assert.Equal(t, now, m.gate(gateGH).last)
	assert.False(t, m.gate(gateRoster).inFlight, "only the named gate arms")

	assert.Equal(t, gatedMsg{kind: gateGH, msg: pollResultMsg{n: 7}}, cmd())
}

func TestDispatchGatedWrapsATickOnceItFires(t *testing.T) {
	m := &home{gates: newPollGates()}

	cmd := m.dispatchGated(gateRatioSave, time.Now(), func() tea.Cmd {
		return tea.Tick(time.Millisecond, func(time.Time) tea.Msg { return pollResultMsg{n: 1} })
	})
	require.NotNil(t, cmd)
	assert.Equal(t, gatedMsg{kind: gateRatioSave, msg: pollResultMsg{n: 1}}, cmd())
}

func TestDispatchGatedSkipsBuildWhenNotDue(t *testing.T) {
	m := &home{gates: newPollGates()}
	now := time.Now()
	require.NotNil(t, m.dispatchGated(gateSubagent, now, func() tea.Cmd { return resultCmd(1) }))

	called := false
	build := func() tea.Cmd { called = true; return resultCmd(2) }

	assert.Nil(t, m.dispatchGated(gateSubagent, now.Add(subagentInterval), build),
		"in flight, even with the interval elapsed")
	m.gate(gateSubagent).inFlight = false
	assert.Nil(t, m.dispatchGated(gateSubagent, now.Add(subagentInterval/2), build),
		"delivered, but inside the interval")
	assert.False(t, called, "build must not run when the gate is not due: it reads model state and may be costly")

	assert.NotNil(t, m.dispatchGated(gateSubagent, now.Add(subagentInterval), build))
	assert.True(t, called)
}

func TestGatedDeliveryDisarmsFirst(t *testing.T) {
	m := homeWithAppState(t)
	for _, kind := range []gateKind{gateRoster, gateSubagent, gateGH, gateRatioSave} {
		m.gate(kind).inFlight = true
		// An inner message no case handles, and a nil one, disarm all the
		// same: disarming belongs to the wrapper, not the handler.
		m.Update(gatedMsg{kind: kind, msg: pollResultMsg{}})
		assert.False(t, m.gate(kind).inFlight, "kind %d", kind)

		m.gate(kind).inFlight = true
		m.Update(gatedMsg{kind: kind})
		assert.False(t, m.gate(kind).inFlight, "kind %d, nil inner message", kind)
	}
}

// TestGatedDeliveryDisarmsRosterErrorAndGitHubResult drives both result
// shapes that once had to disarm by hand through Update: a failed roster
// query (the easy one to miss) and a GitHub poll. Each gate disarms, and
// each inner message still reaches its handler.
func TestGatedDeliveryDisarmsRosterErrorAndGitHubResult(t *testing.T) {
	m := homeWithAppState(t)
	m.roster = map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}
	m.gate(gateRoster).inFlight = true
	m.gate(gateGH).inFlight = true

	m.Update(gatedMsg{kind: gateRoster, msg: rosterReadyMsg{err: errAssertRoster}})
	m.Update(gatedMsg{kind: gateGH, msg: ghReadyMsg{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{"/repo": {}},
	}})

	assert.False(t, m.gate(gateRoster).inFlight, "a failed roster query must disarm its gate")
	assert.False(t, m.gate(gateGH).inFlight, "a GitHub result must disarm its gate")
	assert.Nil(t, m.roster, "the roster handler still ran: a failed query clears the roster")
	assert.Contains(t, m.ghState, "/repo", "the GitHub handler still ran")
}
