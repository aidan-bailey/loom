# Subagent Nesting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each Claude session's live subagents and agent-team teammates on its loom cards: a count on the rail, and rows in place of the overview tail.

**Architecture:** Loom launches Claude with `--settings <file>`, which registers five hooks. Each hook writes its JSON payload to a per-instance folder. A throttled command on the health tick turns those files into events, and a new pure package (`session/subagent`) folds the events into a state machine owned by `session.Instance`. Cards render a snapshot of that state. A compact copy of every event is kept, so a restarted loom can replay the log and rebuild its state.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, testify, Claude Code hooks (verified against 2.1.270).

**Spec:** `docs/superpowers/specs/2026-09-16-subagent-nesting-design.md` (read it first; this plan implements it and doesn't repeat its reasoning).

## Global Constraints

- Build and test with `CGO_ENABLED=0` (no `gcc` on this host). The race detector needs `CGO_ENABLED=1 CC=clang`.
- Format with `gofmt -w`. Lint with `CGO_ENABLED=0 go vet ./...` (the local golangci-lint is v2, while the repo config is for v1).
- Module path: `github.com/aidan-bailey/loom`.
- Hooks folder: `{ConfigDir}/hooks/<tmux.ToLoomTmuxName(title)>/`, containing `settings.json`, `launch-id` and `events/`. Mode 0700 for folders, 0600 for files.
- Hook events, in this order: `SubagentStart`, `SubagentStop`, `TeammateIdle`, `Stop`, `SessionEnd`.
- Scan limits: at most 500 new `.json` files per scan, 1 MiB per event file, `.tmp` files are stale after 1 minute, and scans run every 3s.
- Config key: `ClaudeSubagentTracking *bool` with `json:"claude_subagent_tracking,omitempty"`; nil means enabled.
- Never add hooks on Windows, for non-Claude programs, when the program already passes `--settings`, or when the folder path contains `'`.
- Never serialize tracker state into `InstanceData` (no `SchemaVersion` bump).
- `tea.Cmd` bodies do filesystem work only and never mutate the model (CLAUDE.md, "No model mutation from `tea.Cmd` goroutines").
- UI styles use theme role variables at render time, never literal colours (CLAUDE.md, "Theme-derived styles must be hook-built").
- When unsure, show nothing: an uncertain agent is hidden, never shown in a guessed state.
- Stay on branch `aidanb/integrating-claude-agents`. Use conventional commit messages, and end every commit message with:
  ```
  Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
  ```

## File Map

| File | Responsibility |
|---|---|
| `session/subagent/event.go` (new) | Hook event type, parsing, compact form |
| `session/subagent/meta.go` (new) | Metadata sidecar type, parsing, path derivation |
| `session/subagent/tracker.go` (new) | State machine: events → live agents |
| `session/subagent/hooks.go` (new) | Folder layout, hook command, `settings.json`, `Prepare` |
| `session/subagent/scan.go` (new) | Reading, converting and ordering event files; reading metadata |
| `session/subagent/testdata/*.json` (new) | Probe payloads (paths sanitized) |
| `session/subagent/realclaude_test.go` (new) | Opt-in test against real Claude |
| `session/agent/{adapter,claude,aider,gemini,default}.go` | `ApplySettingsFlag`, `HasSettingsFlag` |
| `config/config.go` | `ClaudeSubagentTracking`, `SubagentTrackingEnabled()` |
| `ui/overlay/claudePreferences.go` | "Track Subagents" row |
| `session/subagent_hooks.go` (new) | Toggle, folder paths, launch composition, Instance tracker API, sweep |
| `session/instance.go` | Tracker fields, launch call sites, cleanup in `Kill` |
| `app/subagents.go` (new) | Scan command, throttle, result handling |
| `app/app.go`, `app/state_settings.go` | Wiring: fields, tick, message case, toggle sync, sweep |
| `ui/card_agents.go` (new) | `SubagentRow`, rail count text, overview agent rows |
| `ui/card.go`, `ui/overview.go` | CardData field, rail second-line priority, overview tail |
| `CLAUDE.md`, `USAGE.md` | Docs |

---

### Task 1: Event and metadata parsing

**Files:**
- Create: `session/subagent/event.go`, `session/subagent/meta.go`
- Create: `session/subagent/testdata/subagent_start.json`, `subagent_stop.json`, `teammate_idle.json`, `stop_teammate_running.json`, `session_end.json`, `meta_plain.json`, `meta_teammate.json`
- Test: `session/subagent/event_test.go`, `session/subagent/meta_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const EventSubagentStart, EventSubagentStop, EventTeammateIdle, EventStop, EventSessionEnd string`
  - `var HookEvents []string`
  - `var ErrUnknownEvent error`
  - `type Task struct{ ID, Type, Status string }`
  - `type Event struct{ Name, AgentID, AgentType, TeammateName, TranscriptPath string; Tasks []Task; HasTasks bool }`
  - `func ParseEvent(data []byte) (Event, error)`
  - `func (e Event) Compact() ([]byte, error)`
  - `type Meta struct{ AgentType, Name, Description, TaskKind string }`, `func (m Meta) IsTeammate() bool`, `func (m Meta) DisplayName() string`, `func ParseMeta(data []byte) (Meta, error)`
  - `type MetaRef struct{ AgentID, Path string }`, `func MetaPath(transcriptPath, agentID string) string`

- [ ] **Step 1: Add the fixtures**

`session/subagent/testdata/subagent_start.json`:
```json
{"session_id":"1a8d294c-6dc6-4fa6-962c-24493cbe5924","transcript_path":"/home/u/.claude/projects/-work/1a8d294c-6dc6-4fa6-962c-24493cbe5924.jsonl","cwd":"/work","prompt_id":"a4e5973b-3e4b-4fef-9ddb-0f33abf91da2","agent_id":"a91a01359700cebc7","agent_type":"Explore","hook_event_name":"SubagentStart"}
```

`session/subagent/testdata/subagent_stop.json`:
```json
{"session_id":"1a8d294c-6dc6-4fa6-962c-24493cbe5924","transcript_path":"/home/u/.claude/projects/-work/1a8d294c-6dc6-4fa6-962c-24493cbe5924.jsonl","cwd":"/work","prompt_id":"a4e5973b-3e4b-4fef-9ddb-0f33abf91da2","permission_mode":"default","agent_id":"a91a01359700cebc7","agent_type":"Explore","hook_event_name":"SubagentStop","stop_hook_active":false,"agent_transcript_path":"/home/u/.claude/projects/-work/1a8d294c-6dc6-4fa6-962c-24493cbe5924/subagents/agent-a91a01359700cebc7.jsonl","last_assistant_message":"pong","background_tasks":[{"id":"a91a01359700cebc7","type":"subagent","status":"running","description":"probe sync","agent_type":"Explore"},{"id":"ad653c9bf6d1720c1","type":"subagent","status":"running","description":"probe teammate","agent_type":"general-purpose"}],"session_crons":[]}
```

`session/subagent/testdata/teammate_idle.json`:
```json
{"session_id":"07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11","transcript_path":"/home/u/.claude/projects/-work/07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11.jsonl","cwd":"/work","hook_event_name":"TeammateIdle","teammate_name":"probe-mate","team_name":"session-07e9a557"}
```

`session/subagent/testdata/stop_teammate_running.json`:
```json
{"session_id":"07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11","transcript_path":"/home/u/.claude/projects/-work/07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11.jsonl","cwd":"/work","prompt_id":"23e4f57c-67dd-42d9-aebc-362aaadeae1a","permission_mode":"default","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"DONE","background_tasks":[{"id":"tkva0drgj","type":"teammate","status":"running","description":"Reply with the word pong. Use no tools."}],"session_crons":[]}
```

`session/subagent/testdata/session_end.json`:
```json
{"session_id":"07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11","transcript_path":"/home/u/.claude/projects/-work/07e9a557-5a0e-4c1e-9d55-2f4f3b1c0a11.jsonl","cwd":"/work","hook_event_name":"SessionEnd"}
```

`session/subagent/testdata/meta_plain.json`:
```json
{"agentType":"Explore","description":"probe sync","toolUseId":"toolu_016bio4Y5MRkRoW5c7ughkGU","spawnDepth":1,"requestShape":"background","requestNonInteractive":true}
```

`session/subagent/testdata/meta_teammate.json`:
```json
{"agentType":"probe-mate","description":"probe teammate","name":"probe-mate","spawnDepth":0,"requestShape":"background","requestNonInteractive":true,"model":"haiku","taskKind":"in_process_teammate","teamName":"session-07e9a557","color":"blue","planModeRequired":false,"permissionMode":"default"}
```

- [ ] **Step 2: Write the failing event tests**

`session/subagent/event_test.go`:
```go
package subagent

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
```

- [ ] **Step 3: Write the failing metadata tests**

`session/subagent/meta_test.go`:
```go
package subagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMeta_Plain(t *testing.T) {
	m, err := ParseMeta(fixture(t, "meta_plain.json"))
	require.NoError(t, err)
	assert.Equal(t, "Explore", m.AgentType)
	assert.Equal(t, "probe sync", m.Description)
	assert.False(t, m.IsTeammate())
	assert.Equal(t, "Explore", m.DisplayName(), "no name falls back to the agent type")
}

func TestParseMeta_Teammate(t *testing.T) {
	m, err := ParseMeta(fixture(t, "meta_teammate.json"))
	require.NoError(t, err)
	assert.True(t, m.IsTeammate())
	assert.Equal(t, "probe-mate", m.DisplayName())
	assert.Equal(t, "probe teammate", m.Description)
}

func TestParseMeta_Invalid(t *testing.T) {
	_, err := ParseMeta([]byte("{"))
	assert.Error(t, err)
}

func TestMetaPath(t *testing.T) {
	assert.Equal(t,
		"/p/-work/sess/subagents/agent-a1.meta.json",
		MetaPath("/p/-work/sess.jsonl", "a1"))
	assert.Equal(t, "", MetaPath("", "a1"))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", ""))
	// agent IDs come from a payload; never let one escape the folder.
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", "../x"))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", `a\b`))
	assert.Equal(t, "", MetaPath("/p/sess.jsonl", ".."))
}
```

- [ ] **Step 4: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/subagent/`
Expected: build failure, `undefined: ParseEvent` (and the other new names).

- [ ] **Step 5: Implement `event.go`**

`session/subagent/event.go`:
```go
// Package subagent tracks the subagents and agent-team teammates a Claude
// session has spawned, from the Claude Code hook events loom registers at
// launch. See docs/superpowers/specs/2026-09-16-subagent-nesting-design.md.
//
// It has no dependency on tmux, the UI or the app, so every piece can be
// tested against payloads captured from a real Claude session.
package subagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Hook event names loom registers.
const (
	EventSubagentStart = "SubagentStart"
	EventSubagentStop  = "SubagentStop"
	EventTeammateIdle  = "TeammateIdle"
	EventStop          = "Stop"
	EventSessionEnd    = "SessionEnd"
)

// HookEvents lists the registered events in the order settings.json
// declares them.
var HookEvents = []string{
	EventSubagentStart, EventSubagentStop, EventTeammateIdle, EventStop, EventSessionEnd,
}

// ErrUnknownEvent is returned by ParseEvent for a JSON object whose
// hook_event_name is not one loom registers.
var ErrUnknownEvent = errors.New("subagent: unknown hook event")

// Task is one entry of a payload's background_tasks list, reduced to the
// fields reconciliation reads.
type Task struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// Event is one hook event. The same shape covers the raw payload Claude
// writes and the compact form the scan keeps (see Compact).
type Event struct {
	Name           string
	AgentID        string
	AgentType      string
	TeammateName   string
	TranscriptPath string
	// Tasks is the background_tasks list. HasTasks is false when the field
	// was absent, null or malformed. Reconciliation must then do nothing:
	// reading an unusable list as empty would remove every agent.
	Tasks    []Task
	HasTasks bool
}

// wireEvent uses the hook payload's own field names, so ParseEvent reads
// both forms. background_tasks stays raw so a malformed list downgrades to
// "missing" instead of failing the whole event.
type wireEvent struct {
	Name           string          `json:"hook_event_name"`
	AgentID        string          `json:"agent_id,omitempty"`
	AgentType      string          `json:"agent_type,omitempty"`
	TeammateName   string          `json:"teammate_name,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	Tasks          json.RawMessage `json:"background_tasks,omitempty"`
}

// ParseEvent decodes a raw hook payload or a compact event. It returns an
// error wrapping ErrUnknownEvent for events loom does not register, and a
// different error for input that is not a JSON object.
func ParseEvent(data []byte) (Event, error) {
	var w wireEvent
	if err := json.Unmarshal(data, &w); err != nil {
		return Event{}, fmt.Errorf("subagent: parse event: %w", err)
	}
	if !slices.Contains(HookEvents, w.Name) {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownEvent, w.Name)
	}
	ev := Event{
		Name:           w.Name,
		AgentID:        w.AgentID,
		AgentType:      w.AgentType,
		TeammateName:   w.TeammateName,
		TranscriptPath: w.TranscriptPath,
	}
	if len(w.Tasks) > 0 && string(w.Tasks) != "null" {
		var tasks []Task
		if err := json.Unmarshal(w.Tasks, &tasks); err == nil {
			if tasks == nil {
				tasks = []Task{}
			}
			ev.Tasks, ev.HasTasks = tasks, true
		}
	}
	return ev, nil
}

// Compact returns the event as the scan keeps it on disk: the payload's
// field names, only the fields loom reads, and tasks reduced to
// id/type/status. ParseEvent reads it back unchanged. A missing task list
// stays missing and an empty one stays empty.
func (e Event) Compact() ([]byte, error) {
	w := wireEvent{
		Name:           e.Name,
		AgentID:        e.AgentID,
		AgentType:      e.AgentType,
		TeammateName:   e.TeammateName,
		TranscriptPath: e.TranscriptPath,
	}
	if e.HasTasks {
		tasks := e.Tasks
		if tasks == nil {
			tasks = []Task{}
		}
		raw, err := json.Marshal(tasks)
		if err != nil {
			return nil, fmt.Errorf("subagent: compact tasks: %w", err)
		}
		w.Tasks = raw
	}
	return json.Marshal(w)
}
```

Note: `TestEventCompact_RoundTrips` compares whole `Event` values, so `ParseEvent` must return `[]Task{}` rather than `nil` for an empty list. The `tasks == nil` branch above handles that.

- [ ] **Step 6: Implement `meta.go`**

`session/subagent/meta.go`:
```go
package subagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// taskKindTeammate marks an agent-team teammate in a metadata sidecar.
const taskKindTeammate = "in_process_teammate"

// Meta is the part of Claude's agent-<id>.meta.json sidecar loom reads.
// Several key sets exist across CLI versions; every field is optional.
type Meta struct {
	AgentType   string `json:"agentType"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TaskKind    string `json:"taskKind"`
}

// IsTeammate reports whether the sidecar describes an agent-team teammate,
// which goes idle after a turn rather than ending.
func (m Meta) IsTeammate() bool { return m.TaskKind == taskKindTeammate }

// DisplayName is the label shown for the agent: its name when it has one,
// else its agent type.
func (m Meta) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.AgentType
}

