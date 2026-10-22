// Package hooks is loom's side of the Claude Code hooks it registers at
// launch: the per-instance folder layout, the settings file and hook
// command, and the scan that turns the files the hooks write into Events.
// It does not interpret them: session/subagent tracks subagents from them,
// and session derives the session's status, ID and last message. See
// docs/superpowers/specs/2026-09-16-subagent-nesting-design.md and
// docs/superpowers/specs/2026-09-23-claude-hook-events-design.md.
//
// It has no dependency on tmux, the UI or the app, so every piece can be
// tested against payloads captured from a real Claude session.
package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Hook event names loom registers.
const (
	EventSubagentStart     = "SubagentStart"
	EventSubagentStop      = "SubagentStop"
	EventTeammateIdle      = "TeammateIdle"
	EventStop              = "Stop"
	EventSessionEnd        = "SessionEnd"
	EventSessionStart      = "SessionStart"
	EventUserPromptSubmit  = "UserPromptSubmit"
	EventPermissionRequest = "PermissionRequest"
	EventNotification      = "Notification"
)

// HookEvents lists the registered events.
var HookEvents = []string{
	EventSubagentStart, EventSubagentStop, EventTeammateIdle, EventStop, EventSessionEnd,
	EventSessionStart, EventUserPromptSubmit, EventPermissionRequest, EventNotification,
}

// MaxMessageBytes caps LastAssistantMessage and Message. Payloads can be
// up to maxEventBytes, and the text is kept in memory and in every .ev
// file; a card only shows a message's last few lines.
const MaxMessageBytes = 4 << 10

// MaxToolSummaryRunes caps Event.ToolSummary, counted in characters and cut
// with "…": it is one line on a phone's screen, not the tool's whole input.
const MaxToolSummaryRunes = 200

// ErrUnknownEvent is returned by ParseEvent for a JSON object whose
// hook_event_name is not one loom registers.
var ErrUnknownEvent = errors.New("hooks: unknown hook event")

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
	// SessionID and Source are kept for SessionStart only: the ID names
	// the conversation a relaunch resumes, and Source says why the
	// session started (startup, resume, clear, compact).
	SessionID string
	Source    string
	// ToolName and ToolSummary are kept for PermissionRequest only.
	// ToolSummary is one line saying what the tool asks to do, read from the
	// payload's tool_input (see toolSummary): empty for a tool this build
	// does not summarize and for an input it cannot read.
	ToolName    string
	ToolSummary string
	// NotificationType and Message are kept for Notification only.
	NotificationType string
	Message          string
	// LastAssistantMessage is kept for Stop only.
	LastAssistantMessage string
	// At is when the hook ran. Scan sets it from the file's name or
	// modification time; ParseEvent and Compact leave it alone, since it
	// is never part of a payload.
	At time.Time
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

	SessionID            string `json:"session_id,omitempty"`
	Source               string `json:"source,omitempty"`
	ToolName             string `json:"tool_name,omitempty"`
	NotificationType     string `json:"notification_type,omitempty"`
	Message              string `json:"message,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`

	// tool_input is the payload's own object. tool_summary is the compact
	// form's one line: Compact writes only that.
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	ToolSummary string          `json:"tool_summary,omitempty"`
}

// ParseEvent decodes a raw hook payload or a compact event. It returns an
// error wrapping ErrUnknownEvent for events loom does not register, and a
// different error for input that is not a JSON object.
func ParseEvent(data []byte) (Event, error) {
	var w wireEvent
	if err := json.Unmarshal(data, &w); err != nil {
		return Event{}, fmt.Errorf("hooks: parse event: %w", err)
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
	switch w.Name {
	case EventSessionStart:
		ev.SessionID, ev.Source = w.SessionID, w.Source
	case EventPermissionRequest:
		ev.ToolName = w.ToolName
		if len(w.ToolInput) > 0 {
			ev.ToolSummary = toolSummary(w.ToolName, w.ToolInput)
		} else {
			ev.ToolSummary = capSummary(w.ToolSummary)
		}
	case EventNotification:
		ev.NotificationType, ev.Message = w.NotificationType, capText(w.Message)
	case EventStop:
		ev.LastAssistantMessage = capText(w.LastAssistantMessage)
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

		SessionID:            e.SessionID,
		Source:               e.Source,
		ToolName:             e.ToolName,
		ToolSummary:          e.ToolSummary,
		NotificationType:     e.NotificationType,
		Message:              e.Message,
		LastAssistantMessage: e.LastAssistantMessage,
	}
	if e.HasTasks {
		tasks := e.Tasks
		if tasks == nil {
			tasks = []Task{}
		}
		raw, err := json.Marshal(tasks)
		if err != nil {
			return nil, fmt.Errorf("hooks: compact tasks: %w", err)
		}
		w.Tasks = raw
	}
	return json.Marshal(w)
}

// capText keeps the last MaxMessageBytes of s, starting on a character
// boundary: cards show a message's last lines, where Claude puts its
// summary or question.
func capText(s string) string {
	if len(s) <= MaxMessageBytes {
		return s
	}
	s = s[len(s)-MaxMessageBytes:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// toolSummaryKeys names, per tool, the tool_input field that says what the
// tool will do. A tool not listed has no summary: its input may hold
// anything, and the phone shows only what loom knows to be one line of
// intent.
var toolSummaryKeys = map[string]string{
	"Bash":         "command",
	"Edit":         "file_path",
	"Write":        "file_path",
	"MultiEdit":    "file_path",
	"NotebookEdit": "notebook_path",
	"WebFetch":     "url",
}

// toolSummary reads the summary of tool's request from a payload's
// tool_input. It fails closed: a tool without a key, an input that is not
// an object, and a key that is missing or not a string all give "".
func toolSummary(tool string, input json.RawMessage) string {
	key, ok := toolSummaryKeys[tool]
	if !ok {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(fields[key], &s); err != nil {
		return ""
	}
	return capSummary(s)
}

// capSummary reduces s to its first line that has any text, trimmed, and
// cuts that to MaxToolSummaryRunes characters, ending in "…" when it cut.
// It is idempotent, which ParseEvent relies on when it reads a compact
// event's tool_summary back.
func capSummary(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if utf8.RuneCountInString(s) <= MaxToolSummaryRunes {
		return s
	}
	return string([]rune(s)[:MaxToolSummaryRunes-1]) + "…"
}
