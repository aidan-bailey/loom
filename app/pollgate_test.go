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

func TestGateIntervalsUseEachJobsInterval(t *testing.T) {
	assert.Equal(t, rosterInterval, gateIntervals[gateRoster])
	assert.Equal(t, hookScanInterval, gateIntervals[gateHookScan])
	assert.Equal(t, ghInterval, gateIntervals[gateGH])
	assert.Zero(t, gateIntervals[gateRatioSave], "the ratio flush paces itself with its own tick")
}

func TestPollGateDueRespectsIntervalAndInFlight(t *testing.T) {
	now := time.Now()
	var g pollGate
	assert.True(t, g.due(now, time.Second), "a gate that never dispatched is due")

	g.last = now
	assert.False(t, g.due(now.Add(500*time.Millisecond), time.Second), "inside the interval")
	assert.True(t, g.due(now.Add(time.Second), time.Second), "the interval has elapsed")

	g.inFlight = true
	assert.False(t, g.due(now.Add(time.Hour), time.Second), "an outstanding dispatch blocks the next one")
}

func TestPollGateZeroIntervalOnlyDedupes(t *testing.T) {
	now := time.Now()
	g := pollGate{last: now}
	assert.True(t, g.due(now, 0))
	g.inFlight = true
	assert.False(t, g.due(now, 0))
}

func TestPollGateExpedite(t *testing.T) {
	now := time.Now()
	g := pollGate{last: now}
	require.False(t, g.due(now, time.Hour))

	g.expedite()
	assert.True(t, g.due(now, time.Hour), "an expedited gate is due at once")

	g.last = now
	g.inFlight = true
	g.expedite()
	assert.False(t, g.due(now, time.Hour), "expedite does not cut an in-flight dispatch short")
	g.inFlight = false
	assert.True(t, g.due(now, time.Hour), "it polls again as soon as that one is delivered")
}

// A zero-value home must be throttled like a production one: the
// intervals are keyed by kind, not installed by a constructor, so no
// construction path can leave a job polling on every tick.
func TestZeroValueHomeThrottlesRoster(t *testing.T) {
	m := &home{}
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "zero-home", Path: t.TempDir(), Program: "/nonexistent/loom-test/claude",
	})
	require.NoError(t, err)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeRosterQuery(active))
	m.gate(gateRoster).inFlight = false // pretend the first query already returned

	assert.Nil(t, m.maybeRosterQuery(active), "a second query inside rosterInterval must not dispatch")
	m.gate(gateRoster).last = time.Now().Add(-rosterInterval - time.Second)
	assert.NotNil(t, m.maybeRosterQuery(active), "once the interval has elapsed it runs again")
}

func TestDispatchGatedNilBuildArmsNothing(t *testing.T) {
	m := &home{}

	assert.Nil(t, m.dispatchGated(gateRoster, time.Now(), func() tea.Cmd { return nil }))
	assert.False(t, m.gate(gateRoster).inFlight, "no Cmd means no delivery to disarm it")
	assert.True(t, m.gate(gateRoster).last.IsZero(), "nor does it start the interval")
}

func TestDispatchGatedArmsAndWrapsResult(t *testing.T) {
	m := &home{}
	now := time.Now()

	cmd := m.dispatchGated(gateGH, now, func() tea.Cmd { return resultCmd(7) })
	require.NotNil(t, cmd)
	assert.True(t, m.gate(gateGH).inFlight)
	assert.Equal(t, now, m.gate(gateGH).last)
	assert.False(t, m.gate(gateRoster).inFlight, "only the named gate arms")

	assert.Equal(t, gatedMsg{kind: gateGH, msg: pollResultMsg{n: 7}}, cmd())
}

func TestDispatchGatedWrapsATickOnceItFires(t *testing.T) {
	m := &home{}

	cmd := m.dispatchGated(gateRatioSave, time.Now(), func() tea.Cmd {
		return tea.Tick(time.Millisecond, func(time.Time) tea.Msg { return pollResultMsg{n: 1} })
	})
	require.NotNil(t, cmd)
	assert.Equal(t, gatedMsg{kind: gateRatioSave, msg: pollResultMsg{n: 1}}, cmd())
}

func TestDispatchGatedSkipsBuildWhenNotDue(t *testing.T) {
	m := &home{}
	now := time.Now()
	require.NotNil(t, m.dispatchGated(gateHookScan, now, func() tea.Cmd { return resultCmd(1) }))

	called := false
	build := func() tea.Cmd { called = true; return resultCmd(2) }

	assert.Nil(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval), build),
		"in flight, even with the interval elapsed")
	m.gate(gateHookScan).inFlight = false
	assert.Nil(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval/2), build),
		"delivered, but inside the interval")
	assert.False(t, called, "build must not run when the gate is not due: it reads model state and may be costly")

	assert.NotNil(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval), build))
	assert.True(t, called)
}