// ParseMeta decodes a metadata sidecar.
func ParseMeta(data []byte) (Meta, error) {
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("subagent: parse meta: %w", err)
	}
	return m, nil
}

// MetaRef locates one agent's sidecar.
type MetaRef struct {
	AgentID string
	Path    string
}

// MetaPath returns where Claude writes agentID's sidecar for the session
// whose transcript is at transcriptPath. It returns "" when either is
// empty, or when agentID could escape the subagents folder.
func MetaPath(transcriptPath, agentID string) string {
	if transcriptPath == "" || agentID == "" || agentID == ".." ||
		strings.ContainsAny(agentID, `/\`) {
		return ""
	}
	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	return filepath.Join(sessionDir, "subagents", "agent-"+agentID+".meta.json")
}
```

- [ ] **Step 7: Run the tests and confirm they pass**

Run: `gofmt -w session/subagent && CGO_ENABLED=0 go test ./session/subagent/ -v`
Expected: all tests `PASS`.

- [ ] **Step 8: Commit**

```bash
git add session/subagent
git commit -F - <<'EOF'
feat(subagent): parse Claude hook events and agent metadata

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 2: Tracker state machine

**Files:**
- Create: `session/subagent/tracker.go`
- Test: `session/subagent/tracker_test.go`

**Interfaces:**
- Consumes (Task 1): `Event`, `Task`, the `Event*` constants, `Meta`, `MetaRef`, `MetaPath`.
- Produces:
  - `type View struct{ Name, Description string; Idle bool }`
  - `type Tracker`, `func NewTracker() *Tracker`
  - `func (t *Tracker) Apply(events []Event, meta map[string]Meta)`
  - `func (t *Tracker) Visible() []View` (never nil)
  - `func (t *Tracker) MissingMeta() []MetaRef` (sorted by agent ID)
  - `func (t *Tracker) Reset()`

- [ ] **Step 1: Write the failing tests**

`session/subagent/tracker_test.go`:
```go
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
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/subagent/ -run TestTracker`
Expected: build failure, `undefined: NewTracker`.

- [ ] **Step 3: Implement `tracker.go`**

`session/subagent/tracker.go`:
```go
package subagent

import (
	"cmp"
	"slices"
)

// kind distinguishes plain subagents, which end when they stop, from
// agent-team teammates, which go idle and can be re-tasked. kindUnknown
// covers the window before an agent's metadata sidecar is read.
type kind int

const (
	kindUnknown kind = iota
	kindPlain
	kindTeammate
)

// state is an agent's position in its lifecycle.
type state int

const (
	stateWorking state = iota
	stateIdle
	// stateStopping: a SubagentStop with no TeammateIdle after it yet.
	// Teammate shutdown produces exactly this, so reconciliation removes
	// Stopping teammates first. An agent of unknown kind also waits here
	// until its metadata says whether it has ended. Shown as idle.
	stateStopping
)

// statusRunning is the background_tasks status of a live task.
const statusRunning = "running"

// View is one agent as the UI renders it.
type View struct {
	Name        string
	Description string
	Idle        bool
}

type agent struct {
	id          string
	name        string // Meta.DisplayName once known, else the event's agent_type
	description string
	kind        kind
	state       state
	metaPath    string
	hasMeta     bool
	spawnSeq    int
	stoppedSeq  int
}

// Tracker derives the live agent set from hook events. It is not safe for
// concurrent use; session.Instance guards it with its own mutex.
type Tracker struct {
	agents map[string]*agent
	seq    int
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{agents: map[string]*agent{}} }

// Reset forgets every agent. Called at a new launch and before a replay
// rebuilds state from the full event log.
func (t *Tracker) Reset() { t.agents = map[string]*agent{} }

// Apply folds events, in order, into the tracker. meta holds sidecars read
// since the last call, keyed by agent ID. They are applied to known agents
// first, so an agent that started and stopped within one batch already
// has its kind when its SubagentStop is processed.
func (t *Tracker) Apply(events []Event, meta map[string]Meta) {
	for id, m := range meta {
		if a, ok := t.agents[id]; ok {
			t.applyMeta(a, m)
		}
	}
	for _, ev := range events {
		t.seq++
		switch ev.Name {
		case EventSubagentStart:
			t.start(ev, meta)
		case EventSubagentStop:
			t.stop(ev.AgentID)
		case EventTeammateIdle:
			t.idle(ev.TeammateName)
		case EventStop:
			// Only the parent's Stop reconciles: no foreground subagent can
			// be running when the parent's turn ends, while a SubagentStop
			// list may omit a parallel foreground agent.
			if ev.HasTasks {
				t.reconcile(ev.Tasks)
			}
		case EventSessionEnd:
			t.Reset()
		}
	}
}

// applyMeta records a sidecar. A plain agent whose SubagentStop arrived
// before its sidecar has ended, so it is removed here.
func (t *Tracker) applyMeta(a *agent, m Meta) {
	a.hasMeta = true
	if name := m.DisplayName(); name != "" {
		a.name = name
	}
	a.description = m.Description
	if m.IsTeammate() {
		a.kind = kindTeammate
	} else {
		a.kind = kindPlain
	}
	if a.kind == kindPlain && a.state == stateStopping {
		delete(t.agents, a.id)
	}
}

// start creates or re-tasks an agent. Rows are only ever created here, so
// Claude's internal helpers, which stop without starting, never appear.
func (t *Tracker) start(ev Event, meta map[string]Meta) {
	if ev.AgentID == "" {
		return
	}
	a, ok := t.agents[ev.AgentID]
	if !ok {
		a = &agent{
			id:       ev.AgentID,
			name:     ev.AgentType,
			metaPath: MetaPath(ev.TranscriptPath, ev.AgentID),
			spawnSeq: t.seq,
		}
		t.agents[ev.AgentID] = a
		if m, found := meta[ev.AgentID]; found {
			t.applyMeta(a, m)
		}
	}
	a.state = stateWorking
}

func (t *Tracker) stop(id string) {
	a, ok := t.agents[id]
	if !ok {
		return
	}
	if a.kind == kindPlain {
		delete(t.agents, id)
		return
	}
	a.state = stateStopping
	a.stoppedSeq = t.seq
}

// idle marks the named teammate idle. A TeammateIdle proves the agent is a
// teammate even before its sidecar is read.
func (t *Tracker) idle(name string) {
	if name == "" {
		return
	}
	for _, a := range t.agents {
		if a.name == name && a.kind != kindPlain {
			a.kind = kindTeammate
			a.state = stateIdle
		}
	}
}

// reconcile applies a parent Stop's list of running tasks. Plain subagents
// are matched by ID. Teammates are listed under task IDs that cannot be
// matched, so only their count is used: extras are removed, Stopping ones
// first, then the most recently stopped. Agents of unknown kind are left
// alone; they are hidden and only exist until their sidecar is read.
func (t *Tracker) reconcile(tasks []Task) {
	liveSubagents := map[string]bool{}
	liveTeammates := 0
	for _, task := range tasks {
		if task.Status != statusRunning {
			continue
		}
		switch task.Type {
		case "subagent":
			liveSubagents[task.ID] = true
		case "teammate":
			liveTeammates++
		}
	}

	var teammates []*agent
	for id, a := range t.agents {
		switch a.kind {
		case kindPlain:
			if !liveSubagents[id] {
				delete(t.agents, id)
			}
		case kindTeammate:
			teammates = append(teammates, a)
		}
	}

	if excess := len(teammates) - liveTeammates; excess > 0 {
		slices.SortFunc(teammates, func(x, y *agent) int {
			xs, ys := x.state == stateStopping, y.state == stateStopping
			if xs != ys {
				if xs {
					return -1
				}
				return 1
			}
			if c := cmp.Compare(y.stoppedSeq, x.stoppedSeq); c != 0 {
				return c
			}
			return cmp.Compare(x.spawnSeq, y.spawnSeq)
		})
		for _, a := range teammates[:excess] {
			delete(t.agents, a.id)
		}
	}

	for _, a := range t.agents {
		if a.kind == kindTeammate && a.state == stateStopping {
			a.state = stateIdle
		}
	}
}

// Visible returns the agents to render: those with a sidecar, working
// first, then idle, each group in spawn order. Never nil.
func (t *Tracker) Visible() []View {
	shown := make([]*agent, 0, len(t.agents))
	for _, a := range t.agents {
		if a.hasMeta {
			shown = append(shown, a)
		}
	}
	slices.SortFunc(shown, func(x, y *agent) int {
		xw, yw := x.state == stateWorking, y.state == stateWorking
		if xw != yw {
			if xw {
				return -1
			}
			return 1
		}
		return cmp.Compare(x.spawnSeq, y.spawnSeq)
	})
	out := make([]View, len(shown))
	for i, a := range shown {
		out[i] = View{Name: a.name, Description: a.description, Idle: a.state != stateWorking}
	}
	return out
}

// MissingMeta lists agents still waiting for their sidecar, sorted by ID,
// so the next scan can retry them.
func (t *Tracker) MissingMeta() []MetaRef {
	var refs []MetaRef
	for _, a := range t.agents {
		if !a.hasMeta && a.metaPath != "" {
			refs = append(refs, MetaRef{AgentID: a.id, Path: a.metaPath})
		}
	}
	slices.SortFunc(refs, func(x, y MetaRef) int { return cmp.Compare(x.AgentID, y.AgentID) })
	return refs
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w session/subagent && CGO_ENABLED=0 go test ./session/subagent/ -v`
Expected: all tests `PASS`.

- [ ] **Step 5: Commit**

```bash
git add session/subagent
git commit -F - <<'EOF'
feat(subagent): track live subagents and teammates from hook events

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 3: Hooks folder, hook command and `settings.json`

**Files:**
- Create: `session/subagent/hooks.go`
- Test: `session/subagent/hooks_test.go`

**Interfaces:**
- Consumes (Task 1): `HookEvents`.
- Produces:
  - `func SettingsPath(dir string) string`
  - `func EventsDir(dir string) string`
  - `func SafePath(p string) bool`
  - `func HookCommand(eventsDir string) string`
  - `func SettingsJSON(eventsDir string) ([]byte, error)`
  - `func Prepare(dir string) (launchID string, err error)`
  - unexported `launchIDPath(dir string) string` (used by Task 4)

- [ ] **Step 1: Write the failing tests**

`session/subagent/hooks_test.go`:
```go
package subagent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsJSON_Golden(t *testing.T) {
	got, err := SettingsJSON("/cfg/hooks/loom_x/events")
	require.NoError(t, err)

	cmd := `f='/cfg/hooks/loom_x/events/'\"$(date +%s%N)-$$\"; { cat > \"$f.tmp\" && mv \"$f.tmp\" \"$f.json\"; } 2>/dev/null || cat >/dev/null`
	entry := func(name string) string {
		return `    "` + name + `": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "` + cmd + `"
          }
        ]
      }
    ]`
	}
	want := "{\n  \"hooks\": {\n" +
		strings.Join([]string{
			entry("SessionEnd"), entry("Stop"), entry("SubagentStart"),
			entry("SubagentStop"), entry("TeammateIdle"),
		}, ",\n") +
		"\n  }\n}\n"
	assert.Equal(t, want, string(got))
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook commands need sh")
	}
}

func runHook(t *testing.T, eventsDir string, stdin []byte) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", HookCommand(eventsDir))
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Run()
}

func TestHookCommand_WritesPayloadAtomically(t *testing.T) {
	requireShell(t)
	dir := filepath.Join(t.TempDir(), "with space", "events")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	require.NoError(t, runHook(t, dir, []byte(`{"hook_event_name":"Stop"}`)))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), ".json"))
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	assert.Equal(t, `{"hook_event_name":"Stop"}`, string(data))
}

// When the folder is gone the hook must still exit 0 and read all of its
// input, or Claude would report a hook error or fail writing the payload.
func TestHookCommand_MissingFolderExitsZeroAndDrains(t *testing.T) {
	requireShell(t)
	missing := filepath.Join(t.TempDir(), "gone", "events")

	r, w, err := os.Pipe()
	require.NoError(t, err)
	cmd := exec.Command("sh", "-c", HookCommand(missing))
	cmd.Stdin = r
	require.NoError(t, cmd.Start())
	require.NoError(t, r.Close())

	big := bytes.Repeat([]byte("x"), 1<<20) // larger than a pipe buffer
	_, writeErr := w.Write(big)
	require.NoError(t, w.Close())

	assert.NoError(t, cmd.Wait())
	assert.NoError(t, writeErr, "the hook must consume its whole input")
}

