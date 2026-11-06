// Package transcript reads one page of a Claude Code conversation from the
// transcript Claude writes for it, <config>/projects/<encoded cwd>/<session
// ID>.jsonl: its turns (a prompt, then Claude's replies and its tool calls
// as one-line steps), newest first, in pages bounded in turns, text and
// bytes read. It only reads. Claude's format is its own and undocumented,
// so a record the reader does not recognise adds nothing to a page.
package transcript

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/session/subagent"
)

// A page's bounds. A turn joins a page while the page holds fewer than
// maxTurns turns and the turn's text keeps the page within maxText bytes;
// the newest turn always joins. A prompt keeps its first maxPrompt bytes, a
// text item its first maxItem, and a run of steps its newest maxSteps.
const (
	maxTurns  = 10
	maxText   = 256 << 10
	maxPrompt = 16 << 10
	maxItem   = 64 << 10
	maxSteps  = 200
)

// A read takes at most scanBudget bytes of the file, and skips a line
// longer than lineLimit. Tests shrink them.
var (
	scanBudget int64 = 16 << 20
	lineLimit  int64 = 4 << 20
)

// Query says which turns to read.
type Query struct {
	// Conversation is the conversation the caller holds turns of, "" for
	// none. Another than the transcript's is answered with the newest
	// page, marked Reset.
	Conversation string
	// Before > 0 reads the page before this offset (a page's Earlier).
	Before int64
	// After > 0 reads every turn from the one at this offset (a turn's ID)
	// to the end of the file.
	After int64
	// Agent, when set, reads that subagent's transcript, <session>/subagents/
	// agent-<Agent>.jsonl beside the conversation's, with the same paging,
	// instead of the conversation. It must pass subagent.ValidAgentID.
	Agent string
}

// Page is the turns a query read, oldest first.
type Page struct {
	// Conversation is the transcript's session ID.
	Conversation string
	Turns        []Turn
	// Earlier is the Before of the page before this one, 0 when this page
	// starts the file and on a refresh's page (Query.After), whose caller
	// keeps the cursor its earlier pages gave.
	Earlier int64
	// Reset is set when the page is the newest page in place of what the
	// query asked for: the caller held another conversation's turns, or
	// its cursor lies past the end of the file or out of a read's reach.
	Reset bool
	// Skipped counts the page's lines that could not be read: not JSON,
	// or longer than lineLimit.
	Skipped int
	// Agent says whose page it is when Query.Agent was set, nil otherwise.
	Agent *AgentInfo
}

// AgentInfo describes the subagent a page belongs to, from its sidecar
// (agent-<id>.meta.json) and its file.
type AgentInfo struct {
	ID string
	// Type is the sidecar's agentType, Description its description or, with
	// none, the brief's first line.
	Type        string
	Description string
	Teammate    bool
	// State is AgentFinished or AgentRunning from the sidecar's finished
	// key, AgentUnknown when the sidecar or the key is missing. The file's
	// last record can't say: it is usually the handback, not an end.
	State string
	// Started is the file's first record's timestamp.
	Started time.Time
	// Missing says the agent's .jsonl does not exist: not written yet, or
	// deleted by Claude's cleanup.
	Missing bool
}

// The states of an AgentInfo.
const (
	AgentRunning  = "running"
	AgentFinished = "finished"
	AgentUnknown  = "unknown"
)

// Turn is one prompt and what Claude did with it.
type Turn struct {
	// ID is the byte offset of the turn's first record. Claude only
	// appends, so it names the turn for good.
	ID int64
	At time.Time
	// From says whose the prompt is: FromYou, FromPlugin, FromPeer,
	// FromTask or, for a subagent file's first prompt, FromBrief; Name is
	// the plugin's or the peer's name.
	From string
	Name string
	// Text is the prompt, trimmed, its first maxPrompt bytes; Cut says it
	// was cut.
	Text    string
	Cut     bool
	Command bool // a slash command, or a shell command ("! …")
	// Partial marks the part of a turn whose start lies before what was
	// read: no Text, its ID the first line read.
	Partial bool
	Items   []Item
}

// Who a turn's prompt came from.
const (
	FromYou    = "you"
	FromPlugin = "plugin"
	FromPeer   = "peer"
	FromTask   = "task"
	// FromBrief is a subagent file's first prompt: what the parent told it.
	FromBrief = "brief"
)

// Item is one thing Claude's side of a turn holds.
type Item struct {
	Kind string // KindText, KindThinking, KindSteps, KindCompacted or KindNote
	// At is its record's timestamp; a steps item's is its first step's.
	At time.Time
	// Text is a text or thinking item's markdown (its first maxItem bytes;
	// Cut says it was cut), or a note's line.
	Text  string
	Cut   bool
	Error bool // a note that is an API error
	// Steps are a steps item's newest maxSteps steps; Total and Failed
	// count every step of the run.
	Steps  []Step
	Total  int
	Failed int
}

// The kinds of Item.
const (
	KindText = "text"
	// KindThinking is a thinking block that holds text: Claude stores most
	// as a signature alone, and those add nothing.
	KindThinking  = "thinking"
	KindSteps     = "steps"
	KindCompacted = "compacted"
	KindNote      = "note"
)

