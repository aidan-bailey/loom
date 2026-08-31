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
	m.roster = rosterFor(inst, session.RosterStatusWaiting)

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
	m.roster = rosterFor(inst, session.RosterStatusBusy)

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
	m.roster = nil

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
	m.roster = rosterFor(inst, session.RosterStatusUnknown)

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
	m.roster = rosterFor(inst, session.RosterStatusWaiting)

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