func TestSafePath(t *testing.T) {
	assert.True(t, SafePath("/home/u/.loom/hooks/loom_a b"))
	assert.False(t, SafePath("/home/u/it's/hooks"))
}

func TestPrepare_CreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")

	id, err := Prepare(dir)
	require.NoError(t, err)
	assert.Len(t, id, 16)

	settings, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir))
	require.NoError(t, err)
	assert.Equal(t, string(want), string(settings))

	stored, err := os.ReadFile(launchIDPath(dir))
	require.NoError(t, err)
	assert.Equal(t, id, string(stored))

	info, err := os.Stat(EventsDir(dir))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
}

func TestPrepare_ClearsPreviousLaunch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	first, err := Prepare(dir)
	require.NoError(t, err)
	old := filepath.Join(EventsDir(dir), "1-1.ev")
	require.NoError(t, os.WriteFile(old, []byte("{}"), 0o600))

	second, err := Prepare(dir)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.NoFileExists(t, old)
}

func TestPrepare_RejectsSingleQuote(t *testing.T) {
	_, err := Prepare(filepath.Join(t.TempDir(), "it's"))
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/subagent/ -run 'TestSettingsJSON|TestHookCommand|TestSafePath|TestPrepare'`
Expected: build failure, `undefined: SettingsJSON`.

- [ ] **Step 3: Implement `hooks.go`**

`session/subagent/hooks.go`:
```go
package subagent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layout of an instance's hooks folder.
const (
	settingsFile = "settings.json"
	launchIDFile = "launch-id"
	eventsSubdir = "events"
)

// SettingsPath is the file passed to claude --settings.
func SettingsPath(dir string) string { return filepath.Join(dir, settingsFile) }

// EventsDir is where hooks write one file per event.
func EventsDir(dir string) string { return filepath.Join(dir, eventsSubdir) }

func launchIDPath(dir string) string { return filepath.Join(dir, launchIDFile) }

// SafePath reports whether p can be embedded in the single-quoted hook
// command and the single-quoted --settings flag.
func SafePath(p string) bool { return !strings.ContainsRune(p, '\'') }

// HookCommand returns the shell command every hook runs. It writes the
// payload Claude pipes on stdin to a new file in eventsDir, first as .tmp
// and then renamed, so a reader never sees a partial file. It always exits
// 0 and always reads its whole input, including when eventsDir is gone or
// the disk is full, so Claude never reports a hook error or fails writing
// the payload. The name is <unix-nanos>-<pid>; macOS date prints a literal
// N for %N, so there the pid alone keeps names unique and the scan orders
// by modification time. eventsDir must satisfy SafePath.
func HookCommand(eventsDir string) string {
	return `f='` + eventsDir + `/'"$(date +%s%N)-$$"; ` +
		`{ cat > "$f.tmp" && mv "$f.tmp" "$f.json"; } 2>/dev/null || cat >/dev/null`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}

type settingsDoc struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// SettingsJSON returns the settings file registering HookCommand for every
// event in HookEvents. Claude adds these hooks to the user's own.
func SettingsJSON(eventsDir string) ([]byte, error) {
	cmd := HookCommand(eventsDir)
	doc := settingsDoc{Hooks: make(map[string][]hookMatcher, len(HookEvents))}
	for _, name := range HookEvents {
		doc.Hooks[name] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: cmd}}}}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep the command's > readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("subagent: encode settings: %w", err)
	}
	return buf.Bytes(), nil
}

// Prepare empties dir and writes a fresh settings.json, events folder and
// launch-id for a new launch, returning the launch ID. Events from an
// earlier launch describe agents that no longer exist, so nothing is kept.
// launch-id is written last, so a concurrent scan never sees a new ID
// without its settings.
func Prepare(dir string) (string, error) {
	if !SafePath(dir) {
		return "", fmt.Errorf("subagent: hooks folder %q contains a single quote", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("subagent: clear hooks folder: %w", err)
	}
	if err := os.MkdirAll(EventsDir(dir), 0o700); err != nil {
		return "", fmt.Errorf("subagent: create hooks folder: %w", err)
	}
	settings, err := SettingsJSON(EventsDir(dir))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(SettingsPath(dir), settings, 0o600); err != nil {
		return "", fmt.Errorf("subagent: write settings: %w", err)
	}
	id, err := newLaunchID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(launchIDPath(dir), []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("subagent: write launch-id: %w", err)
	}
	return id, nil
}

func newLaunchID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("subagent: launch id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w session/subagent && CGO_ENABLED=0 go test ./session/subagent/ -v`
Expected: all tests `PASS`. If `TestSettingsJSON_Golden` fails, compare against the actual output. Map keys are sorted by `encoding/json`, which is why the expected order is alphabetical. Fix the implementation, not the expected string, unless the difference is only whitespace produced by `SetIndent`.

- [ ] **Step 5: Commit**

```bash
git add session/subagent
git commit -F - <<'EOF'
feat(subagent): write per-launch hook settings and event folder

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 4: Scanning the event folder

**Files:**
- Create: `session/subagent/scan.go`
- Test: `session/subagent/scan_test.go`

**Interfaces:**
- Consumes: `ParseEvent`, `Event.Compact`, `ParseMeta`, `MetaPath`, `MetaRef`, `Meta` (Task 1); `Prepare`, `EventsDir`, `launchIDPath` (Task 3).
- Produces:
  - `type Request struct{ Dir string; Cold bool; MissingMeta []MetaRef }`
  - `type Result struct{ LaunchID string; Events []Event; Meta map[string]Meta; Replayed bool }`
  - `var ErrNoHooks error`
  - `func Scan(req Request, now time.Time) (Result, error)`

- [ ] **Step 1: Write the failing tests**

`session/subagent/scan_test.go`:
```go
package subagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var base = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func prepared(t *testing.T) (dir, launchID string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "hooks", "loom_x")
	id, err := Prepare(dir)
	require.NoError(t, err)
	return dir, id
}

func writeRaw(t *testing.T, dir, stem, payload string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(EventsDir(dir), stem+".json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	require.NoError(t, os.Chtimes(path, mod, mod))
	return path
}

func startPayload(id string) string {
	return fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":%q,"agent_type":"Explore","transcript_path":"/p/s.jsonl"}`, id)
}

func ids(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.AgentID
	}
	return out
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func TestScan_NoFolder(t *testing.T) {
	_, err := Scan(Request{Dir: filepath.Join(t.TempDir(), "missing")}, base)
	assert.True(t, errors.Is(err, ErrNoHooks))
}

func TestScan_ConvertsNewEventsInOrder(t *testing.T) {
	dir, id := prepared(t)
	writeRaw(t, dir, "c", startPayload("third"), base.Add(2*time.Second))
	writeRaw(t, dir, "a", startPayload("first"), base)
	writeRaw(t, dir, "b", startPayload("second"), base.Add(time.Second))

	res, err := Scan(Request{Dir: dir}, base.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, id, res.LaunchID)
	assert.False(t, res.Replayed)
	assert.Equal(t, []string{"first", "second", "third"}, ids(res.Events))

	entries, err := os.ReadDir(EventsDir(dir))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.ev", "b.ev", "c.ev"}, names(entries))
	info, err := os.Stat(filepath.Join(EventsDir(dir), "b.ev"))
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(base.Add(time.Second)), "compact file keeps the event's time")
}

func TestScan_TiesBreakByStem(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "2-1", startPayload("later"), base)
	writeRaw(t, dir, "1-9", startPayload("earlier"), base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"earlier", "later"}, ids(res.Events))
}

func TestScan_WarmIgnoresLogAndColdReplaysIt(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("one"), base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)

	warm, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, warm.Events)

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	assert.True(t, cold.Replayed)
	assert.Equal(t, []string{"one"}, ids(cold.Events))
}

func TestScan_ColdAppliesNewEventsOnce(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("old"), base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	writeRaw(t, dir, "b", startPayload("new"), base.Add(time.Second))

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"old", "new"}, ids(cold.Events),
		"the .ev written for b in this scan must not be read again")
}

func TestScan_CapsNewEvents(t *testing.T) {
	dir, _ := prepared(t)
	for i := 0; i < maxNewPerScan+1; i++ {
		writeRaw(t, dir, fmt.Sprintf("%04d", i), startPayload(fmt.Sprintf("a%d", i)), base.Add(time.Duration(i)*time.Millisecond))
	}

	first, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Len(t, first.Events, maxNewPerScan)

	second, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{fmt.Sprintf("a%d", maxNewPerScan)}, ids(second.Events))
}

func TestScan_DiscardsBadFiles(t *testing.T) {
	dir, _ := prepared(t)
	huge := `{"hook_event_name":"Stop","pad":"` + strings.Repeat("x", maxEventBytes) + `"}`
	writeRaw(t, dir, "huge", huge, base)
	writeRaw(t, dir, "junk", "not json", base)
	writeRaw(t, dir, "other", `{"hook_event_name":"PreToolUse"}`, base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, res.Events)
	entries, err := os.ReadDir(EventsDir(dir))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestScan_StaleTmp(t *testing.T) {
	dir, _ := prepared(t)
	stale := filepath.Join(EventsDir(dir), "old.tmp")
	fresh := filepath.Join(EventsDir(dir), "new.tmp")
	for path, mod := range map[string]time.Time{stale: base.Add(-2 * time.Minute), fresh: base.Add(-10 * time.Second)} {
		require.NoError(t, os.WriteFile(path, []byte(`{"hook_event_name":"Stop"}`), 0o600))
		require.NoError(t, os.Chtimes(path, mod, mod))
	}

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, res.Events)
	assert.NoFileExists(t, stale)
	assert.FileExists(t, fresh)
}

func TestScan_ReadsMetaForStartsAndMissing(t *testing.T) {
	dir, _ := prepared(t)
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "sess.jsonl")
	subagents := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(subagents, 0o700))
	writeMeta := func(id, desc string) {
		require.NoError(t, os.WriteFile(filepath.Join(subagents, "agent-"+id+".meta.json"),
			[]byte(fmt.Sprintf(`{"agentType":"Explore","description":%q}`, desc)), 0o600))
	}
	writeMeta("a1", "from start")
	writeMeta("a2", "from retry")

	writeRaw(t, dir, "e", fmt.Sprintf(
		`{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore","transcript_path":%q}`,
		transcriptPath), base)

	res, err := Scan(Request{Dir: dir, MissingMeta: []MetaRef{
		{AgentID: "a2", Path: filepath.Join(subagents, "agent-a2.meta.json")},
		{AgentID: "a3", Path: filepath.Join(subagents, "agent-a3.meta.json")},
	}}, base)
	require.NoError(t, err)
	assert.Equal(t, map[string]Meta{
		"a1": {AgentType: "Explore", Description: "from start"},
		"a2": {AgentType: "Explore", Description: "from retry"},
	}, res.Meta)
}

func TestScan_MalformedTasksReplayAsMissing(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "s", `{"hook_event_name":"Stop","background_tasks":{"x":1}}`, base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	require.Len(t, cold.Events, 1)
	assert.False(t, cold.Events[0].HasTasks)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/subagent/ -run TestScan`
Expected: build failure, `undefined: Scan`.

- [ ] **Step 3: Implement `scan.go`**

`session/subagent/scan.go`:
```go
package subagent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// Scan limits.
const (
	maxNewPerScan = 500
	maxEventBytes = 1 << 20
	staleTmpAge   = time.Minute
)

// ErrNoHooks means the folder or its launch-id is missing: the session was
// launched without tracking.
var ErrNoHooks = errors.New("subagent: no hooks folder")

// Request describes one instance's scan.
type Request struct {
	// Dir is the instance's hooks folder.
	Dir string
	// Cold replays the retained .ev log. True for the first scan after a
	// launch or a loom restart.
	Cold bool
	// MissingMeta lists agents still waiting for their sidecar.
	MissingMeta []MetaRef
}

// Result is what one scan found.
type Result struct {
	LaunchID string
	Events   []Event
	Meta     map[string]Meta
	// Replayed is true when Events is the full history (Request.Cold).
	Replayed bool
}

type eventFile struct {
	path string
	stem string
	mod  time.Time
	size int64
}

type stampedEvent struct {
	eventFile
	ev Event
}

func byTimeThenStem(a, b eventFile) int {
	if c := a.mod.Compare(b.mod); c != 0 {
		return c
	}
	return strings.Compare(a.stem, b.stem)
}

// Scan collects hook events for one instance. New .json files (at most
// maxNewPerScan, oldest first) are parsed and replaced by compact .ev
// files that keep their original modification time. A cold scan also
// replays every .ev file that existed before this scan. Events are
// returned ordered by modification time, then file stem, together with
// the metadata sidecars for new SubagentStart events and req.MissingMeta.
// Scan only touches the filesystem, so it is safe to run from a tea.Cmd.
func Scan(req Request, now time.Time) (Result, error) {
	idBytes, err := os.ReadFile(launchIDPath(req.Dir))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNoHooks
	}
	if err != nil {
		return Result{}, fmt.Errorf("subagent: read launch-id: %w", err)
	}
	launchID := strings.TrimSpace(string(idBytes))
	if launchID == "" {
		return Result{}, ErrNoHooks
	}

	dir := EventsDir(req.Dir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNoHooks
	}
	if err != nil {
		return Result{}, fmt.Errorf("subagent: list events: %w", err)
	}

	var fresh, kept []eventFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since ReadDir
		}
		name := e.Name()
		f := eventFile{path: filepath.Join(dir, name), mod: info.ModTime(), size: info.Size()}
		switch {
		case strings.HasSuffix(name, ".tmp"):
			if now.Sub(f.mod) > staleTmpAge {
				_ = os.Remove(f.path)
			}
		case strings.HasSuffix(name, ".json"):
			f.stem = strings.TrimSuffix(name, ".json")
			fresh = append(fresh, f)
		case strings.HasSuffix(name, ".ev") && req.Cold:
			f.stem = strings.TrimSuffix(name, ".ev")
			kept = append(kept, f)
		}
	}
	slices.SortFunc(fresh, byTimeThenStem)
	if len(fresh) > maxNewPerScan {
		fresh = fresh[:maxNewPerScan]
	}

	var stamped []stampedEvent
	for _, f := range kept {
		if ev, ok := readKept(f); ok {
			stamped = append(stamped, stampedEvent{f, ev})
		}
	}
	for _, f := range fresh {
		if ev, ok := convert(f); ok {
			stamped = append(stamped, stampedEvent{f, ev})
		}
	}
	slices.SortFunc(stamped, func(a, b stampedEvent) int { return byTimeThenStem(a.eventFile, b.eventFile) })

	events := make([]Event, len(stamped))
	for i, s := range stamped {
		events[i] = s.ev
	}
	return Result{
		LaunchID: launchID,
		Events:   events,
		Meta:     readMeta(events, req.MissingMeta),
		Replayed: req.Cold,
	}, nil
}

