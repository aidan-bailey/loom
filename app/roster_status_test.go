package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/require"
)

// rosterFor builds a one-entry roster keyed to an instance's worktree,
// which is how the model joins Claude's roster to Loom instances.
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
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterFor(inst, session.RosterStatusWaiting))

	_, follow := m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

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
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterFor(inst, session.RosterStatusBusy))

	_, follow := m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: false})

	require.Equal(t, session.Running, inst.GetStatus())
	require.Nil(t, follow)
}

// TestRosterAbsentFallsBackToScraper: with no roster entry (daemon down,
// Claude too old, session not listed) the existing ladder must behave
// exactly as before — including arming the re-detection. The model's
// status is set apart from the scraper's answer, so the row shows which
// one won.
func TestRosterAbsentFallsBackToScraper(t *testing.T) {
	inst := startedInstanceWithProgram(t, "nofallback", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.ws.AddForTest(inst)
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()

	_, follow := m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

	require.Equal(t, session.Running, shownStatus(t, m, inst), "the row shows the scraper's Running")
	require.Equal(t, session.Ready, inst.GetStatus(), "which is the TUI's overlay, not the model's")
	require.NotNil(t, follow, "without a roster the re-detect ladder must still run")
}

// TestRosterUnknownStatusFallsBack: a status string this build does not
// recognize is not an answer — it must not suppress the scraper.
func TestRosterUnknownStatusFallsBack(t *testing.T) {
	inst := startedInstanceWithProgram(t, "rosterunknown", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.ws.AddForTest(inst)
	require.NoError(t, inst.TransitionTo(session.Ready))
	m.syncViews()
	deliverRoster(m, rosterFor(inst, session.RosterStatusUnknown))

	_, follow := m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

	require.Equal(t, session.Running, shownStatus(t, m, inst), "the row shows the scraper's Running")
	require.Equal(t, session.Ready, inst.GetStatus(), "and the unrecognized answer moved nothing in the model")
	require.NotNil(t, follow, "an unrecognized roster status must fall through to the ladder")
}

// TestRosterIgnoredForNonClaudeInstance: the roster is Claude's view of the
// world. An aider session that happens to share a directory with a listed
// Claude session must keep its scraped status.
func TestRosterIgnoredForNonClaudeInstance(t *testing.T) {
	inst := startedInstanceWithProgram(t, "aiderinst", "aider", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterFor(inst, session.RosterStatusWaiting))

	_, follow := m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

	require.Equal(t, session.Running, inst.GetStatus(),
		"a non-Claude agent must not be governed by Claude's roster")
	require.NotNil(t, follow)
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
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "sandbox request"))

	m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

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
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "dialog open"))
	m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})
	require.Equal(t, "dialog open", inst.WaitReason(), "precondition")

	deliverRoster(m, rosterWithReason(inst, session.RosterStatusBusy, ""))
	m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})

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
	m.ws.AddForTest(inst)
	m.syncViews()
	deliverRoster(m, rosterWithReason(inst, session.RosterStatusWaiting, "input needed"))
	m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: true})
	require.Equal(t, "input needed", inst.WaitReason(), "precondition")

	failRoster(m)
	m.Update(statusDetectedMsg{id: idOf(m, inst), title: inst.Title, updated: false, hasPrompt: true})

	require.Equal(t, session.Prompting, m.ladder[idOf(m, inst)].status,
		"the scraper still sees a prompt on screen")
	require.Equal(t, session.Prompting, shownStatus(t, m, inst), "and the row shows it")
	require.Equal(t, session.Prompting, inst.GetStatus(),
		"the model keeps the roster's last status: a failed query is no opinion, not a change")
	require.Empty(t, inst.WaitReason(),
		"but only the roster can name a reason, so it must be dropped")
}
