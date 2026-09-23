package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
			assert.NotContains(t, string(compact), "last_assistant_message")
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