func TestGatedDeliveryDisarmsFirst(t *testing.T) {
	m := homeWithAppState(t)
	for _, kind := range []gateKind{gateRoster, gateHookScan, gateGH, gateRatioSave} {
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

// A builder that broke the single-message rule still disarms its gate,
// and the dropped batch is logged rather than lost silently.
func TestGatedBatchMsgStillDisarms(t *testing.T) {
	m := homeWithAppState(t)
	m.gate(gateGH).inFlight = true

	_, cmd := m.Update(gatedMsg{kind: gateGH, msg: tea.BatchMsg{resultCmd(1)}})

	assert.False(t, m.gate(gateGH).inFlight)
	assert.Nil(t, cmd)
}

// TestProductionGatedCmdsYieldOneMessage runs each gated job's Cmd once
// and checks it produces its own result type, never a tea.BatchMsg that
// Update could not expand. The roster points at a nonexistent binary so
// the query fails fast; the GitHub poll runs ghPollCmd (what maybeGHQuery
// builds) against a fake executor rather than real git and gh.
func TestProductionGatedCmdsYieldOneMessage(t *testing.T) {
	inner := func(t *testing.T, cmd tea.Cmd, kind gateKind) tea.Msg {
		t.Helper()
		require.NotNil(t, cmd)
		gm, ok := cmd().(gatedMsg)
		require.True(t, ok, "a gated Cmd must deliver a gatedMsg")
		require.Equal(t, kind, gm.kind)
		require.NotNil(t, gm.msg)
		_, isBatch := gm.msg.(tea.BatchMsg)
		require.False(t, isBatch, "Update cannot expand a wrapped batch")
		return gm.msg
	}

	t.Run("roster", func(t *testing.T) {
		m := homeWithAppState(t)
		inst, err := session.NewInstance(session.InstanceOptions{
			Title: "one-msg-roster", Path: t.TempDir(), Program: "/nonexistent/loom-test/claude",
		})
		require.NoError(t, err)
		msg := inner(t, m.maybeRosterQuery([]*session.Instance{inst}), gateRoster)
		assert.IsType(t, rosterReadyMsg{}, msg)
	})

	t.Run("subagent", func(t *testing.T) {
		m := homeWithAppState(t)
		inst := startedInstanceWithProgram(t, "one-msg-sub", "claude", "x")
		msg := inner(t, m.maybeHookScan([]*session.Instance{inst}), gateHookScan)
		assert.IsType(t, hookScanMsg{}, msg)
	})

	t.Run("github", func(t *testing.T) {
		m := homeWithAppState(t)
		req := ghPollRequest{repos: []string{"/r"}, linked: map[string][]int{}, configured: map[string]string{}, check: true}
		msg := inner(t, m.dispatchGated(gateGH, time.Now(), func() tea.Cmd {
			return ghPollCmd(req, &ghFakeExec{})
		}), gateGH)
		assert.IsType(t, ghReadyMsg{}, msg)
	})

	t.Run("ratio_save", func(t *testing.T) {
		m := newTestHome(t)
		mustAddInstance(t, m, "one-msg-ratio")
		m.resizeSplit(+0.05)
		msg := inner(t, m.maybeArmRatioSave(), gateRatioSave) // waits out ratioSaveDelay
		assert.IsType(t, ratioSaveMsg{}, msg)
	})
}

func TestPollGateRequest(t *testing.T) {
	now := time.Now()
	g := pollGate{last: now}
	g.request()
	assert.True(t, g.due(now, time.Hour), "a request makes an idle gate due at once")
	assert.False(t, g.pending, "nothing in flight: the caller's own dispatch is the follow-up")

	g.inFlight = true
	g.request()
	g.request()
	assert.True(t, g.pending, "requests during a flight collapse into one follow-up")
}

func TestDeliverGatedRedispatchesPendingOnce(t *testing.T) {
	inst := startedInstanceWithProgram(t, "gate-redispatch", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	require.NotNil(t, m.maybeRosterQuery(m.activeInstances()))
	m.gate(gateRoster).request()

	_, cmd := m.Update(gatedMsg{kind: gateRoster, msg: rosterReadyMsg{}})

	require.NotNil(t, cmd, "the pending request dispatches again as soon as the flight lands")
	assert.True(t, m.gate(gateRoster).inFlight)
	assert.False(t, m.gate(gateRoster).pending)

	_, cmd = m.Update(gatedMsg{kind: gateRoster, msg: rosterReadyMsg{}})
	assert.Nil(t, cmd, "no request, no follow-up")
	assert.False(t, m.gate(gateRoster).inFlight)
}