func discard(path, reason string) {
	log.DebugKV("subagent.scan.discarded", "file", path, "reason", reason)
	_ = os.Remove(path)
}

func readKept(f eventFile) (Event, bool) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return Event{}, false
	}
	ev, err := ParseEvent(data)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	return ev, true
}

// convert parses one new event file and replaces it with its compact .ev
// form. The .json never survives a scan. If the .ev cannot be written the
// event is still returned; it just won't be replayed after a restart.
func convert(f eventFile) (Event, bool) {
	if f.size > maxEventBytes {
		discard(f.path, "too large")
		return Event{}, false
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	ev, err := ParseEvent(data)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	if err := writeCompact(f, ev); err != nil {
		log.DebugKV("subagent.scan.not_retained", "file", f.path, "err", err.Error())
	}
	_ = os.Remove(f.path)
	return ev, true
}

func writeCompact(f eventFile, ev Event) error {
	data, err := ev.Compact()
	if err != nil {
		return err
	}
	dst := filepath.Join(filepath.Dir(f.path), f.stem+".ev")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, f.mod, f.mod); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readMeta reads the sidecars for SubagentStart events and for agents
// still missing one. An unreadable sidecar is skipped; the tracker keeps
// the agent hidden and it is retried on the next scan.
func readMeta(events []Event, missing []MetaRef) map[string]Meta {
	refs := slices.Clone(missing)
	for _, ev := range events {
		if ev.Name != EventSubagentStart {
			continue
		}
		if p := MetaPath(ev.TranscriptPath, ev.AgentID); p != "" {
			refs = append(refs, MetaRef{AgentID: ev.AgentID, Path: p})
		}
	}
	meta := map[string]Meta{}
	for _, r := range refs {
		if _, done := meta[r.AgentID]; done {
			continue
		}
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		m, err := ParseMeta(data)
		if err != nil {
			continue
		}
		meta[r.AgentID] = m
	}
	return meta
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w session/subagent && CGO_ENABLED=0 go test ./session/subagent/ -v`
Expected: all tests `PASS`.

- [ ] **Step 5: Commit**

```bash
git add session/subagent
git commit -F - <<'EOF'
feat(subagent): scan hook events into a replayable compact log

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 5: Config key and Claude Preferences row

**Files:**
- Modify: `config/config.go` (field after `ClaudeLoomContext` near line 112; accessor after `LoomContextEnabled` near line 267)
- Modify: `ui/overlay/claudePreferences.go` (row count, key handling, render)
- Test: `config/config_test.go`, `ui/overlay/claudePreferences_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Config.ClaudeSubagentTracking *bool`, `func (c *Config) SubagentTrackingEnabled() bool`.

- [ ] **Step 1: Write the failing tests**

Append to `config/config_test.go`:
```go
func TestSubagentTrackingEnabled(t *testing.T) {
	// nil => enabled (mirrors LoomContextEnabled)
	c := &Config{}
	assert.True(t, c.SubagentTrackingEnabled())

	tru := true
	c.ClaudeSubagentTracking = &tru
	assert.True(t, c.SubagentTrackingEnabled())

	fls := false
	c.ClaudeSubagentTracking = &fls
	assert.False(t, c.SubagentTrackingEnabled())
}
```

In `ui/overlay/claudePreferences_test.go`, replace the second half of `TestClaudePreferencesRowNavigationClamps` (from the comment `// Down eight times stays at row 7` to the end of the function) with:
```go
	// Down nine times stays at row 8 (only nine rows): toggles Track
	// Subagents, not any earlier row.
	for i := 0; i < 9; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.SubagentTrackingEnabled())
	assert.True(t, cfg.LoomContextEnabled(), "row 7 must be untouched")
}
```

In `TestClaudePreferences_Context1MRow`, the `row count` subtest pins the old count. Change `assert.Equal(t, 8, claudePrefsRowCount)` to:
```go
		assert.Equal(t, 9, claudePrefsRowCount)
```

Append:
```go
func TestClaudePreferences_SubagentTrackingToggle(t *testing.T) {
	cfg := &config.Config{}
	cp := NewClaudePreferences(cfg, false, "")
	assert.Contains(t, cp.Render(), "Track Subagents")

	// Row 8 is Track Subagents.
	for i := 0; i < 8; i++ {
		cp.HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, changed := cp.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, changed)
	assert.False(t, cfg.SubagentTrackingEnabled())

	_, changed = cp.HandleKeyPress(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.True(t, changed)
	assert.True(t, cfg.SubagentTrackingEnabled())
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./config/ ./ui/overlay/`
Expected: build failure, `c.SubagentTrackingEnabled undefined`.

- [ ] **Step 3: Implement the config key**

In `config/config.go`, directly after the `ClaudeLoomContext` field:
```go
	// ClaudeSubagentTracking controls whether new Claude sessions launch
	// with hooks that report subagent and teammate state, shown as a count
	// on rail cards and as rows on overview cards (see session/subagent).
	// nil is treated as enabled (read via SubagentTrackingEnabled),
	// matching ClaudeLoomContext. Takes effect at the next launch or
	// resume.
	ClaudeSubagentTracking *bool `json:"claude_subagent_tracking,omitempty"`
```

Directly after `LoomContextEnabled`:
```go
// SubagentTrackingEnabled reports whether new Claude sessions should launch
// with loom's subagent hooks. nil (unset) is treated as enabled, mirroring
// LoomContextEnabled. Read only from the main goroutine.
func (c *Config) SubagentTrackingEnabled() bool {
	return c.ClaudeSubagentTracking == nil || *c.ClaudeSubagentTracking
}
```

- [ ] **Step 4: Implement the preferences row**

In `ui/overlay/claudePreferences.go`:

1. In the `ClaudePreferences` doc comment, change `today it holds seven rows: Remote Control, Permission Mode, Model, Headroom Proxy, Effort, Cache TTL (1h), and Loom Context.` to `today it holds nine rows: Remote Control, Permission Mode, Model, 1M Context, Headroom Proxy, Effort, Cache TTL (1h), Loom Context, and Track Subagents.`
2. Replace the row-count block with:
```go
// claudePrefsRowCount is the number of navigable rows: Remote Control,
// Permission Mode, Model, 1M Context, Headroom Proxy, Effort, Cache TTL
// (1h), Loom Context, and Track Subagents — nine rows.
const claudePrefsRowCount = 9
```
3. In `HandleKeyPress`, after `case 7:` and its body, add:
```go
		case 8:
			c.cfg.Mutate(func(cc *config.Config) {
				v := !cc.SubagentTrackingEnabled()
				cc.ClaudeSubagentTracking = &v
			})
```
4. In `Render`, after the `loomRow` block, add:
```go
	subCheck := "[ ]"
	if c.cfg.SubagentTrackingEnabled() {
		subCheck = "[x]"
	}
	subCursor := "  "
	if c.cursor == 8 {
		subCursor = "> "
	}
	subRow := subCursor + "Track Subagents   " + subCheck
	if c.cursor == 8 {
		subRow = claudePrefsSelectedStyle.Render(subRow)
	} else {
		subRow = claudePrefsRowStyle.Render(subRow)
	}
```
5. In the `content :=` concatenation, change `loomRow + "\n\n" +` to `loomRow + "\n" + subRow + "\n\n" +`.

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `gofmt -w config ui/overlay && CGO_ENABLED=0 go test ./config/ ./ui/overlay/`
Expected: `ok` for both packages.

- [ ] **Step 6: Commit**

```bash
git add config ui/overlay
git commit -F - <<'EOF'
feat(config): add Track Subagents preference

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 6: `--settings` flag in the agent adapters

**Files:**
- Modify: `session/agent/adapter.go` (interface method; `HasSettingsFlag` helper after `insertAfterCommand`)
- Modify: `session/agent/claude.go` (after `ApplyLoomContextFlag`), `aider.go`, `gemini.go`, `default.go`
- Test: `session/agent/adapter_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Adapter.ApplySettingsFlag(program, path string) string`, `func HasSettingsFlag(program string) bool`.

- [ ] **Step 1: Write the failing tests**

Append to `session/agent/adapter_test.go`:
```go
func TestApplySettingsFlag(t *testing.T) {
	reg := DefaultRegistry()
	path := "/home/u/.loom/hooks/loom_a/settings.json"
	claude := reg.Lookup("claude")

	assert.Equal(t, "claude --settings '"+path+"'", claude.ApplySettingsFlag("claude", path))
	assert.Equal(t, "claude --settings '"+path+"' --model opus",
		claude.ApplySettingsFlag("claude --model opus", path))

	// idempotent
	once := claude.ApplySettingsFlag("claude", path)
	assert.Equal(t, once, claude.ApplySettingsFlag(once, path))

	// a user's own --settings wins
	assert.Equal(t, "claude --settings /mine.json", claude.ApplySettingsFlag("claude --settings /mine.json", path))
	assert.Equal(t, "claude --settings=/mine.json", claude.ApplySettingsFlag("claude --settings=/mine.json", path))

	// no-ops
	assert.Equal(t, "claude", claude.ApplySettingsFlag("claude", ""))
	assert.Equal(t, "", claude.ApplySettingsFlag("", path))
	assert.Equal(t, "aider", reg.Lookup("aider").ApplySettingsFlag("aider", path))
	assert.Equal(t, "gemini", reg.Lookup("gemini").ApplySettingsFlag("gemini", path))
	assert.Equal(t, "codex --x", Default().ApplySettingsFlag("codex --x", path))
}

func TestHasSettingsFlag(t *testing.T) {
	assert.True(t, HasSettingsFlag("claude --settings x.json"))
	assert.True(t, HasSettingsFlag("claude --model opus --settings=x.json"))
	assert.False(t, HasSettingsFlag("claude --setting-sources user"))
	assert.False(t, HasSettingsFlag("--settings"), "the command token itself is not a flag")
	assert.False(t, HasSettingsFlag(""))
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/agent/`
Expected: build failure, `ApplySettingsFlag undefined`.

- [ ] **Step 3: Implement**

In `session/agent/adapter.go`, add to the `Adapter` interface after `ApplyLoomContextFlag`:
```go
	// ApplySettingsFlag returns the program string with "--settings
	// '<path>'" inserted, pointing the agent at an extra settings file
	// (loom uses it to register subagent-tracking hooks). path == "" is a
	// no-op. Returns the input unchanged when any --settings flag is
	// already present, loom's or the user's, and for agents without a
	// settings-file concept.
	ApplySettingsFlag(program, path string) string
```

After `insertAfterCommand`:
```go
// HasSettingsFlag reports whether program passes --settings after its
// command token.
func HasSettingsFlag(program string) bool {
	parts := strings.Fields(program)
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts[1:] {
		if p == "--settings" || strings.HasPrefix(p, "--settings=") {
			return true
		}
	}
	return false
}
```

In `session/agent/claude.go`, after `ApplyLoomContextFlag`:
```go
// ApplySettingsFlag inserts "--settings '<path>'" after "claude". The path
// is single-quoted for the same reason as ApplyLoomContextFlag. An existing
// --settings flag wins: whether Claude merges two is unverified, so loom
// adds nothing rather than risk displacing the user's file.
func (claudeAdapter) ApplySettingsFlag(program, path string) string {
	if path == "" || len(strings.Fields(program)) == 0 || HasSettingsFlag(program) {
		return program
	}
	return insertAfterCommand(program, "--settings '"+path+"'")
}
```

In `session/agent/aider.go`, after its `ApplyLoomContextFlag`:
```go
// ApplySettingsFlag is a no-op for aider — it has no settings-file flag.
func (aiderAdapter) ApplySettingsFlag(program, _ string) string { return program }
```

In `session/agent/gemini.go`, after its `ApplyLoomContextFlag`:
```go
// ApplySettingsFlag is a no-op for gemini — it has no settings-file flag.
func (geminiAdapter) ApplySettingsFlag(program, _ string) string { return program }
```

In `session/agent/default.go`, after its `ApplyLoomContextFlag`:
```go
// ApplySettingsFlag implements Adapter. The fallback adapter never
// modifies the program.
func (defaultAdapter) ApplySettingsFlag(program, _ string) string { return program }
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w session/agent && CGO_ENABLED=0 go test ./session/agent/ && CGO_ENABLED=0 go build ./...`
Expected: `ok`, and the build succeeds (every `Adapter` implementation compiles).

- [ ] **Step 5: Commit**

```bash
git add session/agent
git commit -F - <<'EOF'
feat(agent): add --settings flag injection for Claude

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 7: Instance integration

**Files:**
- Create: `session/subagent_hooks.go`
- Modify: `session/instance.go`: fields after `waitReason`; import `session/subagent`; Start's tmux creation (line ~640); remove `applyLoomContext` (line ~1295); `startFreshWithRecovery` (line ~1309); `CrashRestart` (line ~1327); `Kill` (after the tmux cleanup block, line ~756)
- Test: `session/subagent_hooks_test.go`

**Interfaces:**
- Consumes: `subagent.Prepare`, `SettingsPath`, `SafePath`, `NewTracker`, `Tracker`, `View`, `Request`, `Result`, `Scan`, `EventsDir` (Tasks 1–4); `agent.HasSettingsFlag`, `ApplySettingsFlag` (Task 6).
- Produces:
  - `func SetSubagentTrackingEnabled(enabled bool)`
  - `func SubagentHooksDir(configDir, title string) string`
  - `func BuildSettingsCommand(program, path string) string`
  - `func (i *Instance) SubagentScanRequest() (subagent.Request, bool)`
  - `func (i *Instance) ApplySubagentScan(res subagent.Result) bool`
  - `func (i *Instance) Subagents() []subagent.View`
  - `func SweepSubagentHooks(configDir string, claimedTitles map[string]bool, cmdExec internalexec.Executor)`
  - unexported `launchProgram(program string, launching bool) string`, `recoveryLaunch() (program, launch string)`, `removeSubagentHooks()`

- [ ] **Step 1: Write the failing tests**

`session/subagent_hooks_test.go`:
```go
package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTracking(t *testing.T, enabled bool) {
	t.Helper()
	prev := subagentTrackingEnabled.Load()
	SetSubagentTrackingEnabled(enabled)
	t.Cleanup(func() { SetSubagentTrackingEnabled(prev) })
}

func hooksInstance(t *testing.T, program string) *Instance {
	t.Helper()
	return &Instance{Title: "hooks test", Program: program, ConfigDir: t.TempDir(), Status: Running}
}

func settingsFlag(inst *Instance) string {
	return "--settings '" + subagent.SettingsPath(SubagentHooksDir(inst.ConfigDir, inst.Title)) + "'"
}

func TestSubagentHooksDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/cfg", "hooks", "loom_hookstest"), SubagentHooksDir("/cfg", "hooks test"))
}

func TestLaunchProgram_AddsHooksWhenLaunching(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram("claude", true)

	assert.Contains(t, got, settingsFlag(inst))
	assert.Equal(t, "claude", inst.Program, "Program is never rewritten")
	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	stored, err := os.ReadFile(filepath.Join(dir, "launch-id"))
	require.NoError(t, err)
	assert.Equal(t, string(stored), inst.hookLaunchID)
	assert.False(t, inst.subagentWarm)
}

func TestLaunchProgram_ReattachLeavesFolderAlone(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	launchID := inst.hookLaunchID
	kept := filepath.Join(subagent.EventsDir(SubagentHooksDir(inst.ConfigDir, inst.Title)), "1-1.ev")
	require.NoError(t, os.WriteFile(kept, []byte("{}"), 0o600))

	got := inst.launchProgram("claude", false)

	assert.NotContains(t, got, "--settings")
	assert.FileExists(t, kept)
	assert.Equal(t, launchID, inst.hookLaunchID)
}

func TestLaunchProgram_SkipsHooks(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		program string
		noDir   bool
	}{
		{"disabled", false, "claude", false},
		{"non-claude", true, "aider", false},
		{"user settings", true, "claude --settings /mine.json", false},
		{"no config dir", true, "claude", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTracking(t, tc.enabled)
			inst := hooksInstance(t, tc.program)
			if tc.noDir {
				inst.ConfigDir = ""
			}

			got := inst.launchProgram(tc.program, true)

			assert.Equal(t, strings.Count(tc.program, "--settings"), strings.Count(got, "--settings"))
			if inst.ConfigDir != "" {
				assert.NoDirExists(t, filepath.Join(inst.ConfigDir, "hooks"))
			}
			assert.Empty(t, inst.hookLaunchID)
		})
	}
}

func TestRecoveryLaunch_AddsHooks(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")

	program, launch := inst.recoveryLaunch()

	assert.Equal(t, "claude --continue", program)
	assert.NotContains(t, program, "--settings", "InstanceEnv sees the bare recovery program")
	assert.Contains(t, launch, settingsFlag(inst))
	assert.Contains(t, launch, "--continue")
}

// writeHookEvent drops a raw payload the way the hook command does.
func writeHookEvent(t *testing.T, inst *Instance, stem, payload string, mod time.Time) {
	t.Helper()
	path := filepath.Join(subagent.EventsDir(SubagentHooksDir(inst.ConfigDir, inst.Title)), stem+".json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	require.NoError(t, os.Chtimes(path, mod, mod))
}

// fakeTranscript creates a transcript path with one agent's sidecar.
func fakeTranscript(t *testing.T, agentID, meta string) string {
	t.Helper()
	root := t.TempDir()
	sub := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-"+agentID+".meta.json"), []byte(meta), 0o600))
	return filepath.Join(root, "sess.jsonl")
}

func scanAndApply(t *testing.T, inst *Instance) bool {
	t.Helper()
	req, ok := inst.SubagentScanRequest()
	require.True(t, ok)
	res, err := subagent.Scan(req, time.Now())
	require.NoError(t, err)
	return inst.ApplySubagentScan(res)
}

func TestSubagents_EndToEndAndRestart(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	transcript := fakeTranscript(t, "amate-1", `{"agentType":"mate","name":"mate","description":"implement","taskKind":"in_process_teammate"}`)
	t0 := time.Now().Add(-time.Minute)
	writeHookEvent(t, inst, "1", fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"amate-1","agent_type":"mate","transcript_path":%q}`, transcript), t0)
	writeHookEvent(t, inst, "2", `{"hook_event_name":"SubagentStop","agent_id":"amate-1"}`, t0.Add(time.Second))
	writeHookEvent(t, inst, "3", `{"hook_event_name":"TeammateIdle","teammate_name":"mate"}`, t0.Add(2*time.Second))

	require.True(t, scanAndApply(t, inst))
	want := []subagent.View{{Name: "mate", Description: "implement", Idle: true}}
	assert.Equal(t, want, inst.Subagents())

	// A new loom process restores the same instance with empty memory.
	restored := &Instance{Title: inst.Title, Program: "claude", ConfigDir: inst.ConfigDir, Status: Running}
	req, ok := restored.SubagentScanRequest()
	require.True(t, ok)
	assert.True(t, req.Cold)
	require.True(t, scanAndApply(t, restored))
	assert.Equal(t, inst.hookLaunchID, restored.hookLaunchID, "restored instance adopts the folder's launch ID")
	assert.Equal(t, want, restored.Subagents())

	req, _ = restored.SubagentScanRequest()
	assert.False(t, req.Cold)
}

