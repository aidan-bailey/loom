package transcript

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/session/hooks"
)

// entry is what one line adds to the conversation.
type entry struct {
	off  int64
	at   time.Time
	bad  bool // not JSON, or longer than lineLimit
	size int  // its text bytes, for a page's text bound
	// opens is set when the line starts a turn, turn. compact marks a
	// /compact command record, which may repeat the plain /compact Claude
	// wrote before a boundary.
	opens   bool
	compact bool
	turn    Turn
	// parts are what it adds to the turn it falls in, in order.
	parts []part
}

// part is one thing a line adds to a turn: an item (its Kind), a step
// (partStep) or a step's result (partResult, its step's ID and State).
type part struct {
	kind  string
	text  string
	cut   bool
	error bool
	step  Step
}

const (
	partStep   = "step"
	partResult = "result"
)

// The interruption markers Claude writes as the user's text.
const (
	interrupted        = "[Request interrupted by user]"
	interruptedForTool = "[Request interrupted by user for tool use]"
)

// record is the part of a line the reader looks at. Origin and Message
// stay raw: a line of another type may hold another shape there.
type record struct {
	Type                      string          `json:"type"`
	Subtype                   string          `json:"subtype"`
	Timestamp                 string          `json:"timestamp"`
	Cwd                       string          `json:"cwd"`
	IsSidechain               bool            `json:"isSidechain"`
	IsMeta                    bool            `json:"isMeta"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	IsAPIErrorMessage         bool            `json:"isApiErrorMessage"`
	Origin                    json.RawMessage `json:"origin"`
	Message                   json.RawMessage `json:"message"`
}

// origin is a user record's origin, where Claude writes one.
type origin struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Body   string `json:"body"`
	AsUser bool   `json:"asUser"`
}

// block is one content block of a message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// decode reads the line at off.
func decode(off int64, data []byte) entry {
	e := entry{off: off}
	if data == nil {
		e.bad = true
		return e
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return e
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		// Valid JSON of another shape is no unreadable record.
		e.bad = !json.Valid(data)
		return e
	}
	e.at, _ = time.Parse(time.RFC3339Nano, r.Timestamp)
	if r.IsSidechain {
		return e
	}
	switch r.Type {
	case "system":
		if r.Subtype == "compact_boundary" {
			e.parts = []part{{kind: KindCompacted}}
		}
	case "assistant":
		e.assistant(r)
	case "user":
		e.user(r)
	}
	return e
}

// assistant reads Claude's blocks: text items and steps, or an API error's
// note.
func (e *entry) assistant(r record) {
	text, blocks := content(r.Message)
	if text != "" {
		blocks = []block{{Type: "text", Text: text}}
	}
	if r.IsAPIErrorMessage {
		var all []string
		for _, b := range blocks {
			if b.Type == "text" {
				all = append(all, b.Text)
			}
		}
		note := hooks.CapSummary(strings.Join(all, "\n"))
		if note == "" {
			note = "API error"
		}
		e.parts = []part{{kind: KindNote, text: note, error: true}}
		e.size = len(note)
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			t, cut := capBytes(b.Text, maxItem)
			e.parts = append(e.parts, part{kind: KindText, text: t, cut: cut})
			e.size += len(t)
		case "tool_use":
			e.parts = append(e.parts, part{kind: partStep, step: Step{
				ID: b.ID, Tool: b.Name, Summary: summarize(b.Name, b.Input, r.Cwd), At: e.at, State: StatePending,
			}})
		}
	}
}

// user reads a user record: tool results, an interruption, or a prompt
// that starts a turn, labelled by where it came from.
func (e *entry) user(r record) {
	if r.IsCompactSummary || r.IsVisibleInTranscriptOnly {
		return
	}
	text, blocks := content(r.Message)
	var results []part
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			results = append(results, part{kind: partResult, step: Step{ID: b.ToolUseID, State: resultState(b)}})
		case "text":
			texts = append(texts, b.Text)
		case "image":
			texts = append(texts, "[image]")
		}
	}
	if len(results) > 0 {
		e.parts = results
		return
	}
	if len(texts) > 0 {
		text = strings.Join(texts, "\n")
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == interrupted || trimmed == interruptedForTool {
		e.parts = []part{{kind: KindNote, text: "interrupted"}}
		return
	}
	var o origin
	_ = json.Unmarshal(r.Origin, &o)
	switch {
	case o.Kind == "peer":
		body := o.Body
		if body == "" {
			body = text
		}
		e.open(FromPeer, o.Name, body)
	case o.Kind == "plugin" && o.AsUser:
		e.open(FromYou, "", text)
		e.turn.Command = isCommandLine(trimmed)
	case o.Kind == "plugin":
		e.open(FromPlugin, o.Name, text)
	case o.Kind == "task-notification" || strings.HasPrefix(trimmed, "<task-notification>"):
		e.open(FromTask, "", taskText(trimmed))
	case r.IsMeta:
	case strings.HasPrefix(trimmed, "<command-name>") || strings.HasPrefix(trimmed, "<command-message>"):
		name := tagText(trimmed, "command-name")
		if name == "" {
			return
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		cmd := name
		if args := tagText(trimmed, "command-args"); args != "" {
			cmd += " " + args
		}
		e.open(FromYou, "", cmd)
		e.turn.Command, e.compact = true, name == "/compact"
	case strings.HasPrefix(trimmed, "<bash-input>"):
		e.open(FromYou, "", "! "+tagText(trimmed, "bash-input"))
		e.turn.Command = true
	case hasAnyPrefix(trimmed, "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>", "<bash-stdout>", "<bash-stderr>"):
	case strings.HasPrefix(trimmed, "Another Claude session sent a message:"):
		// Older Claude wrote a peer's message as plain text.
		e.open(FromPeer, "", text)
	case trimmed != "":
		e.open(FromYou, "", text)
		e.turn.Command = isCommandLine(trimmed)
	}
}

// open makes e start a turn from from (and name) with text as its prompt.
func (e *entry) open(from, name, text string) {
	t, cut := capBytes(strings.TrimSpace(text), maxPrompt)
	e.opens = true
	e.turn = Turn{ID: e.off, At: e.at, From: from, Name: name, Text: t, Cut: cut}
	e.size += len(t)
}

// content reads a message's content: a string, or its blocks.
func content(message json.RawMessage) (string, []block) {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(message, &m) != nil || len(m.Content) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s, nil
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return "", nil
	}
	return "", blocks
}

// resultState is the state a tool result leaves its step in. Only a short
// result can be the interruption marker, so a long one is never decoded.
func resultState(b block) string {
	if len(b.Content) < 128 {
		var s string
		if json.Unmarshal(b.Content, &s) == nil && strings.TrimSpace(s) == interruptedForTool {
			return StateInterrupted
		}
		var blocks []block
		if json.Unmarshal(b.Content, &blocks) == nil && len(blocks) == 1 && strings.TrimSpace(blocks[0].Text) == interruptedForTool {
			return StateInterrupted
		}
	}
	if b.IsError {
		return StateFailed
	}
	return StateDone
}

// taskText is a task notification's line: its status and its summary.
func taskText(s string) string {
	status, summary := tagText(s, "status"), tagText(s, "summary")
	switch {
	case status != "" && summary != "":
		return status + ": " + summary
	case summary != "":
		return summary
	case status != "":
		return status
	}
	return "background task"
}

// tagText is the trimmed text between s's first <tag> and the </tag> after
// it, "" when either is missing.
func tagText(s, tag string) string {
	open, end := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// commandLine matches a prompt that is one slash command: "/name" and its
// arguments, the name holding no further slash ("/tmp/x is broken" is no
// command).
var commandLine = regexp.MustCompile(`^/[A-Za-z][A-Za-z0-9:_.-]*(\s|$)`)

func isCommandLine(s string) bool {
	return !strings.Contains(s, "\n") && commandLine.MatchString(s)
}

// capBytes is s's first n bytes, cut back to a rune boundary, and whether
// it cut.
func capBytes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// summaryKeys names, per tool, the input field its step shows. It is wider
// than the permission prompt's (session/hooks): a step is the user's own
// history to read, not a request to approve.
var summaryKeys = map[string]string{
	"Bash":         "command",
	"Read":         "file_path",
	"Edit":         "file_path",
	"Write":        "file_path",
	"MultiEdit":    "file_path",
	"NotebookEdit": "notebook_path",
	"Grep":         "pattern",
	"Glob":         "pattern",
	"WebFetch":     "url",
	"WebSearch":    "query",
	"ToolSearch":   "query",
	"Agent":        "description",
	"Task":         "description",
	"Skill":        "skill",
}

// summarize is a step's one line: its tool's field from summaryKeys, a path
// under cwd made relative to it, cut as a permission prompt's is
// (hooks.CapSummary). A tool not listed, or an input without the field as a
// string, has none.
func summarize(tool string, input json.RawMessage, cwd string) string {
	key, ok := summaryKeys[tool]
	if !ok {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(fields[key], &s) != nil {
		return ""
	}
	if strings.HasSuffix(key, "_path") {
		s = under(cwd, s)
	}
	return hooks.CapSummary(s)
}

// under is p relative to dir when p lies inside it, else p.
func under(dir, p string) string {
	if dir == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}
