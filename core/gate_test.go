package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pollResult stands in for a gated job's result.
type pollResult struct{ n int }

func resultJob(n int) Job {
	return func() any { return pollResult{n: n} }
}

func TestGateIntervalsUseEachJobsInterval(t *testing.T) {
	assert.Equal(t, rosterInterval, gateIntervals[gateRoster])
	assert.Equal(t, hookScanInterval, gateIntervals[gateHookScan])
	assert.Equal(t, ghInterval, gateIntervals[gateGH])
	assert.Equal(t, usageInterval, gateIntervals[gateUsage])
	assert.Zero(t, gateIntervals[gateAccountsRefresh], "account refreshes run on events, not a cadence")
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

// A zero-value Model must be throttled like a production one: the
// intervals are keyed by kind, not installed by a constructor, so no
// construction path can leave a job polling on every tick.
func TestZeroValueModelThrottlesRoster(t *testing.T) {
	m := &Model{}
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "zero-home", Path: t.TempDir(), Program: "/nonexistent/loom-test/claude",
	})
	require.NoError(t, err)
	active := []*session.Instance{inst}

	require.True(t, m.maybeRosterQuery(active))
	m.gate(gateRoster).inFlight = false // pretend the first query already returned

	assert.False(t, m.maybeRosterQuery(active), "a second query inside rosterInterval must not dispatch")
	m.gate(gateRoster).last = time.Now().Add(-rosterInterval - time.Second)
	assert.True(t, m.maybeRosterQuery(active), "once the interval has elapsed it runs again")
}

func TestDispatchGatedNilBuildArmsNothing(t *testing.T) {
	m := &Model{}

	assert.False(t, m.dispatchGated(gateRoster, time.Now(), func() Job { return nil }))
	assert.False(t, m.gate(gateRoster).inFlight, "no Job means no delivery to disarm it")
	assert.True(t, m.gate(gateRoster).last.IsZero(), "nor does it start the interval")
}

func TestDispatchGatedArmsAndWrapsResult(t *testing.T) {
	m := &Model{}
	now := time.Now()

	dispatched := m.dispatchGated(gateGH, now, func() Job { return resultJob(7) })
	require.True(t, dispatched)
	assert.True(t, m.gate(gateGH).inFlight)
	assert.Equal(t, now, m.gate(gateGH).last)
	assert.False(t, m.gate(gateRoster).inFlight, "only the named gate arms")

	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	assert.Equal(t, gatedResult{kind: gateGH, result: pollResult{n: 7}}, jobs[0]())
}

// A job that blocks before it answers (as the ratio flush's tea.Tick did)
// is wrapped all the same once it returns.
func TestDispatchGatedWrapsABlockingJobOnceItReturns(t *testing.T) {
	m := &Model{}

	dispatched := m.dispatchGated(gateAccountsRefresh, time.Now(), func() Job {
		return func() any { time.Sleep(time.Millisecond); return pollResult{n: 1} }
	})
	require.True(t, dispatched)
	assert.Equal(t, gatedResult{kind: gateAccountsRefresh, result: pollResult{n: 1}}, m.Drain().Jobs[0]())
}

func TestDispatchGatedSkipsBuildWhenNotDue(t *testing.T) {
	m := &Model{}
	now := time.Now()
	require.True(t, m.dispatchGated(gateHookScan, now, func() Job { return resultJob(1) }))

	called := false
	build := func() Job { called = true; return resultJob(2) }

	assert.False(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval), build),
		"in flight, even with the interval elapsed")
	m.gate(gateHookScan).inFlight = false
	assert.False(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval/2), build),
		"delivered, but inside the interval")
	assert.False(t, called, "build must not run when the gate is not due: it reads model state and may be costly")

	assert.True(t, m.dispatchGated(gateHookScan, now.Add(hookScanInterval), build))
	assert.True(t, called)
}

func TestGatedDeliveryDisarmsFirst(t *testing.T) {
	m := NewForTest(Options{})
	for _, kind := range []gateKind{gateRoster, gateHookScan, gateGH, gateUsage, gateAccountsRefresh} {
		m.gate(kind).inFlight = true
		// A result no case handles, and a nil one, disarm all the
		// same: disarming belongs to the wrapper, not the handler.
		m.Deliver(gatedResult{kind: kind, result: pollResult{}})
		assert.False(t, m.gate(kind).inFlight, "kind %d", kind)

		m.gate(kind).inFlight = true
		m.Deliver(gatedResult{kind: kind})
		assert.False(t, m.gate(kind).inFlight, "kind %d, nil result", kind)
	}
}

