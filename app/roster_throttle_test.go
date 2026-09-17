package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/require"
)

// The roster query used to ride the health tick directly, which fires every
// 500ms on the snapshot path — a ~380ms `claude agents --json` process at
// 500ms intervals is a permanent ~76% duty cycle, and with no in-flight
// guard a slow CLI could stack ~10 concurrent processes before the 5s
// timeout tripped. maybeRosterQuery decouples the cadence from the tick.

func TestRosterQueryDispatchesOnFirstCall(t *testing.T) {
	inst := startedInstanceWithProgram(t, "throttle-first", "claude", "x")
	m := homeWithAppState(t)

	require.NotNil(t, m.maybeRosterQuery([]*session.Instance{inst}),
		"the first tick must dispatch a query")
	require.True(t, m.rosterInFlight)
}

func TestRosterQueryThrottledWithinInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "throttle-window", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeRosterQuery(active))
	m.rosterInFlight = false // pretend the first query already returned

	require.Nil(t, m.maybeRosterQuery(active),
		"a second tick inside the interval must not spawn another process")
}

func TestRosterQueryResumesAfterInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "throttle-resume", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeRosterQuery(active))
	m.rosterInFlight = false
	m.lastRosterQuery = time.Now().Add(-rosterInterval - time.Second)

	require.NotNil(t, m.maybeRosterQuery(active),
		"once the interval has elapsed the query must run again")
}

func TestRosterQueryNotStackedWhileInFlight(t *testing.T) {
	inst := startedInstanceWithProgram(t, "throttle-inflight", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeRosterQuery(active))
	// Interval has elapsed, but the previous query has not come back — a
	// hung CLI must not accumulate concurrent processes.
	m.lastRosterQuery = time.Now().Add(-rosterInterval - time.Second)

	require.Nil(t, m.maybeRosterQuery(active),
		"an outstanding query must suppress the next dispatch")
}

func TestRosterReadyMsgClearsInFlight(t *testing.T) {
	m := homeWithAppState(t)
	m.rosterInFlight = true

	m.Update(rosterReadyMsg{entries: map[string]session.RosterEntry{}})

	require.False(t, m.rosterInFlight, "a delivered result must re-arm the next query")
}

func TestRosterReadyMsgClearsInFlightOnError(t *testing.T) {
	m := homeWithAppState(t)
	m.rosterInFlight = true

	m.Update(rosterReadyMsg{err: errAssertRoster})

	require.False(t, m.rosterInFlight,
		"a failed query must re-arm too, or the roster latches off forever")
}

// TestRosterQueryNoClaudeDoesNotLatchInFlight guards a deadlock: when no
// Claude agent is running there is no query and therefore no rosterReadyMsg
// to clear the flag, so the flag must never be set in the first place.
func TestRosterQueryNoClaudeDoesNotLatchInFlight(t *testing.T) {
	inst := startedInstanceWithProgram(t, "throttle-aider", "aider", "x")
	m := homeWithAppState(t)

	require.Nil(t, m.maybeRosterQuery([]*session.Instance{inst}))
	require.False(t, m.rosterInFlight,
		"no dispatch means no in-flight flag — nothing would ever clear it")
	require.True(t, m.lastRosterQuery.IsZero(),
		"a skipped query must not start the throttle window either")
}
