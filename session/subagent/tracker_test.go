package subagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const transcript = "/p/-work/sess.jsonl"

func startEv(id, agentType string) Event {
	return Event{Name: EventSubagentStart, AgentID: id, AgentType: agentType, TranscriptPath: transcript}
}
func stopEv(id string) Event      { return Event{Name: EventSubagentStop, AgentID: id} }
func idleEv(name string) Event    { return Event{Name: EventTeammateIdle, TeammateName: name} }
func sessionEnd() Event           { return Event{Name: EventSessionEnd} }
func parentStop(ts ...Task) Event { return Event{Name: EventStop, Tasks: ts, HasTasks: true} }
func runningSub(id string) Task   { return Task{ID: id, Type: "subagent", Status: "running"} }
func runningMate() Task           { return Task{ID: "tk", Type: "teammate", Status: "running"} }

func plainMeta(agentType, desc string) Meta { return Meta{AgentType: agentType, Description: desc} }
func mateMeta(name, desc string) Meta {
	return Meta{AgentType: name, Name: name, Description: desc, TaskKind: "in_process_teammate"}
}

func TestTracker_PlainLifecycle(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore")}, map[string]Meta{"a1": plainMeta("Explore", "map code")})
	assert.Equal(t, []View{{Name: "Explore", Description: "map code"}}, tr.Visible())

	tr.Apply([]Event{stopEv("a1")}, nil)
	assert.Empty(t, tr.Visible(), "a plain subagent ends when it stops")
	assert.NotNil(t, tr.Visible(), "Visible never returns nil")
}

func TestTracker_TeammateCycle(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{"amate-1": mateMeta("mate", "implement task")}

	tr.Apply([]Event{startEv("amate-1", "mate")}, meta)
	assert.Equal(t, []View{{Name: "mate", Description: "implement task"}}, tr.Visible())

	tr.Apply([]Event{stopEv("amate-1")}, nil)
	assert.Equal(t, []View{{Name: "mate", Description: "implement task", Idle: true}}, tr.Visible(),
		"a stopped teammate is shown as idle while its TeammateIdle is pending")

	tr.Apply([]Event{idleEv("mate")}, nil)
	assert.True(t, tr.Visible()[0].Idle)

	tr.Apply([]Event{startEv("amate-1", "mate")}, nil)
	require.Len(t, tr.Visible(), 1, "re-tasking reuses the agent ID")
	assert.False(t, tr.Visible()[0].Idle)

	tr.Apply([]Event{stopEv("amate-1"), idleEv("mate")}, nil)
	assert.True(t, tr.Visible()[0].Idle)
}

func TestTracker_FullShutdownRemovesTeammates(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("amate-1", "mate"), stopEv("amate-1"), idleEv("mate")},
		map[string]Meta{"amate-1": mateMeta("mate", "x")})

	// Observed shutdown: re-tasked for the request, stopped without idle,
	// then the lead's Stop lists no teammates.
	tr.Apply([]Event{startEv("amate-1", "mate"), stopEv("amate-1"), parentStop()}, nil)
	assert.Empty(t, tr.Visible())
}

func TestTracker_PartialShutdownRemovesStoppingFirst(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{"aa-1": mateMeta("a", "x"), "ab-1": mateMeta("b", "y")}
	tr.Apply([]Event{
		startEv("aa-1", "a"), stopEv("aa-1"), idleEv("a"),
		startEv("ab-1", "b"), stopEv("ab-1"), idleEv("b"),
	}, meta)

	// a is shut down (stop, no idle); one teammate is still running.
	tr.Apply([]Event{startEv("aa-1", "a"), stopEv("aa-1"), parentStop(runningMate())}, nil)
	assert.Equal(t, []View{{Name: "b", Description: "y", Idle: true}}, tr.Visible())
}

func TestTracker_PartialShutdownFallsBackToMostRecentlyStopped(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{"aa-1": mateMeta("a", "x"), "ab-1": mateMeta("b", "y")}
	tr.Apply([]Event{
		startEv("aa-1", "a"), startEv("ab-1", "b"),
		stopEv("aa-1"), idleEv("a"),
		stopEv("ab-1"), idleEv("b"),
	}, meta)

	tr.Apply([]Event{parentStop(runningMate())}, nil)
	assert.Equal(t, []View{{Name: "a", Description: "x", Idle: true}}, tr.Visible(),
		"with no Stopping teammate, the most recently stopped one goes")
}

func TestTracker_SurvivingStoppingTeammateBecomesIdle(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("aa-1", "a"), stopEv("aa-1")}, map[string]Meta{"aa-1": mateMeta("a", "x")})
	tr.Apply([]Event{parentStop(runningMate())}, nil)
	require.Len(t, tr.Visible(), 1)
	assert.True(t, tr.Visible()[0].Idle)
}

func TestTracker_UnknownStopIgnored(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{stopEv("a2e1f32d0ce479a2f")}, nil)
	assert.Empty(t, tr.Visible())
	assert.Empty(t, tr.MissingMeta(), "internal helper agents are never tracked")
}

