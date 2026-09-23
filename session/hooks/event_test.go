package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func TestParseEvent_SubagentStart(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "subagent_start.json"))
	require.NoError(t, err)
	assert.Equal(t, EventSubagentStart, ev.Name)
	assert.Equal(t, "a91a01359700cebc7", ev.AgentID)
	assert.Equal(t, "Explore", ev.AgentType)
	assert.Equal(t, "/home/u/.claude/projects/-work/1a8d294c-6dc6-4fa6-962c-24493cbe5924.jsonl", ev.TranscriptPath)
	assert.False(t, ev.HasTasks)
}

func TestParseEvent_SubagentStopKeepsTasks(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "subagent_stop.json"))
	require.NoError(t, err)
	assert.Equal(t, EventSubagentStop, ev.Name)
	require.True(t, ev.HasTasks)
	assert.Equal(t, []Task{
		{ID: "a91a01359700cebc7", Type: "subagent", Status: "running"},
		{ID: "ad653c9bf6d1720c1", Type: "subagent", Status: "running"},
	}, ev.Tasks)
}

func TestParseEvent_TeammateIdle(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "teammate_idle.json"))
	require.NoError(t, err)
	assert.Equal(t, EventTeammateIdle, ev.Name)
	assert.Equal(t, "probe-mate", ev.TeammateName)
}

func TestParseEvent_StopTeammateTask(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "stop_teammate_running.json"))
	require.NoError(t, err)
	require.True(t, ev.HasTasks)
	assert.Equal(t, []Task{{ID: "tkva0drgj", Type: "teammate", Status: "running"}}, ev.Tasks)
}

func TestParseEvent_SessionEnd(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "session_end.json"))
	require.NoError(t, err)
	assert.Equal(t, EventSessionEnd, ev.Name)
}