func TestApplySubagentScan_Gates(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	inst.launchProgram("claude", true)
	start := subagent.Event{Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl"}
	meta := map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "d"}}

	assert.False(t, inst.ApplySubagentScan(subagent.Result{}), "empty launch ID")
	assert.False(t, inst.ApplySubagentScan(subagent.Result{LaunchID: "other", Replayed: true,
		Events: []subagent.Event{start}, Meta: meta}), "another launch")
	assert.False(t, inst.ApplySubagentScan(subagent.Result{LaunchID: inst.hookLaunchID,
		Events: []subagent.Event{start}, Meta: meta}), "incremental result for a cold tracker")
	assert.Empty(t, inst.Subagents())

	assert.True(t, inst.ApplySubagentScan(subagent.Result{LaunchID: inst.hookLaunchID, Replayed: true,
		Events: []subagent.Event{start}, Meta: meta}))
	assert.Len(t, inst.Subagents(), 1)
}

func TestSubagents_HiddenWhenNotLive(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplySubagentScan(subagent.Result{LaunchID: "L", Replayed: true,
		Events: []subagent.Event{{Name: subagent.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	for _, st := range []Status{Paused, Recoverable, Deleting} {
		inst.Status = st
		assert.Nil(t, inst.Subagents(), st.String())
	}
	inst.Status = Prompting
	assert.Len(t, inst.Subagents(), 1)
}

func TestSubagentScanRequest(t *testing.T) {
	aider := hooksInstance(t, "aider")
	_, ok := aider.SubagentScanRequest()
	assert.False(t, ok)

	paused := hooksInstance(t, "claude")
	paused.Status = Paused
	_, ok = paused.SubagentScanRequest()
	assert.False(t, ok)

	noDir := hooksInstance(t, "claude")
	noDir.ConfigDir = ""
	_, ok = noDir.SubagentScanRequest()
	assert.False(t, ok)

	inst := hooksInstance(t, "claude")
	req, ok := inst.SubagentScanRequest()
	require.True(t, ok)
	assert.Equal(t, SubagentHooksDir(inst.ConfigDir, inst.Title), req.Dir)
	assert.True(t, req.Cold)

	require.True(t, inst.ApplySubagentScan(subagent.Result{LaunchID: "L", Replayed: true,
		Events: []subagent.Event{{Name: subagent.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}}}))
	req, _ = inst.SubagentScanRequest()
	assert.False(t, req.Cold)
	assert.Equal(t, []subagent.MetaRef{{AgentID: "a1", Path: "/p/s/subagents/agent-a1.meta.json"}}, req.MissingMeta)
}

func TestKill_RemovesHooksFolder(t *testing.T) {
	withTracking(t, true)
	inst := newTestStartedInstance(t)
	inst.Program = "claude"
	inst.ConfigDir = t.TempDir()
	inst.launchProgram("claude", true)
	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	require.DirExists(t, dir)

	require.NoError(t, inst.Kill())
	assert.NoDirExists(t, dir)
}

func TestSweepSubagentHooks(t *testing.T) {
	cfg := t.TempDir()
	for _, title := range []string{"claimed", "alive", "dead"} {
		require.NoError(t, os.MkdirAll(SubagentHooksDir(cfg, title), 0o700))
	}
	tmuxLs := func(out string, err error) cmd_test.MockCmdExec {
		return cmd_test.MockCmdExec{OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			require.Contains(t, c.Args, "list-sessions")
			return []byte(out), err
		}}
	}
	claimed := map[string]bool{"claimed": true}

	// tmux unreachable: nothing is removed on a guess.
	SweepSubagentHooks(cfg, claimed, tmuxLs("", errors.New("no server")))
	assert.DirExists(t, SubagentHooksDir(cfg, "dead"))

	SweepSubagentHooks(cfg, claimed, tmuxLs("loom_alive\nloom_term_x\n", nil))
	assert.DirExists(t, SubagentHooksDir(cfg, "claimed"))
	assert.DirExists(t, SubagentHooksDir(cfg, "alive"))
	assert.NoDirExists(t, SubagentHooksDir(cfg, "dead"))
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'Subagent|LaunchProgram|RecoveryLaunch|Kill_RemovesHooks|Sweep'`
Expected: build failure, `undefined: subagentTrackingEnabled` and others.

- [ ] **Step 3: Add the Instance fields**

In `session/instance.go`, add `"github.com/aidan-bailey/loom/session/subagent"` to the imports. After the `waitReason string` field, add:
```go
	// subagents, hookLaunchID and subagentWarm track the agents this
	// session has spawned, from loom's hook events (see
	// subagent_hooks.go). hookLaunchID is the hooks folder generation a
	// scan result must come from; subagentWarm records whether the tracker
	// has applied a result for it yet. Guarded by mu. Ephemeral: never
	// serialized (absent from InstanceData).
	subagents    *subagent.Tracker
	hookLaunchID string
	subagentWarm bool
```

- [ ] **Step 4: Create `session/subagent_hooks.go`**

```go
package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/tmux"
)

// subagentTrackingEnabled mirrors config.SubagentTrackingEnabled(). The
// app sets it at the same points as SetLoomContextEnabled. It only decides
// whether a launch gets hooks; sessions already launched with hooks keep
// being scanned, so their rows never go stale when the setting changes.
var subagentTrackingEnabled atomic.Bool

// SetSubagentTrackingEnabled updates the global subagent-tracking toggle.
func SetSubagentTrackingEnabled(enabled bool) { subagentTrackingEnabled.Store(enabled) }

// hooksRoot holds every instance's hooks folder. It is deliberately outside
// worktrees/: DiscoverOrphans descends into any directory there that lacks
// the _<hex> suffix.
func hooksRoot(configDir string) string { return filepath.Join(configDir, "hooks") }

// SubagentHooksDir returns the hooks folder for the instance titled title.
// It is keyed by tmux session name, so workspace terminals, which have no
// worktree, get one too.
func SubagentHooksDir(configDir, title string) string {
	return filepath.Join(hooksRoot(configDir), tmux.ToLoomTmuxName(title))
}

// BuildSettingsCommand returns program with Claude's --settings flag
// pointing at path. The adapter registry no-ops for non-Claude programs.
func BuildSettingsCommand(program, path string) string {
	return defaultRegistry.Lookup(program).ApplySettingsFlag(program, path)
}

// subagentLive reports whether an instance in status s can have running
// agents.
func subagentLive(s Status) bool {
	return s == Running || s == Ready || s == Prompting || s == Loading
}

// launchProgram composes the command for a new tmux session: loom's
// context flag and, when launching is true, loom's subagent hooks.
// launching is false only for Start(false), which reattaches to a live
// session with Restore. That Claude is still writing to its existing
// hooks folder, and preparing a new one would wipe its history.
func (i *Instance) launchProgram(program string, launching bool) string {
	program = loomContextProgram(program, i.ConfigDir, i.IsWorkspaceTerminal)
	if launching {
		program = i.prepareSubagentHooks(program)
	}
	return program
}

// recoveryLaunch returns the recovery program (what InstanceEnv keys off)
// and the full launch command. startFreshWithRecovery and CrashRestart
// always start a new Claude process.
func (i *Instance) recoveryLaunch() (program, launch string) {
	program = BuildRecoveryCommand(i.Program)
	return program, i.launchProgram(program, true)
}

// prepareSubagentHooks readies a fresh hooks folder and returns program
// with --settings added. On any failure it returns program unchanged, so
// the session still launches, just untracked. It also adopts the new
// launch ID and resets the tracker, so scan results from before this
// launch are dropped.
func (i *Instance) prepareSubagentHooks(program string) string {
	if !subagentTrackingEnabled.Load() || i.ConfigDir == "" ||
		runtime.GOOS == "windows" || !IsClaudeProgram(program) {
		return program
	}
	if agent.HasSettingsFlag(program) {
		i.getLogger().Info("subagent_hooks.skipped", "reason", "program already passes --settings")
		return program
	}
	dir := SubagentHooksDir(i.ConfigDir, i.Title)
	if !subagent.SafePath(dir) {
		i.getLogger().Debug("subagent_hooks.skipped", "reason", "single quote in hooks folder path")
		return program
	}
	launchID, err := subagent.Prepare(dir)
	if err != nil {
		i.getLogger().Warn("subagent_hooks.prepare_failed", "err", err.Error())
		return program
	}
	i.mu.Lock()
	i.hookLaunchID = launchID
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	return BuildSettingsCommand(program, subagent.SettingsPath(dir))
}

// subagentTrackerLocked returns the tracker, creating it on first use.
// Callers hold i.mu for writing.
func (i *Instance) subagentTrackerLocked() *subagent.Tracker {
	if i.subagents == nil {
		i.subagents = subagent.NewTracker()
	}
	return i.subagents
}

// SubagentScanRequest describes the scan this instance needs, or returns
// false when it should not be scanned: a non-Claude agent, no config dir,
// or a status in which no agent can be running. Call it on the Update
// goroutine; the returned request is safe to hand to a tea.Cmd.
func (i *Instance) SubagentScanRequest() (subagent.Request, bool) {
	if i.ConfigDir == "" || !IsClaudeProgram(i.Program) {
		return subagent.Request{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !subagentLive(i.Status) {
		return subagent.Request{}, false
	}
	return subagent.Request{
		Dir:         SubagentHooksDir(i.ConfigDir, i.Title),
		Cold:        !i.subagentWarm,
		MissingMeta: i.subagentTrackerLocked().MissingMeta(),
	}, true
}

// ApplySubagentScan applies one scan result and reports whether it was
// applied. A result for another launch is dropped. An instance restored
// after a loom restart has no launch ID yet and adopts the result's. A
// replayed result rebuilds the tracker from scratch, and an incremental
// one is only applied once the tracker is warm.
func (i *Instance) ApplySubagentScan(res subagent.Result) bool {
	if res.LaunchID == "" {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	switch {
	case i.hookLaunchID == "":
		i.hookLaunchID = res.LaunchID
	case i.hookLaunchID != res.LaunchID:
		return false
	}
	tracker := i.subagentTrackerLocked()
	if res.Replayed {
		tracker.Reset()
	} else if !i.subagentWarm {
		return false
	}
	tracker.Apply(res.Events, res.Meta)
	i.subagentWarm = true
	return true
}

// Subagents returns the live agents to render, or nil when nothing is
// tracked or the instance is in a status where no agent can be running
// (Paused, Recoverable, Deleting).
func (i *Instance) Subagents() []subagent.View {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.subagents == nil || !subagentLive(i.Status) {
		return nil
	}
	return i.subagents.Visible()
}

// removeSubagentHooks deletes this instance's hooks folder. Best effort:
// SweepSubagentHooks removes leftovers on a later load.
func (i *Instance) removeSubagentHooks() {
	if i.ConfigDir == "" {
		return
	}
	if err := os.RemoveAll(SubagentHooksDir(i.ConfigDir, i.Title)); err != nil {
		log.For("session").Debug("subagent_hooks.remove_failed", "title", i.Title, "err", err)
	}
}

// SweepSubagentHooks removes hooks folders under configDir that no
// instance claims and whose tmux session is not running. claimedTitles
// holds the title of every instance in the workspace. If tmux cannot be
// queried, nothing is removed, so a folder in use by a live session, such
// as one owned by another loom process, is never deleted on a guess.
func SweepSubagentHooks(configDir string, claimedTitles map[string]bool, cmdExec internalexec.Executor) {
	if configDir == "" {
		return
	}
	entries, err := os.ReadDir(hooksRoot(configDir))
	if err != nil {
		return
	}
	claimed := make(map[string]bool, len(claimedTitles))
	for title := range claimedTitles {
		claimed[tmux.ToLoomTmuxName(title)] = true
	}
	var unclaimed []string
	for _, e := range entries {
		if e.IsDir() && !claimed[e.Name()] {
			unclaimed = append(unclaimed, e.Name())
		}
	}
	if len(unclaimed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTmuxTimeout)
	defer cancel()
	out, err := cmdExec.Output(exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}"))
	if err != nil {
		return
	}
	alive := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			alive[name] = true
		}
	}
	for _, name := range unclaimed {
		if alive[name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(hooksRoot(configDir), name)); err != nil {
			log.For("session").Debug("subagent_hooks.sweep_failed", "folder", name, "err", err)
		}
	}
}
```

- [ ] **Step 5: Switch the launch call sites**

In `session/instance.go`:

1. In `Start`, replace:
```go
		// Create new tmux session. loomContextProgram wraps the program
		// with --append-system-prompt-file for Claude sessions (no-op when
		// disabled, non-Claude, or the file is missing); InstanceEnv still
		// keys off the bare i.Program.
		launchProgram := i.applyLoomContext(i.Program)
```
with:
```go
		// Create new tmux session. launchProgram adds loom's context flag
		// and, only when this Start actually launches (firstTimeSetup),
		// the subagent hooks. Start(false) reattaches with Restore, and
		// that Claude keeps writing to its existing hooks folder.
		// InstanceEnv still keys off the bare i.Program.
		launchProgram := i.launchProgram(i.Program, firstTimeSetup)
```
2. Delete the `applyLoomContext` doc comment and function.
3. In `startFreshWithRecovery`, replace:
```go
	program := BuildRecoveryCommand(i.Program)
	launchProgram := i.applyLoomContext(program)
```
with:
```go
	program, launchProgram := i.recoveryLaunch()
```
4. Make the same replacement in `CrashRestart`.
5. In `Kill`, after the `if tmuxSess != nil { ... }` block and before the git worktree cleanup, add:
```go
	// After the agent's tmux session is gone, so a SessionEnd hook firing
	// on exit finds no folder and exits harmlessly.
	i.removeSubagentHooks()
```

Run: `grep -rn applyLoomContext --include='*.go' .`
Expected: no output.

- [ ] **Step 6: Run the tests and confirm they pass**

Run: `gofmt -w session && CGO_ENABLED=0 go test ./session/...`
Expected: `ok` for every package. If `TestRecoveryLaunch_AddsHooks` fails because `loomContextProgram` added a flag, check that the loom-context toggle is off in this test process (its default is off because `loomContextEnabled` is only set by the app). Don't weaken the assertion.

- [ ] **Step 7: Commit**

```bash
git add session
git commit -F - <<'EOF'
feat(session): launch Claude with subagent hooks and track agents

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 8: App wiring

**Files:**
- Create: `app/subagents.go`
- Modify: `app/app.go`: fields near `rosterInFlight` (line ~402); `case rosterReadyMsg:` neighbourhood for the new message case (line ~1185); health tick after the roster dispatch (line ~1358); `SetLoomContextEnabled` calls at lines ~455 and ~2707; the end of `reconcileOrphans` (line ~2056)
- Modify: `app/state_settings.go` (line ~46)
- Test: `app/subagents_test.go`

**Interfaces:**
- Consumes: `session.SetSubagentTrackingEnabled`, `SubagentHooksDir`, `SweepSubagentHooks`, `Instance.SubagentScanRequest`, `ApplySubagentScan`, `Subagents` (Task 7); `subagent.Scan`, `Result`, `ErrNoHooks`, `Prepare`, `EventsDir` (Tasks 3–4); `config.SubagentTrackingEnabled` (Task 5).
- Produces: `subagentScanMsg`, `subagentScanCmd`, `(*home).maybeSubagentScan`, `(*home).handleSubagentScan`, `subagentInterval`, and the fields `home.lastSubagentScan` and `home.subagentInFlight`.

- [ ] **Step 1: Write the failing tests**

`app/subagents_test.go`:
```go
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/stretchr/testify/require"
)

func TestSubagentScanDispatchesForClaude(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-first", "claude", "x")
	m := homeWithAppState(t)

	require.NotNil(t, m.maybeSubagentScan([]*session.Instance{inst}))
	require.True(t, m.subagentInFlight)
}

func TestSubagentScanThrottledWithinInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-window", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeSubagentScan(active))
	m.subagentInFlight = false
	require.Nil(t, m.maybeSubagentScan(active))

	m.lastSubagentScan = time.Now().Add(-subagentInterval - time.Second)
	require.NotNil(t, m.maybeSubagentScan(active))
}

func TestSubagentScanNotStackedWhileInFlight(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-inflight", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeSubagentScan(active))
	m.lastSubagentScan = time.Now().Add(-subagentInterval - time.Second)
	require.Nil(t, m.maybeSubagentScan(active))
}

// Same deadlock guard as the roster: no dispatch must arm nothing, or no
// message would ever clear the flag.
func TestSubagentScanNoClaudeDoesNotLatch(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-aider", "aider", "x")
	m := homeWithAppState(t)

	require.Nil(t, m.maybeSubagentScan([]*session.Instance{inst}))
	require.False(t, m.subagentInFlight)
	require.True(t, m.lastSubagentScan.IsZero())
}

func TestSubagentScanMsgClearsInFlightOnEveryDelivery(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-clear", "claude", "x")
	m := homeWithAppState(t)

	m.subagentInFlight = true
	m.Update(subagentScanMsg{})
	require.False(t, m.subagentInFlight)

	m.subagentInFlight = true
	m.Update(subagentScanMsg{results: []subagentScanResult{
		{instance: inst, err: errors.New("disk on fire")},
		{instance: inst, err: subagent.ErrNoHooks},
	}})
	require.False(t, m.subagentInFlight, "errors must re-arm scanning too")
}

func explorerResult(launchID string, replayed bool, extra ...subagent.Event) subagent.Result {
	events := append([]subagent.Event{{
		Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl",
	}}, extra...)
	return subagent.Result{
		LaunchID: launchID,
		Replayed: replayed,
		Events:   events,
		Meta:     map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "map code"}},
	}
}

func TestSubagentScanMsgAppliesAndGatesByLaunch(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-apply", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())

	// A result from another launch, even one that would clear everything, is dropped.
	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, result: explorerResult("L2", true,
		subagent.Event{Name: subagent.EventSessionEnd})}}})
	require.Len(t, inst.Subagents(), 1)
}

func TestSubagentScanCmdEndToEnd(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-e2e", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := subagent.Prepare(dir)
	require.NoError(t, err)
	root := t.TempDir()
	sub := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-a1.meta.json"),
		[]byte(`{"agentType":"Explore","description":"map code"}`), 0o600))
	payload := fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore","transcript_path":%q}`,
		filepath.Join(root, "sess.jsonl"))
	require.NoError(t, os.WriteFile(filepath.Join(subagent.EventsDir(dir), "1-1.json"), []byte(payload), 0o600))

	cmd := m.maybeSubagentScan([]*session.Instance{inst})
	require.NotNil(t, cmd)
	m.Update(cmd())

	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())
	require.False(t, m.subagentInFlight)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run Subagent`
Expected: build failure, `m.maybeSubagentScan undefined`.

- [ ] **Step 3: Create `app/subagents.go`**

```go
package app

import (
	"errors"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	tea "charm.land/bubbletea/v2"
)

// subagentInterval is the hook-event scan cadence, matching rosterInterval:
// the snapshot-path health tick fires every 500ms, far more often than
// hook events need collecting.
const subagentInterval = 3 * time.Second

// subagentScanResult is one instance's scan outcome.
type subagentScanResult struct {
	instance *session.Instance
	result   subagent.Result
	err      error
}

// subagentScanMsg carries a scan of every tracked instance back to Update.
type subagentScanMsg struct {
	results []subagentScanResult
}

// subagentScanCmd builds one scan covering every instance that wants one.
// Requests are built here, on the Update goroutine, because they read the
// tracker; the returned Cmd only touches the filesystem. Returns nil when
// there is nothing to scan.
func subagentScanCmd(active []*session.Instance) tea.Cmd {
	type job struct {
		inst *session.Instance
		req  subagent.Request
	}
	var jobs []job
	for _, inst := range active {
		if req, ok := inst.SubagentScanRequest(); ok {
			jobs = append(jobs, job{inst: inst, req: req})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	return func() tea.Msg {
		now := time.Now()
		results := make([]subagentScanResult, 0, len(jobs))
		for _, j := range jobs {
			res, err := subagent.Scan(j.req, now)
			results = append(results, subagentScanResult{instance: j.inst, result: res, err: err})
		}
		return subagentScanMsg{results: results}
	}
}

// maybeSubagentScan returns a scan when one is due, following
// maybeRosterQuery: none in flight, and at least subagentInterval since
// the last dispatch. When nothing is dispatched neither field is armed,
// since no scan means no subagentScanMsg to clear them. Call on the Update
// goroutine.
func (m *home) maybeSubagentScan(active []*session.Instance) tea.Cmd {
	if m.subagentInFlight || time.Since(m.lastSubagentScan) < subagentInterval {
		return nil
	}
	cmd := subagentScanCmd(active)
	if cmd == nil {
		return nil
	}
	m.subagentInFlight = true
	m.lastSubagentScan = time.Now()
	return cmd
}

// handleSubagentScan applies a scan. It clears the in-flight flag first: a
// delivery that did not would stop scanning for the rest of the session.
func (m *home) handleSubagentScan(msg subagentScanMsg) {
	m.subagentInFlight = false
	for _, r := range msg.results {
		if r.err != nil {
			// ErrNoHooks is the normal state of a session launched
			// without tracking.
			if !errors.Is(r.err, subagent.ErrNoHooks) {
				log.DebugKV("app.subagent.scan_failed", "instance", r.instance.Title, "err", r.err.Error())
			}
			continue
		}
		r.instance.ApplySubagentScan(r.result)
	}
}
```

- [ ] **Step 4: Wire it into `app/app.go` and `app/state_settings.go`**

1. After the `rosterInFlight  bool` field:
```go
	// lastSubagentScan / subagentInFlight throttle the hook-event scan
	// (see maybeSubagentScan), in the same way as the roster fields.
	lastSubagentScan time.Time
	subagentInFlight bool
```
2. Directly before `case rosterReadyMsg:`:
```go
	case subagentScanMsg:
		m.handleSubagentScan(msg)
		return m, nil
```
3. In the health tick, directly after the `if roster := m.maybeRosterQuery(active); roster != nil { ... }` block:
```go
		// Subagent hook events, throttled like the roster (see
		// maybeSubagentScan). nil when not due, in flight, or no Claude
		// agent is live.
		if scan := m.maybeSubagentScan(active); scan != nil {
			cmds = append(cmds, scan)
		}
```
4. After each of the two `session.SetLoomContextEnabled(appConfig.LoomContextEnabled())` lines in `app/app.go`, add:
```go
	session.SetSubagentTrackingEnabled(appConfig.SubagentTrackingEnabled())
```
5. In `app/state_settings.go`, after `session.SetLoomContextEnabled(m.appConfig.LoomContextEnabled())`:
```go
		session.SetSubagentTrackingEnabled(m.appConfig.SubagentTrackingEnabled())
```
6. At the end of `reconcileOrphans`, replace:
```go
	if storage != nil {
		summary.failed = len(storage.UnrecoveredTitles())
	}
	return summary
```
with:
```go
	claimed := make(map[string]bool)
	for _, inst := range list.GetInstances() {
		claimed[inst.Title] = true
	}
	if storage != nil {
		summary.failed = len(storage.UnrecoveredTitles())
		// Unrecovered records may come back on the next load; keep their
		// hooks folders.
		for _, title := range storage.UnrecoveredTitles() {
			claimed[title] = true
		}
	}
	session.SweepSubagentHooks(cfgDir, claimed, cmdExec)
	return summary
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `gofmt -w app && CGO_ENABLED=0 go test ./app/`
Expected: `ok`. If a `reconcileOrphans` test uses a strict mock executor that fails on unexpected commands, the sweep's `tmux list-sessions` call only runs when an unclaimed hooks folder exists, which test config dirs don't have. Check the failing test's config dir before changing either side.

- [ ] **Step 6: Commit**

```bash
git add app
git commit -F - <<'EOF'
feat(app): scan subagent hook events on the health tick

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 9: Rail count

**Files:**
- Create: `ui/card_agents.go`
- Modify: `ui/card.go` (`CardData` field; `BuildCardData`; `RenderCard` second-line selection)
- Test: `ui/card_agents_test.go`

**Interfaces:**
- Consumes: `Instance.Subagents() []subagent.View`, `Instance.ApplySubagentScan` (Task 7).
- Produces: `type SubagentRow struct{ Name, Description string; Idle bool }`, `CardData.Subagents []SubagentRow`, `func (d CardData) agentSummary() string`.

- [ ] **Step 1: Write the failing tests**

`ui/card_agents_test.go`:
```go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agents(idle ...bool) []SubagentRow {
	rows := make([]SubagentRow, len(idle))
	for i, isIdle := range idle {
		rows[i] = SubagentRow{Name: "agent", Description: "task", Idle: isIdle}
	}
	return rows
}

func TestAgentSummary(t *testing.T) {
	cases := []struct {
		rows []SubagentRow
		want string
	}{
		{nil, ""},
		{agents(false), " · 1 agent"},
		{agents(true), " · 1 agent (idle)"},
		{agents(false, false, false), " · 3 agents"},
		{agents(false, true, false), " · 3 agents (1 idle)"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, CardData{Subagents: tc.rows}.agentSummary())
	}
}

func TestRenderCard_RailAgentsReplaceTail(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		TailLines: []string{"waiting for agents"}, Subagents: agents(false, true, false)}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "✻ working · 3 agents (1 idle)")
	assert.NotContains(t, out, "waiting for agents")
}

func TestRenderCard_RailAttentionKeepsReasonAndCount(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Prompting, WaitReason: "sandbox request",
		StatusAge: 4 * time.Minute, Subagents: agents(false, false)}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "❯ sandbox request · 4m · 2 agents")
}

