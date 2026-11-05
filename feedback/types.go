package feedback

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind is what a note reports (spec §3).
type Kind string

const (
	KindBug      Kind = "bug"      // loom did something wrong
	KindFriction Kind = "friction" // it worked, awkwardly or surprisingly
	KindMissing  Kind = "missing"  // a capability the agent needed
	KindDocs     Kind = "docs"     // the loom context or a loom message is untrue or unclear
)

// Kinds are the kinds, in the order the CLI's help names them.
var Kinds = []Kind{KindBug, KindFriction, KindMissing, KindDocs}

// NoteID and ThreadID are counted per host, from 1.
type (
	NoteID   int
	ThreadID int
)

// Grouping is where a note stands with the grouper.
type Grouping string

const (
	Pending   Grouping = "pending"   // not yet offered to the grouper
	Grouped   Grouping = "grouped"   // in a thread
	Ungrouped Grouping = "ungrouped" // a call failed; retried at RetryAt
)

// Status is a thread's.
type Status string

const (
	Open   Status = "open"
	Fixing Status = "fixing"
	Closed Status = "closed"
)

// Session is who filed a note, as the daemon resolved the launch: never the
// agent's own text.
type Session struct {
	ID            uint64 `json:"id"` // core.InstanceID
	Title         string `json:"title"`
	Workspace     string `json:"workspace"`
	Branch        string `json:"branch,omitempty"`
	Agent         string `json:"agent,omitempty"`          // the subagent's agentId, "" for the main loop or unknown
	ClaudeSession string `json:"claude_session,omitempty"` // Claude's session ID
	Transcript    string `json:"transcript,omitempty"`     // its transcript's path as Claude reported it
}

// Note is one filed note.
type Note struct {
	ID       NoteID    `json:"id"`
	Kind     Kind      `json:"kind"`
	Title    string    `json:"title"`
	Text     string    `json:"text"`
	From     Session   `json:"from"`
	Build    string    `json:"build,omitempty"` // the daemon's version and build
	At       time.Time `json:"at"`
	Thread   ThreadID  `json:"thread,omitempty"`
	Grouping Grouping  `json:"grouping"`
	RetryAt  time.Time `json:"retry_at,omitzero"`
	GroupErr string    `json:"group_err,omitempty"` // why the last call failed
}

// Fix is a thread's fix session; a zero ID is none.
type Fix struct {
	ID        uint64    `json:"id"` // core.InstanceID
	Title     string    `json:"title"`
	Workspace string    `json:"workspace"`
	At        time.Time `json:"at"`
}

// Thread is a group of notes about one problem.
type Thread struct {
	ID     ThreadID  `json:"id"`
	Title  string    `json:"title"`
	Kind   Kind      `json:"kind"`
	Status Status    `json:"status"`
	Notes  []NoteID  `json:"notes"`
	Fix    Fix       `json:"fix,omitzero"`
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
}

// Note bounds (spec §3).
const (
	MaxTitle = 120    // characters
	MaxText  = 20_000 // bytes
)

// ValidateNote checks what an agent supplies: a known kind, a title of
// 1–MaxTitle characters on one line, a text of 1–MaxText bytes. Both are
// trimmed first; the caller stores the trimmed values.
func ValidateNote(kind Kind, title, text string) error {
	title, text = strings.TrimSpace(title), strings.TrimSpace(text)
	switch {
	case !slices.Contains(Kinds, kind):
		return fmt.Errorf("kind %q: it must be one of bug, friction, missing, docs", kind)
	case title == "":
		return errors.New("the title is empty")
	case strings.ContainsAny(title, "\r\n"):
		return errors.New("the title must be one line")
	case utf8.RuneCountInString(title) > MaxTitle:
		return fmt.Errorf("the title is %d characters: at most %d", utf8.RuneCountInString(title), MaxTitle)
	case text == "":
		return errors.New("the text is empty")
	case len(text) > MaxText:
		return fmt.Errorf("the text is %d bytes: at most %d", len(text), MaxText)
	}
	return nil
}