// Step is one tool call.
type Step struct {
	ID      string // the tool_use block's id
	Tool    string
	Summary string
	At      time.Time
	State   string // StatePending, StateDone, StateFailed or StateInterrupted
}

// The states of a Step.
const (
	StatePending     = "pending"
	StateDone        = "done"
	StateFailed      = "failed"
	StateInterrupted = "interrupted"
)

// Read reads the page q asks for from the transcript at path, which belongs
// to conversation (its session ID). It refuses a path that is not
// absolute, not <conversation>.jsonl or not a regular file, so a bad hook
// payload can't point it at another file. It opens the file read-only and
// reads nothing past the last newline it finds: a line after that is one
// Claude is still writing. With q.Agent set it reads that subagent's file
// instead (agents.go); a bad agent id is an error.
func Read(path, conversation string, q Query) (Page, error) {
	if q.Before > 0 && q.After > 0 {
		return Page{}, errors.New("transcript: a query takes before or after, not both")
	}
	if q.Agent != "" && !subagent.ValidAgentID(q.Agent) {
		return Page{}, fmt.Errorf("transcript: agent %q: not an agent id", q.Agent)
	}
	if err := checkPath(path, conversation); err != nil {
		return Page{}, err
	}
	if q.Agent != "" {
		return readAgent(path, conversation, q)
	}
	f, size, err := openFile(path)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	page, err := read(f, size, conversation, q)
	if err != nil {
		return Page{}, fmt.Errorf("transcript %s: %w", path, err)
	}
	page.Conversation = conversation
	return page, nil
}

// checkPath refuses a path that is not the absolute path of conversation's
// transcript.
func checkPath(path, conversation string) error {
	switch {
	case !filepath.IsAbs(path):
		return fmt.Errorf("transcript %s: not an absolute path", path)
	case conversation == "" || filepath.Base(path) != conversation+".jsonl":
		return fmt.Errorf("transcript %s: not the transcript of conversation %q", path, conversation)
	}
	return nil
}

// openFile opens the regular file at path read-only, and returns its size.
func openFile(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("transcript %s: not a regular file", path)
	}
	return f, info.Size(), nil
}

// read reads q's page from r, whose first size bytes are the transcript (a
// subagent's when q.Agent is set).
func read(r io.ReaderAt, size int64, conversation string, q Query) (Page, error) {
	end, err := lastLineEnd(r, size)
	if err != nil {
		return Page{}, err
	}
	agent := q.Agent != ""
	other := q.Conversation != "" && q.Conversation != conversation
	if other || q.Before > end || (q.After > 0 && q.After >= end) {
		return pageBefore(r, end, true, agent)
	}
	if q.After > 0 {
		page, ok, err := since(r, end, q.After, agent)
		if err != nil || ok {
			return page, err
		}
		return pageBefore(r, end, true, agent)
	}
	to := end
	if q.Before > 0 {
		to = q.Before
	}
	return pageBefore(r, to, false, agent)
}

// pageBefore reads the newest page of the turns that end by to, marked
// reset when it stands in for another query.
func pageBefore(r io.ReaderAt, to int64, reset, agent bool) (Page, error) {
	var newestFirst []entry
	joined, text, pending := 0, 0, 0
	first := -1   // newestFirst's index of the oldest joined turn's first record
	more := false // a turn older than the page exists
	reached, err := scanBack(r, 0, to, scanBudget, func(off int64, data []byte) bool {
		e := decode(off, data, agent)
		newestFirst = append(newestFirst, e)
		pending += e.size
		// A /compact command record may repeat the turn before it (build
		// decides), so it starts no counted turn here.
		if !e.opens || e.compact {
			return true
		}
		if joined > 0 && (joined == maxTurns || text+pending > maxText) {
			more = true
			return false
		}
		joined, text, pending, first = joined+1, text+pending, 0, len(newestFirst)-1
		return true
	})
	if err != nil {
		return Page{}, err
	}
	window := newestFirst
	var earlier int64
	switch {
	case more || (!reached && first >= 0):
		window = newestFirst[:first+1]
		earlier = newestFirst[first].off
	case !reached && len(newestFirst) > 0:
		// The budget ran out inside one turn: what was read is its newest
		// part, and the page before ends where it begins.
		earlier = newestFirst[len(newestFirst)-1].off
	}
	entries := reverse(window)
	var start int64
	if len(entries) > 0 {
		start = entries[0].off
	}
	turns, skipped := build(entries, start)
	return Page{Turns: turns, Earlier: earlier, Reset: reset, Skipped: skipped}, nil
}

// since reads every turn from the one at after to end. It reports false
// when after lies out of a read's reach.
func since(r io.ReaderAt, end, after int64, agent bool) (Page, bool, error) {
	var newestFirst []entry
	reached, err := scanBack(r, after, end, scanBudget, func(off int64, data []byte) bool {
		newestFirst = append(newestFirst, decode(off, data, agent))
		return true
	})
	if err != nil || !reached {
		return Page{}, false, err
	}
	turns, skipped := build(reverse(newestFirst), after)
	return Page{Turns: turns, Skipped: skipped}, true, nil
}

// reverse is es in the opposite order.
func reverse(es []entry) []entry {
	out := make([]entry, len(es))
	for i, e := range es {
		out[len(es)-1-i] = e
	}
	return out
}
