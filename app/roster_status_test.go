package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/require"
)

// rosterFor builds a one-entry roster keyed to an instance's worktree,
// which is how the app joins Claude's roster to Loom instances.
func rosterFor(inst *session.Instance, status session.RosterStatus) map[string]session.RosterEntry {
	return map[string]session.RosterEntry{
		inst.GetWorktreePath(): {Status: status, WaitingFor: "dialog open"},
	}
}

// TestRosterOverridesScrapedStatus is the payoff of the whole change: the
// scraper's updated=true would normally latch Running and arm a re-detect,
// but Claude's own roster says the session is blocked on a dialog. The
// authoritative answer must win immediately, with no re-detection chain —
// the health tick's next roster refresh is the settle mechanism.
func TestRosterOverridesScrapedStatus(t *testing.T) {
	inst := startedInstanceWithProgram(t, "rosterwins", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterFor(inst, session.RosterStatusWaiting))

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Prompting, inst.GetStatus(),
		"roster 'waiting' must beat the scraper's updated=true Running")
	require.Nil(t, follow, "an authoritative roster answer needs no re-detection")
}

// TestRosterBusyMapsToRunning pins the other direction: a settled pane that
// the scraper would call Ready is still Running if Claude says it is busy
// (e.g. a long tool call that paints nothing).
func TestRosterBusyMapsToRunning(t *testing.T) {
	inst := startedInstanceWithProgram(t, "rosterbusy", "claude", "quiet")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterFor(inst, session.RosterStatusBusy))

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: false})

	require.Equal(t, session.Running, inst.GetStatus())
	require.Nil(t, follow)
}

// TestRosterAbsentFallsBackToScraper: with no roster entry (daemon down,
// Claude too old, session not listed) the existing ladder must behave
// exactly as before — including arming the re-detection.
func TestRosterAbsentFallsBackToScraper(t *testing.T) {
	inst := startedInstanceWithProgram(t, "nofallback", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Running, inst.GetStatus())
	require.NotNil(t, follow, "without a roster the re-detect ladder must still run")
}

// TestRosterUnknownStatusFallsBack: a status string this build does not
// recognize is not an answer — it must not suppress the scraper.
func TestRosterUnknownStatusFallsBack(t *testing.T) {
	inst := startedInstanceWithProgram(t, "rosterunknown", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterFor(inst, session.RosterStatusUnknown))

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Running, inst.GetStatus())
	require.NotNil(t, follow, "an unrecognized roster status must fall through to the ladder")
}

// TestRosterIgnoredForNonClaudeInstance: the roster is Claude's view of the
// world. An aider session that happens to share a directory with a listed
// Claude session must keep its scraped status.
func TestRosterIgnoredForNonClaudeInstance(t *testing.T) {
	inst := startedInstanceWithProgram(t, "aiderinst", "aider", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterFor(inst, session.RosterStatusWaiting))

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Running, inst.GetStatus(),
		"a non-Claude agent must not be governed by Claude's roster")
	require.NotNil(t, follow)
}

// TestRosterReadyMsgStoresEntries: the tick's query result lands on the
// model so later status events can consult it.
func TestRosterReadyMsgStoresEntries(t *testing.T) {
	m := homeWithAppState(t)
	entries := map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}

	_, cmd := m.Update(rosterReadyMsg{entries: entries})

	require.Nil(t, cmd)
	require.Equal(t, entries, m.roster)
}

// TestRosterReadyMsgErrorClearsEntries: a failed query must drop the old
// roster rather than keep driving transitions from stale data — one tick on
// the scraper is safer than acting on a snapshot that may be minutes old.
func TestRosterReadyMsgErrorClearsEntries(t *testing.T) {
	m := homeWithAppState(t)
	m.roster = map[string]session.RosterEntry{"/w/x": {Status: session.RosterStatusIdle}}

	_, cmd := m.Update(rosterReadyMsg{err: errors.New("daemon down")})

	require.Nil(t, cmd)
	require.Empty(t, m.roster, "a failed roster query must not leave stale entries")
}

// TestRosterQueryCmdSkipsWhenNoClaudeInstances: Loom must not shell out to
// the Claude CLI on every tick for a fleet that contains no Claude agents.
func TestRosterQueryCmdSkipsWhenNoClaudeInstances(t *testing.T) {
	inst := startedInstanceWithProgram(t, "aideronly", "aider", "x")
	require.Nil(t, rosterQueryCmd([]*session.Instance{inst}))
}

// TestRosterQueryCmdRunsForClaudeInstances: with at least one Claude agent
// the tick schedules exactly one roster query for the whole fleet.
func TestRosterQueryCmdRunsForClaudeInstances(t *testing.T) {
	aider := startedInstanceWithProgram(t, "mixed-aider", "aider", "x")
	claude := startedInstanceWithProgram(t, "mixed-claude", "claude", "x")
	require.NotNil(t, rosterQueryCmd([]*session.Instance{aider, claude}))
}

// errAssertRoster is a sentinel for roster query failures in tests.
var errAssertRoster = errors.New("roster query failed")

// rosterWithReason builds a one-entry roster carrying an explicit
// waitingFor reason, which is the payload the card label consumes.
func rosterWithReason(inst *session.Instance, status session.RosterStatus, reason string) map[string]session.RosterEntry {
	return map[string]session.RosterEntry{
		inst.GetWorktreePath(): {Status: status, WaitingFor: reason},
	}
}

// TestRosterWaitReasonReachesInstance: Claude names what it is blocked on
// ("sandbox request", "dialog open"). That reason is the whole point of
// preferring the roster over the scraper for a Prompting session — it must
// survive the trip from the query to the instance the card renders.
func TestRosterWaitReasonReachesInstance(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reasonwait", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "sandbox request"))

	m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Prompting, inst.GetStatus())
	require.Equal(t, "sandbox request", inst.WaitReason())
}

// TestRosterWaitReasonClearedWhenNoLongerWaiting: the reason describes a
// live block. Once Claude reports busy again the card must not keep
// advertising a dialog the user already dismissed.
func TestRosterWaitReasonClearedWhenNoLongerWaiting(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reasoncleared", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "dialog open"))
	m.Update(statusDetectedMsg{instance: inst, updated: true})
	require.Equal(t, "dialog open", inst.WaitReason(), "precondition")

	deliverRoster(m, rosterWithReason(inst, session.RosterStatusBusy, ""))
	m.Update(statusDetectedMsg{instance: inst, updated: true})

	require.Equal(t, session.Running, inst.GetStatus())
	require.Empty(t, inst.WaitReason(), "a stale reason must not outlive the wait")
}

// TestRosterWaitReasonClearedWhenRosterGoesAway: a failed or emptied roster
// leaves status to the scraper, which has no notion of a reason. Keeping the
// last one would pin a label that nothing is refreshing.
func TestRosterWaitReasonClearedWhenRosterGoesAway(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reasondropped", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "input needed"))
	m.Update(statusDetectedMsg{instance: inst, updated: true})
	require.Equal(t, "input needed", inst.WaitReason(), "precondition")

	failRoster(m)
	m.Update(statusDetectedMsg{instance: inst, updated: false, hasPrompt: true})

	require.Equal(t, session.Prompting, inst.GetStatus(),
		"the scraper still sees a prompt on screen")
	require.Empty(t, inst.WaitReason(),
		"but only the roster can name a reason, so it must be dropped")
}