// TestGatedDeliveryDisarmsRosterErrorAndGitHubResult drives both result
// shapes that once had to disarm by hand through Deliver: a failed roster
// query (the easy one to miss) and a GitHub poll. Each gate disarms, and
// each inner result still reaches its handler.
func TestGatedDeliveryDisarmsRosterErrorAndGitHubResult(t *testing.T) {
	m := NewForTest(Options{})
	m.roster = map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}
	m.gate(gateRoster).inFlight = true
	m.gate(gateGH).inFlight = true

	m.Deliver(gatedResult{kind: gateRoster, result: rosterResult{err: errAssertRoster}})
	m.Deliver(gatedResult{kind: gateGH, result: ghResult{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{"/repo": {}},
	}})

	assert.False(t, m.gate(gateRoster).inFlight, "a failed roster query must disarm its gate")
	assert.False(t, m.gate(gateGH).inFlight, "a GitHub result must disarm its gate")
	assert.Nil(t, m.roster, "the roster handler still ran: a failed query clears the roster")
	assert.Contains(t, m.ghState, "/repo", "the GitHub handler still ran")
}

// A builder that broke the single-result rule (its job answers with
// another job) still disarms its gate, and the stray job is logged as an
// unknown result rather than run.
func TestGatedNestedJobStillDisarms(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).inFlight = true

	m.Deliver(gatedResult{kind: gateGH, result: resultJob(1)})

	assert.False(t, m.gate(gateGH).inFlight)
	assert.True(t, m.Drain().Empty())
}

// TestProductionGatedJobsYieldOneResult runs each gated job once and
// checks it produces its own result type inside one gatedResult. The
// roster points at a nonexistent binary so the query fails fast; the
// GitHub poll runs ghPollJob (what maybeGHQuery builds) against a fake
// executor rather than real git and gh.
func TestProductionGatedJobsYieldOneResult(t *testing.T) {
	inner := func(t *testing.T, m *Model, dispatched bool, kind gateKind) any {
		t.Helper()
		require.True(t, dispatched)
		jobs := m.Drain().Jobs
		require.Len(t, jobs, 1)
		gr, ok := jobs[0]().(gatedResult)
		require.True(t, ok, "a gated job must deliver a gatedResult")
		require.Equal(t, kind, gr.kind)
		require.NotNil(t, gr.result)
		_, nested := gr.result.(gatedResult)
		require.False(t, nested, "Deliver unwraps one gatedResult, not two")
		return gr.result
	}

	t.Run("roster", func(t *testing.T) {
		m := NewForTest(Options{})
		inst, err := session.NewInstance(session.InstanceOptions{
			Title: "one-msg-roster", Path: t.TempDir(), Program: "/nonexistent/loom-test/claude",
		})
		require.NoError(t, err)
		result := inner(t, m, m.maybeRosterQuery([]*session.Instance{inst}), gateRoster)
		assert.IsType(t, rosterResult{}, result)
	})

	t.Run("subagent", func(t *testing.T) {
		m := NewForTest(Options{})
		inst := startedInst(t, "one-msg-sub", "claude")
		result := inner(t, m, m.maybeHookScan([]*session.Instance{inst}), gateHookScan)
		assert.IsType(t, hookScanResults{}, result)
	})

	t.Run("github", func(t *testing.T) {
		m := NewForTest(Options{})
		req := ghPollRequest{repos: []string{"/r"}, linked: map[string][]int{}, configured: map[string]string{}, check: true}
		result := inner(t, m, m.dispatchGated(gateGH, time.Now(), func() Job {
			return ghPollJob(req, &ghFakeExec{})
		}), gateGH)
		assert.IsType(t, ghResult{}, result)
	})

	t.Run("usage", func(t *testing.T) {
		m := NewForTest(Options{Program: "/nonexistent/loom-test/claude"})
		main := withAccounts(t, m, "max-2")
		editRCAuth(m, func(a *session.RemoteControlAuth) { a.Identity.ConfigDir = main })
		m.Drain()
		result := inner(t, m, m.maybeUsageProbe(), gateUsage)
		assert.IsType(t, usageResult{}, result)
	})

	t.Run("accounts_refresh", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // account.MainDir's fallback
		m := NewForTest(Options{Program: "/nonexistent/loom-test/claude"})
		withAccounts(t, m, "max-2")
		m.Drain()
		m.RequestAccountsRefresh(true)
		result := inner(t, m, m.gate(gateAccountsRefresh).inFlight, gateAccountsRefresh)
		assert.IsType(t, accountsRefreshed{}, result)
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
	m := NewForTest(Options{})
	activeInst(t, m, "gate-redispatch")
	require.True(t, m.maybeRosterQuery(m.activeInstances()))
	m.gate(gateRoster).request()
	m.Drain()

	m.Deliver(gatedResult{kind: gateRoster, result: rosterResult{}})

	require.NotEmpty(t, m.Drain().Jobs, "the pending request dispatches again as soon as the flight lands")
	assert.True(t, m.gate(gateRoster).inFlight)
	assert.False(t, m.gate(gateRoster).pending)

	m.Deliver(gatedResult{kind: gateRoster, result: rosterResult{}})
	assert.Empty(t, m.Drain().Jobs, "no request, no follow-up")
	assert.False(t, m.gate(gateRoster).inFlight)
}