// An empty list is an answer ("nothing is running"); an absent, null or
// malformed one is not, and must never be read as empty.
func TestParseEvent_TaskListPresence(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		hasTasks bool
	}{
		{"empty list", `{"hook_event_name":"Stop","background_tasks":[]}`, true},
		{"absent", `{"hook_event_name":"Stop"}`, false},
		{"null", `{"hook_event_name":"Stop","background_tasks":null}`, false},
		{"object", `{"hook_event_name":"Stop","background_tasks":{"x":1}}`, false},
		{"wrong element type", `{"hook_event_name":"Stop","background_tasks":[{"id":7}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := ParseEvent([]byte(tc.payload))
			require.NoError(t, err)
			assert.Equal(t, tc.hasTasks, ev.HasTasks)
			if !tc.hasTasks {
				assert.Nil(t, ev.Tasks)
			}
		})
	}
}

func TestParseEvent_UnknownEvent(t *testing.T) {
	_, err := ParseEvent([]byte(`{"hook_event_name":"PreToolUse"}`))
	assert.True(t, errors.Is(err, ErrUnknownEvent))

	_, err = ParseEvent([]byte(`{}`))
	assert.True(t, errors.Is(err, ErrUnknownEvent))
}

func TestParseEvent_NotJSON(t *testing.T) {
	_, err := ParseEvent([]byte("not json"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrUnknownEvent))
}

func TestEventCompact_RoundTrips(t *testing.T) {
	for _, name := range []string{
		"subagent_start.json", "subagent_stop.json", "teammate_idle.json",
		"stop_teammate_running.json", "session_end.json",
	} {
		t.Run(name, func(t *testing.T) {
			raw := fixture(t, name)
			ev, err := ParseEvent(raw)
			require.NoError(t, err)

			compact, err := ev.Compact()
			require.NoError(t, err)
			again, err := ParseEvent(compact)
			require.NoError(t, err)

			assert.Equal(t, ev, again)
			assert.Less(t, len(compact), len(raw))
			if ev.Name != EventStop {
				assert.NotContains(t, string(compact), "last_assistant_message")
			}
			assert.NotContains(t, string(compact), "description")
			assert.NotContains(t, string(compact), "session_id")
		})
	}
}

func TestEventCompact_MissingTasksStayMissing(t *testing.T) {
	ev, err := ParseEvent([]byte(`{"hook_event_name":"Stop","background_tasks":{"x":1}}`))
	require.NoError(t, err)
	compact, err := ev.Compact()
	require.NoError(t, err)
	assert.NotContains(t, string(compact), "background_tasks")

	empty := Event{Name: EventStop, HasTasks: true}
	compact, err = empty.Compact()
	require.NoError(t, err)
	again, err := ParseEvent(compact)
	require.NoError(t, err)
	assert.True(t, again.HasTasks, "an empty list must survive as an empty list")
}

// probeFixture reads one payload from the 2026-09-23 probe by its
// <unix-nanos> file-name prefix.
func probeFixture(t *testing.T, stem string) []byte {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("testdata", "probe-2.1.280", stem+"-*.json"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "fixture %s", stem)
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	return data
}

func TestParseEvent_ProbeSessionStart(t *testing.T) {
	ev, err := ParseEvent(probeFixture(t, "1790179911178279565"))
	require.NoError(t, err)
	assert.Equal(t, EventSessionStart, ev.Name)
	assert.Equal(t, "8c634184-0fe5-4b62-b437-8f364eeeefcc", ev.SessionID)
	assert.Equal(t, "startup", ev.Source)
	assert.Equal(t, "/home/user/.claude/projects/-probe-work/8c634184-0fe5-4b62-b437-8f364eeeefcc.jsonl", ev.TranscriptPath)
	assert.Empty(t, ev.AgentID)

	cleared, err := ParseEvent(probeFixture(t, "1790180077137029376"))
	require.NoError(t, err)
	assert.Equal(t, "clear", cleared.Source)
	assert.Equal(t, "487f460e-49ff-4961-a883-6b2c290c9aec", cleared.SessionID)
}

func TestParseEvent_ProbePermissionRequest(t *testing.T) {
	parent, err := ParseEvent(probeFixture(t, "1790179972989150631"))
	require.NoError(t, err)
	assert.Equal(t, EventPermissionRequest, parent.Name)
	assert.Equal(t, "Bash", parent.ToolName)
	assert.Empty(t, parent.AgentID)

	sub, err := ParseEvent(probeFixture(t, "1790180017571376478"))
	require.NoError(t, err)
	assert.Equal(t, "Bash", sub.ToolName)
	assert.Equal(t, "a4775447930305717", sub.AgentID)
}

func TestParseEvent_ProbeNotification(t *testing.T) {
	ev, err := ParseEvent(probeFixture(t, "1790179978985750130"))
	require.NoError(t, err)
	assert.Equal(t, EventNotification, ev.Name)
	assert.Equal(t, "permission_prompt", ev.NotificationType)
	assert.Equal(t, "Claude needs your permission", ev.Message)
}

func TestParseEvent_ProbeStops(t *testing.T) {
	done, err := ParseEvent(probeFixture(t, "1790179927419880486"))
	require.NoError(t, err)
	assert.Equal(t, "PONG", done.LastAssistantMessage)
	require.True(t, done.HasTasks)
	assert.Empty(t, done.Tasks)

	mid, err := ParseEvent(probeFixture(t, "1790180017490508216"))
	require.NoError(t, err)
	assert.Equal(t, "Agent launched. Waiting for completion...", mid.LastAssistantMessage)
	assert.Equal(t, []Task{{ID: "a4775447930305717", Type: "subagent", Status: "running"}}, mid.Tasks)
}

// Each event keeps only the fields loom reads from it: a SubagentStop's
// last_assistant_message is the subagent's reply, not the parent's.
func TestParseEvent_KeepsFieldsOnlyForTheirEvent(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "subagent_stop.json"))
	require.NoError(t, err)
	assert.Empty(t, ev.LastAssistantMessage)
	assert.Empty(t, ev.SessionID)

	ev, err = ParseEvent([]byte(`{"hook_event_name":"Stop","session_id":"s","source":"x","tool_name":"Bash","message":"m","notification_type":"n"}`))
	require.NoError(t, err)
	assert.Equal(t, Event{Name: EventStop}, ev)
}

func TestParseEvent_CapsMessagesKeepingTheEnd(t *testing.T) {
	long := strings.Repeat("é", MaxMessageBytes) + "Should I push?"
	ev, err := ParseEvent([]byte(`{"hook_event_name":"Stop","last_assistant_message":"` + long + `"}`))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(ev.LastAssistantMessage), MaxMessageBytes)
	assert.True(t, utf8.ValidString(ev.LastAssistantMessage), "the cap never splits a character")
	assert.True(t, strings.HasSuffix(ev.LastAssistantMessage, "Should I push?"),
		"cards show a message's last lines, where Claude puts its summary or question")
}

func TestEventCompact_ProbeFixturesRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "probe-2.1.280", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 34)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		ev, err := ParseEvent(raw)
		require.NoError(t, err, f)
		compact, err := ev.Compact()
		require.NoError(t, err)
		again, err := ParseEvent(compact)
		require.NoError(t, err)
		assert.Equal(t, ev, again, f)
	}
}
