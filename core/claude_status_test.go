package core

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rosterFor builds a one-entry roster keyed to an instance's worktree,
// which is how the model joins Claude's roster to Loom instances.
func rosterFor(inst *session.Instance, status session.RosterStatus) map[string]session.RosterEntry {
	return map[string]session.RosterEntry{
		inst.GetWorktreePath(): {Status: status, WaitingFor: "dialog open"},
	}
}

// errAssertRoster is a sentinel for roster query failures in tests.
var errAssertRoster = errors.New("roster query failed")

// deliverRoster lands a roster answer the way a finished query does,
// stamped now.
func deliverRoster(m *Model, entries map[string]session.RosterEntry) {
	m.Deliver(rosterResult{entries: entries, at: time.Now()})
}

// failRoster lands a failed roster query.
func failRoster(m *Model) {
	m.Deliver(rosterResult{err: errAssertRoster, at: time.Now()})
}

// applyHookEvents feeds events to inst as a replayed scan would. The test
// instances start on a mock tmux session, so their launch prepared no
// hooks folder: like an instance restored after a loom restart, they have
// no launch ID yet and adopt the result's.
func applyHookEvents(t *testing.T, inst *session.Instance, events ...hooks.Event) {
	t.Helper()
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: events}))
}

// TestRosterResultStoresEntries: the tick's query result lands on the
// model so later status events can consult it.
func TestRosterResultStoresEntries(t *testing.T) {
	m := NewForTest(Options{})
	entries := map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}

	m.Deliver(rosterResult{entries: entries})

	require.True(t, m.Drain().Empty())
	require.Equal(t, entries, m.roster)
}

// TestRosterResultErrorClearsEntries: a failed query must drop the old
// roster rather than keep driving transitions from stale data — one tick on
// the scraper is safer than acting on a snapshot that may be minutes old.
func TestRosterResultErrorClearsEntries(t *testing.T) {
	m := NewForTest(Options{})
	m.roster = map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}

	m.Deliver(rosterResult{err: errors.New("daemon down")})

	require.True(t, m.Drain().Empty())
	require.Empty(t, m.roster, "a failed roster query must not leave stale entries")
}

// TestRosterQueryJobSkipsWhenNoClaudeInstances: Loom must not shell out to
// the Claude CLI on every tick for a fleet that contains no Claude agents.
func TestRosterQueryJobSkipsWhenNoClaudeInstances(t *testing.T) {
	inst := startedInst(t, "aideronly", "aider")
	require.Nil(t, rosterQueryJob([]*session.Instance{inst}, nil))
}

// TestRosterQueryJobRunsForClaudeInstances: with at least one Claude agent
// the tick schedules exactly one roster query for the whole fleet.
func TestRosterQueryJobRunsForClaudeInstances(t *testing.T) {
	aider := startedInst(t, "mixed-aider", "aider")
	claude := startedInst(t, "mixed-claude", "claude")
	require.NotNil(t, rosterQueryJob([]*session.Instance{aider, claude}, nil))
}

// A query that started before a Stop and landed after it must not undo
// the Stop: its busy is older than the hook's Ready.
func TestRosterAnswerOlderThanHookEventIsDropped(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "stale-roster")
	queryStarted := time.Now()
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: queryStarted.Add(100 * time.Millisecond)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Deliver(rosterResult{entries: rosterFor(inst, session.RosterStatusBusy), at: queryStarted})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// A lead that stops while waiting for a teammate's reply, then picks it
// up with no hook event, is corrected by the next roster answer.
func TestNewerRosterAnswerCorrectsIntermediateStop(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "mid-stop")
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	deliverRoster(m, rosterFor(inst, session.RosterStatusBusy))

	assert.Equal(t, session.Running, inst.GetStatus())
}

func TestFailedRosterKeepsHookStatus(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "hook-survives")
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)

	failRoster(m)

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
}

// Answering a prompt makes output but fires no hook, so output on a
// Prompting session asks the roster, no more often than
// promptingRosterSpacing.
func TestPromptingOutputQueriesRosterSpaced(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "prompt-roster")
	require.NoError(t, inst.TransitionTo(session.Prompting))

	m.paneOutputInst(inst)
	require.True(t, m.gate(gateRoster).inFlight, "output on a Prompting session queries the roster")

	m.gate(gateRoster).inFlight = false
	m.paneOutputInst(inst)
	assert.False(t, m.gate(gateRoster).inFlight, "a second query inside promptingRosterSpacing is not dispatched")

	m.gate(gateRoster).last = time.Now().Add(-promptingRosterSpacing)
	m.paneOutputInst(inst)
	assert.True(t, m.gate(gateRoster).inFlight)
}

// The roster query used to ride the health tick directly, which fires every
// 500ms on the snapshot path — a ~380ms `claude agents --json` process at
// 500ms intervals is a permanent ~76% duty cycle, and with no in-flight
// guard a slow CLI could stack ~10 concurrent processes before the 5s
// timeout tripped. maybeRosterQuery decouples the cadence from the tick.

func TestRosterQueryDispatchesOnFirstCall(t *testing.T) {
	inst := startedInst(t, "throttle-first", "claude")
	m := NewForTest(Options{})

	require.True(t, m.maybeRosterQuery([]*session.Instance{inst}),
		"the first tick must dispatch a query")
	require.True(t, m.gate(gateRoster).inFlight)
}

func TestRosterQueryThrottledWithinInterval(t *testing.T) {
	inst := startedInst(t, "throttle-window", "claude")
	m := NewForTest(Options{})
	active := []*session.Instance{inst}

	require.True(t, m.maybeRosterQuery(active))
	m.gate(gateRoster).inFlight = false // pretend the first query already returned

	require.False(t, m.maybeRosterQuery(active),
		"a second tick inside the interval must not spawn another process")
}