func TestRenderCard_RailWithoutAgentsShowsTail(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		TailLines: []string{"compiling"}}
	out := plain(RenderCard(d, DensityRail, 60))
	assert.Contains(t, out, "compiling")
	assert.NotContains(t, out, "agent")
}

func TestRenderCard_RailCountCutBeforeStatus(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Running, Spinner: "✻",
		Subagents: agents(false, true, false)}
	out := plain(RenderCard(d, DensityRail, 16))
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(l), 16)
	}
	assert.Contains(t, out, "✻ working")
}

func TestBuildCardData_CopiesSubagents(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "cd", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.ApplySubagentScan(subagent.Result{
		LaunchID: "L", Replayed: true,
		Events: []subagent.Event{{Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "map code"}},
	}))

	d := BuildCardData(inst, false, "", 0)
	assert.Equal(t, []SubagentRow{{Name: "Explore", Description: "map code"}}, d.Subagents)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'AgentSummary|RailAgents|RailAttention|RailWithoutAgents|RailCount|CopiesSubagents'`
Expected: build failure, `undefined: SubagentRow`.

- [ ] **Step 3: Implement**

`ui/card_agents.go`:
```go
package ui

import "fmt"

// SubagentRow is one live subagent or teammate shown on a card.
type SubagentRow struct {
	Name        string
	Description string
	Idle        bool
}