func TestTracker_HiddenUntilMeta(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore")}, nil)
	assert.Empty(t, tr.Visible())
	assert.Equal(t, []MetaRef{{AgentID: "a1", Path: "/p/-work/sess/subagents/agent-a1.meta.json"}},
		tr.MissingMeta())

	tr.Apply(nil, map[string]Meta{"a1": plainMeta("Explore", "d")})
	assert.Len(t, tr.Visible(), 1)
	assert.Empty(t, tr.MissingMeta())
}

func TestTracker_PlainStopBeforeMetaEndsWhenMetaArrives(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore"), stopEv("a1")}, nil)
	assert.Len(t, tr.MissingMeta(), 1, "kind unknown: kept, hidden")

	tr.Apply(nil, map[string]Meta{"a1": plainMeta("Explore", "d")})
	assert.Empty(t, tr.Visible())
	assert.Empty(t, tr.MissingMeta())
}

func TestTracker_TeammateStopBeforeMetaStaysIdle(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("amate-1", "mate"), stopEv("amate-1")}, nil)
	tr.Apply(nil, map[string]Meta{"amate-1": mateMeta("mate", "d")})
	assert.Equal(t, []View{{Name: "mate", Description: "d", Idle: true}}, tr.Visible())
}

func TestTracker_TeammateIdleBeforeMetaMarksTeammate(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("amate-1", "mate"), stopEv("amate-1"), idleEv("mate")}, nil)
	// One teammate running: the tracked teammate matches the count.
	tr.Apply([]Event{parentStop(runningMate())}, nil)
	tr.Apply(nil, map[string]Meta{"amate-1": mateMeta("mate", "d")})
	assert.Equal(t, []View{{Name: "mate", Description: "d", Idle: true}}, tr.Visible())
}

func TestTracker_ParentStopRepairsOutOfOrderEvents(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{stopEv("a1"), startEv("a1", "Explore")}, map[string]Meta{"a1": plainMeta("Explore", "d")})
	require.Len(t, tr.Visible(), 1, "stop processed first, so the agent looks stuck")

	tr.Apply([]Event{parentStop()}, nil)
	assert.Empty(t, tr.Visible())
}

func TestTracker_ParentStopKeepsListedSubagents(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{"a1": plainMeta("Explore", "d"), "a2": plainMeta("Plan", "e")}
	tr.Apply([]Event{startEv("a1", "Explore"), startEv("a2", "Plan")}, meta)

	tr.Apply([]Event{parentStop(runningSub("a1"), Task{ID: "a2", Type: "subagent", Status: "completed"})}, nil)
	assert.Equal(t, []View{{Name: "Explore", Description: "d"}}, tr.Visible(),
		"only running entries count as live")
}

func TestTracker_UnusableTaskListDoesNotReconcile(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore")}, map[string]Meta{"a1": plainMeta("Explore", "d")})
	tr.Apply([]Event{{Name: EventStop}}, nil) // HasTasks false
	assert.Len(t, tr.Visible(), 1)
}

func TestTracker_SubagentStopListIsNotUsedForReconciliation(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{"a1": plainMeta("Explore", "d"), "a2": plainMeta("Plan", "e")}
	tr.Apply([]Event{startEv("a1", "Explore"), startEv("a2", "Plan")}, meta)

	// A parallel foreground agent (a2) may be missing from this list.
	stop := stopEv("a1")
	stop.Tasks, stop.HasTasks = []Task{}, true
	tr.Apply([]Event{stop}, nil)
	assert.Equal(t, []View{{Name: "Plan", Description: "e"}}, tr.Visible())
}

func TestTracker_SessionEndClears(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore"), sessionEnd()}, map[string]Meta{"a1": plainMeta("Explore", "d")})
	assert.Empty(t, tr.Visible())
}

func TestTracker_VisibleOrder(t *testing.T) {
	tr := NewTracker()
	meta := map[string]Meta{
		"a1":      plainMeta("Explore", "first"),
		"amate-1": mateMeta("mate", "second"),
		"a3":      plainMeta("Plan", "third"),
	}
	tr.Apply([]Event{
		startEv("a1", "Explore"),
		startEv("amate-1", "mate"), stopEv("amate-1"), idleEv("mate"),
		startEv("a3", "Plan"),
	}, meta)
	assert.Equal(t, []View{
		{Name: "Explore", Description: "first"},
		{Name: "Plan", Description: "third"},
		{Name: "mate", Description: "second", Idle: true},
	}, tr.Visible(), "working first, then idle, each in spawn order")
}

func TestTracker_Reset(t *testing.T) {
	tr := NewTracker()
	tr.Apply([]Event{startEv("a1", "Explore")}, map[string]Meta{"a1": plainMeta("Explore", "d")})
	tr.Reset()
	assert.Empty(t, tr.Visible())
	assert.Empty(t, tr.MissingMeta())
}