func TestRosterQueryResumesAfterInterval(t *testing.T) {
	inst := startedInst(t, "throttle-resume", "claude")
	m := NewForTest(Options{})
	active := []*session.Instance{inst}

	require.True(t, m.maybeRosterQuery(active))
	m.gate(gateRoster).inFlight = false
	m.gate(gateRoster).last = time.Now().Add(-rosterInterval - time.Second)

	require.True(t, m.maybeRosterQuery(active),
		"once the interval has elapsed the query must run again")
}

func TestRosterQueryNotStackedWhileInFlight(t *testing.T) {
	inst := startedInst(t, "throttle-inflight", "claude")
	m := NewForTest(Options{})
	active := []*session.Instance{inst}

	require.True(t, m.maybeRosterQuery(active))
	// Interval has elapsed, but the previous query has not come back — a
	// hung CLI must not accumulate concurrent processes.
	m.gate(gateRoster).last = time.Now().Add(-rosterInterval - time.Second)

	require.False(t, m.maybeRosterQuery(active),
		"an outstanding query must suppress the next dispatch")
}

func TestRosterResultClearsInFlight(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateRoster).inFlight = true

	m.Deliver(gatedResult{kind: gateRoster, result: rosterResult{entries: map[string]session.RosterEntry{}}})

	require.False(t, m.gate(gateRoster).inFlight, "a delivered result must re-arm the next query")
}

func TestRosterResultClearsInFlightOnError(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateRoster).inFlight = true

	m.Deliver(gatedResult{kind: gateRoster, result: rosterResult{err: errAssertRoster}})

	require.False(t, m.gate(gateRoster).inFlight,
		"a failed query must re-arm too, or the roster latches off forever")
}

// TestRosterQueryNoClaudeDoesNotLatchInFlight guards a deadlock: when no
// Claude agent is running there is no query and therefore no rosterResult
// to clear the flag, so the flag must never be set in the first place.
func TestRosterQueryNoClaudeDoesNotLatchInFlight(t *testing.T) {
	inst := startedInst(t, "throttle-aider", "aider")
	m := NewForTest(Options{})

	require.False(t, m.maybeRosterQuery([]*session.Instance{inst}))
	require.False(t, m.gate(gateRoster).inFlight,
		"no dispatch means no in-flight flag — nothing would ever clear it")
	require.True(t, m.gate(gateRoster).last.IsZero(),
		"a skipped query must not start the throttle window either")
}

func TestRosterStatusFor_JoinsTheInstancesAccountRoster(t *testing.T) {
	inst := startedInst(t, "acct-join", "claude")
	inst.SetAccount("max-2")
	m := NewForTest(Options{})
	wt := inst.GetWorktreePath()
	m.roster = map[string]session.RosterEntry{wt: {Status: session.RosterStatusBusy}}
	m.rosterByAccount = map[string]map[string]session.RosterEntry{"max-2": {wt: {Status: session.RosterStatusIdle}}}

	status, _, ok := m.rosterStatusFor(inst)

	require.True(t, ok)
	assert.Equal(t, session.Ready, status, "an account's session is joined against that account's roster")
}

func TestRosterStatusFor_TheDefaultRosterNeverAnswersForAnotherAccount(t *testing.T) {
	inst := startedInst(t, "acct-none", "claude")
	inst.SetAccount("max-2")
	m := NewForTest(Options{})
	m.roster = map[string]session.RosterEntry{inst.GetWorktreePath(): {Status: session.RosterStatusBusy}}

	_, _, ok := m.rosterStatusFor(inst)

	assert.False(t, ok)
}

func TestRosterResult_OneAccountsFailureKeepsTheOthers(t *testing.T) {
	m := NewForTest(Options{})

	m.Deliver(rosterResult{
		entries:   map[string]session.RosterEntry{"/w": {Status: session.RosterStatusBusy}},
		extra:     map[string]map[string]session.RosterEntry{"max-3": {"/v": {Status: session.RosterStatusIdle}}},
		extraErrs: map[string]error{"max-2": errors.New("daemon down")},
	})

	assert.Len(t, m.roster, 1)
	assert.Contains(t, m.rosterByAccount, "max-3")
	assert.NotContains(t, m.rosterByAccount, "max-2")
}

// TestRosterQueryJob_NeverQueriesAnAccountAsAnother: an instance whose
// account is gone from the registry, or whose account dir vanished, must
// not be answered for by some other account's roster. Neither case gets
// as far as spawning the CLI, so this runs no subprocess.
func TestRosterQueryJob_NeverQueriesAnAccountAsAnother(t *testing.T) {
	gone := startedInst(t, "acct-gone", "claude")
	gone.SetAccount("gone")
	missing := startedInst(t, "acct-missing", "claude")
	missing.SetAccount("max-2")
	dirs := map[string]string{"max-2": filepath.Join(t.TempDir(), "deleted")}

	job := rosterQueryJob([]*session.Instance{gone, missing}, dirs)
	require.NotNil(t, job)
	msg, ok := job().(rosterResult)
	require.True(t, ok)

	assert.Nil(t, msg.entries, "no default-account instance: no default query")
	assert.NoError(t, msg.err)
	assert.NotContains(t, msg.extra, "gone", "an unregistered account is not queried at all")
	assert.NotContains(t, msg.extraErrs, "gone")
	assert.ErrorIs(t, msg.extraErrs["max-2"], account.ErrAccountDirMissing)
}