// agentSummary is the rail's count suffix, e.g. " · 3 agents (1 idle)".
// Empty when the card has no live agents.
func (d CardData) agentSummary() string {
	n := len(d.Subagents)
	if n == 0 {
		return ""
	}
	idle := 0
	for _, r := range d.Subagents {
		if r.Idle {
			idle++
		}
	}
	switch {
	case n == 1 && idle == 1:
		return " · 1 agent (idle)"
	case n == 1:
		return " · 1 agent"
	case idle == 0:
		return fmt.Sprintf(" · %d agents", n)
	default:
		return fmt.Sprintf(" · %d agents (%d idle)", n, idle)
	}
}
```

In `ui/card.go`, add to `CardData` after `WaitReason`:
```go
	// Subagents lists the session's live subagents and teammates, working
	// first. Empty for most cards; see session.Instance.Subagents.
	Subagents []SubagentRow
```

In `BuildCardData`, before `if stat := inst.GetDiffStats(); ...`:
```go
	for _, v := range inst.Subagents() {
		d.Subagents = append(d.Subagents, SubagentRow{Name: v.Name, Description: v.Description, Idle: v.Idle})
	}
```

In `RenderCard`, replace:
```go
	// Second line: attention prompt beats tail beats status label.
	second := d.statusLabel()
	secondFg := Dim
	if d.NeedsAttention() {
		secondFg = Attention
	} else if len(d.TailLines) > 0 {
		second = d.TailLines[len(d.TailLines)-1]
	}
```
with:
```go
	// Second line: attention prompt, then live agents, then tail, then
	// status label. The agent count is a suffix, so end-truncation drops
	// it before the status.
	second := d.statusLabel()
	secondFg := Dim
	switch {
	case d.NeedsAttention():
		secondFg = Attention
		second += d.agentSummary()
	case len(d.Subagents) > 0:
		second += d.agentSummary()
	case len(d.TailLines) > 0:
		second = d.TailLines[len(d.TailLines)-1]
	}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w ui && CGO_ENABLED=0 go test ./ui/`
Expected: `ok` (the existing card tests pass unchanged).

- [ ] **Step 5: Commit**

```bash
git add ui
git commit -F - <<'EOF'
feat(ui): show live subagent count on rail cards

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 10: Overview agent rows

**Files:**
- Modify: `ui/card_agents.go` (add row rendering)
- Modify: `ui/overview.go` (`renderOverviewCard` tail block)
- Test: `ui/card_agents_test.go`, `ui/overview_test.go` (`TestOverview_UniformCardHeight`)

**Interfaces:**
- Consumes: `SubagentRow`, `CardData.Subagents` (Task 9); `overviewCardTailLines`, `truncate`, theme roles `Rule`, `OK`, `Dim`, `Text`.
- Produces: `func overviewTail(d CardData, inner int) []string`.

- [ ] **Step 1: Write the failing tests**

Append to `ui/card_agents_test.go`:
```go
func overviewLines(t *testing.T, d CardData, width int) []string {
	t.Helper()
	return strings.Split(plain(renderOverviewCard(d, width)), "\n")
}

func TestOverview_NoAgentsKeepsTail(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, TailLines: []string{"one", "two"}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "one")
	assert.Contains(t, lines[5], "two")
}

func TestOverview_OneAgentKeepsLastTailLine(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, TailLines: []string{"one", "two"},
		Subagents: []SubagentRow{{Name: "impl-t3", Description: "Implement Task 3"}}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "└ ✻ impl-t3  Implement Task 3")
	assert.Contains(t, lines[5], "two")
}

func TestOverview_TwoAgentsAligned(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "impl-t3", Description: "Implement Task 3"},
		{Name: "spec", Description: "Review spec", Idle: true},
	}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "├ ✻ impl-t3  Implement Task 3")
	assert.Contains(t, lines[5], "└ ◦ spec     idle")
	assert.NotContains(t, lines[5], "Review spec", "idle rows show 'idle'")
}

func TestOverview_ManyAgentsSummarized(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "a", Description: "first"},
		{Name: "b", Description: "second"},
		{Name: "c", Idle: true},
		{Name: "d", Idle: true},
	}}
	lines := overviewLines(t, d, 50)
	assert.Contains(t, lines[4], "├ ✻ a  first")
	assert.Contains(t, lines[5], "└ +3 more · 1 working · 2 idle")

	allIdle := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: "a", Description: "first"}, {Name: "c", Idle: true}, {Name: "d", Idle: true},
	}}
	lines = overviewLines(t, allIdle, 50)
	assert.Contains(t, lines[5], "└ +2 more · 2 idle")
	assert.NotContains(t, lines[5], "working")
}

func TestOverview_LongNamesCapped(t *testing.T) {
	d := CardData{Title: "db", Status: session.Running, Subagents: []SubagentRow{
		{Name: strings.Repeat("n", 30), Description: "desc"},
		{Name: "b", Description: "other"},
	}}
	lines := overviewLines(t, d, 60)
	assert.Contains(t, lines[4], strings.Repeat("n", 13)+"…  desc")
	assert.Contains(t, lines[5], "└ ✻ b"+strings.Repeat(" ", 13)+"  other")
}
```

In `ui/overview_test.go`, add these entries to the `variants` slice in `TestOverview_UniformCardHeight`:
```go
		{Title: "one-agent", Status: session.Running, TailLines: []string{"a", "b"},
			Subagents: []SubagentRow{{Name: "impl", Description: "work"}}},
		{Title: "two-agents", Status: session.Running, Subagents: []SubagentRow{
			{Name: "impl", Description: "work"}, {Name: "rev", Idle: true}}},
		{Title: "many-agents", Status: session.Prompting, Subagents: []SubagentRow{
			{Name: strings.Repeat("long-name-", 5), Description: strings.Repeat("long description ", 6)},
			{Name: "b"}, {Name: "c", Idle: true}, {Name: "d", Idle: true}}},
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestOverview_'`
Expected: `FAIL` in the new overview tests (agent rows not rendered); `TestOverview_NoAgentsKeepsTail` and `TestOverview_UniformCardHeight` pass.

- [ ] **Step 3: Implement the rows**

Append to `ui/card_agents.go` (and extend its imports to `"fmt"`, `"strings"`, `"charm.land/lipgloss/v2"`, `"github.com/charmbracelet/x/ansi"`, `"github.com/mattn/go-runewidth"`):
```go
// agentNameMax caps the overview name column.
const agentNameMax = 14

// Row glyphs. ◦ is used rather than Claude's own ◯, which has
// East-Asian-ambiguous width and can render two columns wide.
const (
	agentGlyphWorking = "✻"
	agentGlyphIdle    = "◦"
)

// overviewTail fills an overview card's tail slots. With live agents they
// show agent rows; otherwise the output tail. Always exactly
// overviewCardTailLines lines, so card height never changes.
func overviewTail(d CardData, inner int) []string {
	dim := lipgloss.NewStyle().Foreground(Dim)
	var lines []string
	switch n := len(d.Subagents); {
	case n == 0:
		for _, l := range d.TailLines {
			lines = append(lines, dim.Render(truncate(l, inner)))
		}
	case n == 1:
		lines = append(lines, agentRow("└", d.Subagents[0], nameColumn(d.Subagents), inner))
		if len(d.TailLines) > 0 {
			lines = append(lines, dim.Render(truncate(d.TailLines[len(d.TailLines)-1], inner)))
		}
	case n == 2:
		col := nameColumn(d.Subagents)
		lines = append(lines,
			agentRow("├", d.Subagents[0], col, inner),
			agentRow("└", d.Subagents[1], col, inner))
	default:
		lines = append(lines,
			agentRow("├", d.Subagents[0], nameColumn(d.Subagents[:1]), inner),
			moreRow(d.Subagents[1:], inner))
	}
	for len(lines) < overviewCardTailLines {
		lines = append(lines, "")
	}
	return lines[:overviewCardTailLines]
}

// nameColumn is the width names are padded to: the longest shown, capped.
func nameColumn(rows []SubagentRow) int {
	w := 0
	for _, r := range rows {
		w = max(w, runewidth.StringWidth(r.Name))
	}
	return min(w, agentNameMax)
}

// agentRow renders "<tree> <glyph> <name>  <text>" within inner columns.
func agentRow(tree string, r SubagentRow, nameCol, inner int) string {
	glyph, glyphFg, text, textFg := agentGlyphWorking, OK, r.Description, Text
	if r.Idle {
		glyph, glyphFg, text, textFg = agentGlyphIdle, Dim, "idle", Dim
	}
	name := truncate(r.Name, nameCol)
	name += strings.Repeat(" ", max(0, nameCol-runewidth.StringWidth(name)))

	line := lipgloss.NewStyle().Foreground(Rule).Render(tree) + " " +
		lipgloss.NewStyle().Foreground(glyphFg).Render(glyph) + " " +
		lipgloss.NewStyle().Foreground(Text).Render(name)
	if text != "" {
		line += "  " + lipgloss.NewStyle().Foreground(textFg).Render(text)
	}
	return ansi.Truncate(line, inner, "…")
}

// moreRow summarizes the agents a card has no room for, e.g.
// "└ +3 more · 1 working · 2 idle", leaving out zero counts.
func moreRow(hidden []SubagentRow, inner int) string {
	working, idle := 0, 0
	for _, r := range hidden {
		if r.Idle {
			idle++
		} else {
			working++
		}
	}
	text := fmt.Sprintf("+%d more", len(hidden))
	if working > 0 {
		text += fmt.Sprintf(" · %d working", working)
	}
	if idle > 0 {
		text += fmt.Sprintf(" · %d idle", idle)
	}
	line := lipgloss.NewStyle().Foreground(Rule).Render("└") + " " +
		lipgloss.NewStyle().Foreground(Dim).Render(text)
	return ansi.Truncate(line, inner, "…")
}
```

In `ui/overview.go` `renderOverviewCard`, replace:
```go
	tails := make([]string, 0, overviewCardTailLines)
	for _, l := range d.TailLines {
		tails = append(tails, dim.Render(truncate(l, inner)))
	}
	for len(tails) < overviewCardTailLines {
		tails = append(tails, "")
	}
```
with:
```go
	tails := overviewTail(d, inner)
```
If `dim` is now unused in `renderOverviewCard`, the compiler will say so; it is still used for the branch line, so keep it.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `gofmt -w ui && CGO_ENABLED=0 go test ./ui/`
Expected: `ok`. If `TestOverview_LongNamesCapped` fails on the ellipsis position, check `truncate`: at width 14 it returns 13 runes plus `…`. Fix the expectation only if `truncate`'s documented behaviour differs; otherwise fix the code.

- [ ] **Step 5: Commit**

```bash
git add ui
git commit -F - <<'EOF'
feat(ui): list live subagents on overview cards

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 11: Opt-in real Claude test

**Files:**
- Create: `session/subagent/realclaude_test.go`

**Interfaces:**
- Consumes: `Prepare`, `SettingsPath`, `Scan`, `Request`, `NewTracker`, `Tracker.Apply`, `Visible`, `MissingMeta`, and the event constants (Tasks 1–4).
- Produces: nothing used by other tasks.

- [ ] **Step 1: Write the test**

`session/subagent/realclaude_test.go`:
```go
package subagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRealClaude_TeammateLifecycle repeats the 2026-09-16 probe against a
// real interactive Claude session on a private tmux server. It spends a
// few cents of haiku usage and leaves entries in ~/.claude/projects and
// ~/.claude/teams, so it only runs with LOOM_TEST_REAL_CLAUDE=1 and never
// in CI. It covers the parts of the hook contract that were observed
// rather than documented: background_tasks and the teammate event order.
func TestRealClaude_TeammateLifecycle(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	launchID, err := Prepare(hooks)
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	sock := fmt.Sprintf("loomsub-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	screen := func() string { return tm("capture-pane", "-p", "-t", "probe") }
	send := func(text string) {
		tm("send-keys", "-t", "probe", "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", "probe", "Enter")
	}

	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	tm("send-keys", "-t", "probe", fmt.Sprintf("claude --model haiku --settings '%s'", SettingsPath(hooks)), "Enter")

	// Wait for Claude itself, not a "❯" that a shell prompt may also print.
	const trustDialog = "trust this folder"
	waitFor(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, trustDialog) {
		// "No, exit" is always listed; it is the default only for some
		// folders (those under a temp dir, for one).
		if strings.Contains(s, "❯ No, exit") {
			tm("send-keys", "-t", "probe", "Down")
		}
		tm("send-keys", "-t", "probe", "Enter")
	}
	waitFor(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, "Claude Code") && !strings.Contains(s, trustDialog)
	})
	time.Sleep(2 * time.Second) // let the input box take focus

	tracker := NewTracker()
	cold := true
	var seen []Event
	collect := func() {
		res, err := Scan(Request{Dir: hooks, Cold: cold, MissingMeta: tracker.MissingMeta()}, time.Now())
		require.NoError(t, err)
		require.Equal(t, launchID, res.LaunchID)
		if res.Replayed {
			tracker.Reset()
		}
		cold = false
		tracker.Apply(res.Events, res.Meta)
		seen = append(seen, res.Events...)
	}

	send("Automated test. Use the Agent tool exactly once with name 'probe-mate', " +
		"subagent_type general-purpose, description 'probe teammate', " +
		"prompt 'Reply with the word pong. Use no tools.' Then wait for its reply and say DONE.")
	waitFor(t, 3*time.Minute, func() bool {
		collect()
		return idleThenStop(seen, "probe-mate")
	})
	requireOrder(t, seen, EventSubagentStart, EventSubagentStop, EventTeammateIdle, EventStop)
	waitFor(t, 30*time.Second, func() bool { collect(); return len(tracker.Visible()) == 1 })
	require.Equal(t, []View{{Name: "probe-mate", Description: "probe teammate", Idle: true}}, tracker.Visible())

	send("Send probe-mate a shutdown request with SendMessage, wait until it has terminated, then say ALLDONE.")
	waitFor(t, 3*time.Minute, func() bool {
		collect()
		return len(tracker.Visible()) == 0
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// idleThenStop reports whether a TeammateIdle for name is followed by a
// parent Stop.
func idleThenStop(events []Event, name string) bool {
	idle := false
	for _, e := range events {
		if e.Name == EventTeammateIdle && e.TeammateName == name {
			idle = true
		}
		if idle && e.Name == EventStop {
			return true
		}
	}
	return false
}

// requireOrder asserts want appears in events as a subsequence.
func requireOrder(t *testing.T, events []Event, want ...string) {
	t.Helper()
	i := 0
	for _, e := range events {
		if i < len(want) && e.Name == want[i] {
			i++
		}
	}
	got := make([]string, len(events))
	for j, e := range events {
		got[j] = e.Name
	}
	require.Equal(t, len(want), i, "event order %v does not contain %v in order", got, want)
}
```

- [ ] **Step 2: Confirm it skips by default**

Run: `CGO_ENABLED=0 go test ./session/subagent/ -run TestRealClaude -v`
Expected: `--- SKIP: TestRealClaude_TeammateLifecycle`.

- [ ] **Step 3: Run it once for real (costs a few cents; ask the user first)**

Run: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/subagent/ -run TestRealClaude -v -timeout 10m`
Expected: `PASS`. If it fails, read the event order printed by `requireOrder` before changing anything. A failure here means Claude's behaviour differs from the spec's findings, and the spec must be updated first.

- [ ] **Step 4: Commit**

```bash
git add session/subagent/realclaude_test.go
git commit -F - <<'EOF'
test(subagent): add opt-in real Claude hook contract test

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```

---

### Task 12: Documentation and final verification

**Files:**
- Modify: `CLAUDE.md`, `USAGE.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Update CLAUDE.md**

1. In **Key Packages**, after the `session/agent/` bullet, add:
```markdown
- **`session/subagent/`** — Tracks the subagents and agent-team teammates a Claude session spawns, from Claude Code hook events. `hooks.go` writes the per-launch hooks folder (`settings.json`, `launch-id`, `events/`); `scan.go` turns new event files into compact `.ev` files and replays them when needed; `tracker.go` is the state machine; `event.go`/`meta.go` parse payloads and the `agent-<id>.meta.json` sidecars. No tmux, UI or app dependency. `session/subagent_hooks.go` wires it into `Instance`.
```
2. In **Gotchas**, after the Claude roster bullet, add:
```markdown
- **Subagent rows come from hooks, not transcripts.** Transcripts can't tell you whether an agent is running (each content block is its own record, and `stop_reason` is missing from ~60% of responses), so Claude is launched with `--settings {ConfigDir}/hooks/<tmux-name>/settings.json`, registering `SubagentStart`/`SubagentStop`/`TeammateIdle`/`Stop`/`SessionEnd`. Each hook writes a file to `events/`; `maybeSubagentScan` (throttled and guarded like `maybeRosterQuery`, and `handleSubagentScan` must clear `subagentInFlight` on every delivery) collects them into `Instance`'s `subagent.Tracker`. Rules that are easy to break: hooks are prepared **only on real launches** (`launchProgram(…, true)`: `Start(true)`, `startFreshWithRecovery`, `CrashRestart`), never on `Restore`, because a reattached Claude is still writing to its folder. The folder lives outside `worktrees/` because `DiscoverOrphans` would walk it. Only the **parent's `Stop`** reconciles against `background_tasks` (a `SubagentStop` list can omit a parallel foreground agent), and a missing or malformed list must never be read as empty. Teammates can't be matched by ID in that list, so shutdown is inferred by count. Read events are kept as compact `.ev` files so a restarted loom replays them (`Request.Cold`); a restored instance adopts the folder's `launch-id`, and results for another launch ID are dropped. Everything fails closed: an agent without its metadata sidecar is hidden. Windows, non-Claude programs, a user-supplied `--settings`, or a `'` in the path launch without hooks. `claude_subagent_tracking` only decides whether a launch gets hooks; sessions launched with hooks keep being scanned. Opt-in contract test: `LOOM_TEST_REAL_CLAUDE=1 go test ./session/subagent -run TestRealClaude` (costs money).
```
3. In **Persistent State**, in the `config.json` bullet, after the `Claude1MContext` description, add:
```markdown
, `ClaudeSubagentTracking` (`*bool`, default on — launches Claude sessions with loom's subagent hooks; nil is treated as enabled, read via `Config.SubagentTrackingEnabled()`, toggled from the Claude Preferences overlay's Track Subagents row)
```
4. In the **Persistent State** list, after `worktrees/`, add:
```markdown
- `hooks/` — per-instance Claude hook folders for subagent tracking (see `session/subagent`); emptied at each launch, removed when an instance is killed, swept when unclaimed and its tmux session is gone
```

- [ ] **Step 2: Update USAGE.md**

1. In **Left Panel — Session Rail**, replace the first paragraph's status examples sentence with:
```markdown
Each session renders as a live mini-card: its title, a status line with wait age (e.g. `❯ awaiting input · 4m`, `✻ working`, `✓ idle`, `paused · 3d`, `⟲ recoverable`), and a tail of recent agent output. When Claude says why it is waiting, the reason replaces the generic phrase (`❯ sandbox request · 4m`). When a Claude session has live subagents or teammates, the status line replaces the tail and ends with a count, e.g. `✻ working · 3 agents (1 idle)`.
```
2. In **Overview Mode** (the section at line ~107), after the sentence ending `and a live output tail.`, add:
```markdown
While a Claude session has live subagents, the tail shows them instead: `✻` for working, `◦` for idle, with a `+N more` line when more than two are running.
```
3. After the **Claude Remote Control** section, add:
```markdown
### Subagent Tracking

Loom shows the subagents and agent-team teammates a Claude session has spawned: a count on its rail card and rows on its overview card. It works by launching Claude with an extra `--settings` file that registers hooks for subagent events; your own hooks keep running alongside them.

- Toggle it with **Track Subagents** under `S` → Claude Preferences. It is on by default, and a change applies the next time a session launches or resumes.
- Only live agents are shown. A finished subagent disappears; a teammate stays listed as idle until it is shut down.
- Sessions launched before you enabled it aren't tracked until they are resumed. Sessions whose program already passes `--settings`, and sessions on Windows, are never tracked.
- Restarting loom keeps the rows: loom replays the events it already collected for sessions that are still running.
- Event files live in `~/.loom/hooks/`. They are cleared at each launch and removed when you kill the session.
```

- [ ] **Step 3: Full verification**

Run each command and confirm its result:
```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # expected: no output
CGO_ENABLED=0 go vet ./...                              # expected: no output
CGO_ENABLED=0 go build -o /dev/null .                  # expected: success
CGO_ENABLED=0 go test ./...                             # expected: every package ok
CGO_ENABLED=1 CC=clang go test -race ./app/ ./session/ ./session/subagent/ ./ui/   # expected: ok
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -F - <<'EOF'
docs: document subagent tracking

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HGRt44WaNdgJQvkimKHNRN
EOF
```
