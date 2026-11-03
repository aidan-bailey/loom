# Answer asks from the phone: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a phone paired with loom answers Claude's `AskUserQuestion` dialogs and tool permission prompts, racing Claude's own dialog, first answer wins.

**Architecture:** the loom mod holds each ask (a `tool.call` hook on `AskUserQuestion`, a `classic.PermissionRequest` hook paired with its call by a pass-through `tool.call` hook), posts it to the daemon's mod socket and long-polls for an answer. The model keeps open asks on the launch's `modLink`, publishes them in `InstanceView.Asks`, and takes answers through a new `Core.Answer` request, which validates the answer and composes Claude's exact `result`, `deny` or `decision`. The web layer projects the asks and serves an answer route; the app draws a form per ask in the session screen's needs-you box.

**Tech stack:** Go (core, modserver, web), TypeScript (the mod, on Claude Code 2.1.316's function-hook API), Preact + htm (the app).

**Spec:** `docs/superpowers/specs/2026-11-03-pwa-answer-asks-design.md` (its Outcome section is the spike's record; every shape below comes from it).

**Rules that bind every package** (from the CLAUDE.md files): scratch files stay out of the worktree; commit with explicit paths; never run loom itself in a pane (dev builds through the `loom-dev` skill); never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand; `go test` runs with `CGO_ENABLED=0`; gofmt over `git ls-files '*.go' | grep -v '^vendor/'`.

**Settled while planning** (the spec's details):
- Every refusal of `Core.Answer` reaches the phone as 409 with the model's words: only three errors keep their identity across the wire, so the route does not tell 409 from 422.
- `AskView` carries the permission's input as a JSON string (`InputJSON`), not `json.RawMessage`: the wire round-trip test fills every field with arbitrary bytes.
- A permission ask the mod can't pair with an in-flight call is never posted (fail closed: nothing would ever close it).
- The model closes a main-loop permission ask on a status change only once it has seen the session Prompting while the ask was open (`seenPrompting`), so a stale status from before the hook scan read the prompt never closes a fresh ask.
- A closed ask leaves a tombstone on its link (up to 256), so the mod's re-post after a 404 never reopens it; a link that ends drops its asks without tombstones.

---

## Package A: the daemon holds asks

**Files:**
- Create: `core/asks.go`, `core/asks_test.go`
- Modify: `core/mod.go` (`modLink` fields, `newModLink`, `endLink`, `modGone`, `modTick`, `ModHost`), `core/loop_mod.go`, `core/claude_status.go` (`adoptClaudeStatus`), `core/view.go` (`InstanceView.Asks`), `core/views.go` (`viewOf`, `cloneViews`), `core/model.go` (`asksMoved`)
- Modify: `session/hooks/event.go` (export `ToolSummary`)
- Modify: `internal/modserver/server.go`, `internal/modserver/server_test.go`, `internal/modserver/realclaude_test.go` (queueHost implements the new methods)

- [ ] **A1: `hooks.ToolSummary`.** In `session/hooks/event.go` add, below `toolSummary`:

```go
// ToolSummary is toolSummary for a caller outside the package: the one line
// of intent the phone shows for tool's request (a Bash command's first line,
// a file path, a URL), "" for a tool with no summary key or an input that
// does not hold one.
func ToolSummary(tool string, input json.RawMessage) string { return toolSummary(tool, input) }
```

- [ ] **A2: write the failing model tests** in `core/asks_test.go`:

```go
package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// questionAsk is the ask the mod posts for the spike's two questions.
func questionAsk(id string) ModAsk {
	return ModAsk{Ask: id, Kind: AskQuestion, ToolUseID: "toolu_q", Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[
		{"question":"Which color do you prefer?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"warm"},{"label":"Blue","description":"cool","preview":"#00f"}]},
		{"question":"Which pets do you have?","header":"Pets","multiSelect":true,"options":[{"label":"Cat","description":"feline"},{"label":"Dog","description":"canine"},{"label":"Fish","description":"aquatic"}]}]}`)}
}

// permissionAsk is the ask the mod posts for a main-loop Bash prompt.
func permissionAsk(id, agent string) ModAsk {
	return ModAsk{Ask: id, Kind: AskPermission, AgentID: agent, ToolUseID: "toolu_p", Tool: "Bash",
		Input: json.RawMessage(`{"command":"mkdir made-by-phone\nsecond line","description":"Create directory"}`),
		Suggestions: []json.RawMessage{
			json.RawMessage(`{"type":"addDirectories","directories":["/w"],"destination":"session"}`),
			json.RawMessage(`{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test:*"}],"behavior":"allow","destination":"localSettings"}`),
		}}
}

func asksOf(m *Model, inst *session.Instance) []AskView {
	v, _ := m.View(m.idOf(inst))
	return v.Asks
}

func TestModAsk_PublishesTheAskParsed(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))

	asks := asksOf(m, inst)
	require.Len(t, asks, 2)
	q := asks[0]
	assert.Equal(t, "q1", q.ID)
	assert.Equal(t, AskQuestion, q.Kind)
	require.Len(t, q.Questions, 2)
	assert.Equal(t, AskQuestion{Question: "Which color do you prefer?", Header: "Color", Options: []AskOption{
		{Label: "Red", Description: "warm"}, {Label: "Blue", Description: "cool", Preview: "#00f"},
	}}, q.Questions[0])
	assert.True(t, q.Questions[1].MultiSelect)
	p := asks[1]
	assert.Equal(t, "Bash", p.Tool)
	assert.Equal(t, "mkdir made-by-phone", p.Summary, "the summary is the command's first line")
	assert.JSONEq(t, string(permissionAsk("p1", "").Input), p.InputJSON)
	assert.Equal(t, []AskSuggestion{
		{Type: "addDirectories", Directories: []string{"/w"}, Destination: "session"},
		{Type: "addRules", Rules: []AskRule{{ToolName: "Bash", RuleContent: "npm test:*"}}, Behavior: "allow", Destination: "localSettings"},
	}, p.Suggestions)
	assert.False(t, p.Opened.IsZero())
}

func TestModAsk_RefusesWhatItCannotHold(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	five := `{"questions":[` + `{"question":"a","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"b","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"c","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"d","header":"h","options":[{"label":"x","description":""}]},` +
		`{"question":"e","header":"h","options":[{"label":"x","description":""}]}]}`
	for name, a := range map[string]ModAsk{
		"no ID":            {Kind: AskQuestion, Tool: "AskUserQuestion", Input: questionAsk("").Input},
		"an unknown kind":  {Ask: "a", Kind: "elicitation", Tool: "X", Input: json.RawMessage(`{}`)},
		"no questions":     {Ask: "a", Kind: AskQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[]}`)},
		"five questions":   {Ask: "a", Kind: AskQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(five)},
		"a twice-asked question": {Ask: "a", Kind: AskQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[{"question":"a","header":"h","options":[{"label":"x"}]},{"question":"a","header":"h","options":[{"label":"y"}]}]}`)},
		"a repeated label": {Ask: "a", Kind: AskQuestion, Tool: "AskUserQuestion", Input: json.RawMessage(`{"questions":[{"question":"a","header":"h","options":[{"label":"x"},{"label":"x"}]}]}`)},
		"a permission input that is no object": {Ask: "a", Kind: AskPermission, Tool: "Bash", Input: json.RawMessage(`"ls"`)},
		"a permission with no tool":            {Ask: "a", Kind: AskPermission, Input: json.RawMessage(`{}`)},
		"a suggestion with no type":            {Ask: "a", Kind: AskPermission, Tool: "Bash", Input: json.RawMessage(`{}`), Suggestions: []json.RawMessage{json.RawMessage(`{}`)}},
	} {
		err := m.ModAsk("L1", "", a)
		assert.ErrorIs(t, err, ErrModBadAsk, name)
	}
	assert.ErrorIs(t, m.ModAsk("L9", "", questionAsk("q1")), ErrModUnknownLaunch)
}

func TestModAsk_PostedAgainKeepsOneAsk(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	assert.Len(t, asksOf(m, inst), 1)
}

func TestModAsk_ASupersededPollerIsRefused(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Poller: "new", Loaded: 2}))
	assert.ErrorIs(t, m.ModAsk("L1", "old", questionAsk("q1")), ErrModSuperseded)
	_, _, err := m.ModAnswer("L1", "old", "q1")
	assert.ErrorIs(t, err, ErrModSuperseded)
	assert.ErrorIs(t, m.ModAskClosed("L1", "old", "q1", "elsewhere"), ErrModSuperseded)
}

func TestModAnswer_HoldsAnOpenAskAndKnowsNoOther(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	payload, wake, err := m.ModAnswer("L1", "", "q1")
	require.NoError(t, err)
	assert.Nil(t, payload)
	require.NotNil(t, wake)
	assert.False(t, closed(wake))

	_, _, err = m.ModAnswer("L1", "", "nope")
	assert.ErrorIs(t, err, ErrModUnknownAsk)
}

func TestModAskClosed_TombstonesTheAsk(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	require.NoError(t, m.ModAskClosed("L1", "", "q1", "elsewhere"))

	assert.Empty(t, asksOf(m, inst))
	assert.True(t, closed(wake), "a held poll ends")
	_, _, err := m.ModAnswer("L1", "", "q1")
	assert.ErrorIs(t, err, ErrModAskClosed)
	assert.ErrorIs(t, m.ModAsk("L1", "", questionAsk("q1")), ErrModAskClosed, "a re-post never reopens a closed ask")
	assert.NoError(t, m.ModAskClosed("L1", "", "never-posted", "elsewhere"), "an unknown ask closes quietly")
}

// The roster turns busy within a second of a terminal answer, so a
// main-loop permission ask closes when the session leaves Prompting, but
// only once the model saw it Prompting while the ask was open: a status
// from before the hook scan read the prompt must not close a fresh ask.
func TestAsks_AMainLoopPermissionClosesWhenTheSessionLeavesPrompting(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p2", "agent-1")))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	m.asksSeeStatus(inst, false)
	assert.Len(t, asksOf(m, inst), 3, "not seen Prompting yet: nothing closes")

	m.asksSeeStatus(inst, true)
	m.asksSeeStatus(inst, false)

	asks := asksOf(m, inst)
	require.Len(t, asks, 2, "the main-loop permission closed")
	assert.Equal(t, "p2", asks[0].ID, "a subagent's waits for its call: the roster reads busy while a background agent asks")
	assert.Equal(t, "q1", asks[1].ID, "a question closes through its own hook")
}

func TestAsks_AnEndedLinkDropsItsAsks(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	m.modGone(inst, m.linkFor(inst), "the loom mod stopped answering")

	assert.Empty(t, asksOf(m, inst))
	assert.True(t, closed(wake))
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")), "a mod that comes back posts it again")
	assert.Len(t, asksOf(m, inst), 1)
}

func TestAsks_ViewsCarryACopy(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	views := cloneViews([]InstanceView{{Asks: asksOf(m, inst)}})
	views[0].Asks[0].Questions[0].Options[0].Label = "changed"
	assert.Equal(t, "Red", asksOf(m, inst)[0].Questions[0].Options[0].Label)
}

func TestAsks_AnAskWakesTheClient(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")
	m.asksMoved = false
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	assert.True(t, m.asksMoved)
}
```

- [ ] **A3: run them and see them fail to compile** (`ModAsk`, `AskView` … undefined):
  `CGO_ENABLED=0 go test ./core -run 'TestModAsk|TestModAnswer|TestModAskClosed|TestAsks_' 2>&1 | head`

- [ ] **A4: write `core/asks.go`:**

```go
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
)

// Asks are the dialogs a session's Claude holds open that the phone can
// answer: an AskUserQuestion dialog or a tool permission prompt. The loom mod
// holds each (session/claudemod), posts it here (ModAsk) and polls for its
// answer (ModAnswer); the first answer wins, Claude's own dialog's or the
// phone's (Answer). Design: docs/superpowers/specs/2026-11-03-pwa-answer-asks-design.md.

// The kinds of ask.
const (
	AskQuestion   = "question"
	AskPermission = "permission"
)

const (
	// maxAskQuestions and maxAskOptions are Claude's own caps on an
	// AskUserQuestion call (1 to 4 questions, 2 to 4 options each).
	maxAskQuestions = 4
	maxAskOptions   = 4
	// maxAskSuggestions bounds a permission's suggested rules, maxAskInput
	// its tool input, and maxAskID an ask's ID.
	maxAskSuggestions = 16
	maxAskInput       = 64 << 10
	maxAskID          = 128
	// askTakeTimeout is how long an answer waits for the mod to take it
	// before it is withdrawn (askTick).
	askTakeTimeout = 30 * time.Second
	// maxAskTombstones bounds the closed asks a link remembers.
	maxAskTombstones = 256
)

var (
	// ErrModBadAsk answers an ask the model can't hold: the mod leaves it to
	// Claude's dialog.
	ErrModBadAsk = errors.New("the ask is malformed")
	// ErrModUnknownAsk answers a poll for an ask the model doesn't hold (the
	// daemon restarted): the mod posts it again.
	ErrModUnknownAsk = errors.New("no such ask")
	// ErrModAskClosed answers a poll or a post for an ask that closed: the
	// mod stops waiting.
	ErrModAskClosed = errors.New("the ask is closed")
)

// ModAsk is an ask as the mod posts it. Input is the tool's input as Claude
// handed it; Suggestions are a permission request's permission_suggestions.
type ModAsk struct {
	Ask         string            `json:"ask"`
	Kind        string            `json:"kind"`
	AgentID     string            `json:"agentId,omitempty"`
	AgentType   string            `json:"agentType,omitempty"`
	ToolUseID   string            `json:"toolUseId,omitempty"`
	Tool        string            `json:"tool"`
	Input       json.RawMessage   `json:"input"`
	Suggestions []json.RawMessage `json:"suggestions,omitempty"`
}

// AskView is an open ask as a client sees it (InstanceView.Asks).
type AskView struct {
	ID   string
	Kind string
	// AgentID and AgentType name the subagent that asks; "" for the main
	// loop.
	AgentID   string
	AgentType string
	Opened    time.Time
	// Answering is set while an answer waits for the mod to take it.
	Answering bool
	// Questions are a question ask's.
	Questions []AskQuestion
	// Tool, Summary, InputJSON and Suggestions are a permission ask's: the
	// tool, its one line of intent (hooks.ToolSummary), its input as JSON
	// text, and the rules Claude suggests allowing with it.
	Tool        string
	Summary     string
	InputJSON   string
	Suggestions []AskSuggestion
}

// AskQuestion is one question of an AskUserQuestion call.
type AskQuestion struct {
	Question    string
	Header      string
	MultiSelect bool
	Options     []AskOption
}

// AskOption is one of a question's options.
type AskOption struct {
	Label       string
	Description string
	Preview     string
}

// AskSuggestion is one of a permission request's suggested rule changes, as
// Claude's PermissionUpdate has it: addRules, replaceRules and removeRules
// carry Rules and Behavior, setMode Mode, addDirectories and
// removeDirectories Directories.
type AskSuggestion struct {
	Type        string
	Destination string
	Behavior    string
	Mode        string
	Rules       []AskRule
	Directories []string
}

// AskRule is one permission rule: a tool, and what of it ("npm test:*").
type AskRule struct {
	ToolName    string
	RuleContent string
}

// modAsk is an open ask on a launch's link.
type modAsk struct {
	view AskView
	// questions and suggestions are what Claude handed, kept to hand back
	// verbatim (a result's questions, an allow's updatedPermissions).
	questions   json.RawMessage
	suggestions []json.RawMessage
	// wake closes when an answer arrives or the ask closes: a held poll ends.
	wake chan struct{}
	// answer is what the mod returns once it takes it; req the Answer that
	// made it, at when, and follow the person's words to send after it.
	answer json.RawMessage
	req    ReqID
	at     time.Time
	follow string
	// seenPrompting: the model saw the session Prompting while this ask was
	// open (asksSeeStatus).
	seenPrompting bool
}

func (a *modAsk) poke() {
	close(a.wake)
	a.wake = make(chan struct{})
}

// askBad is an ErrModBadAsk naming what is wrong.
func askBad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrModBadAsk, fmt.Sprintf(format, args...))
}

// parseAsk checks a and builds the ask the model holds, opened at now.
func parseAsk(a ModAsk, now time.Time) (*modAsk, error) {
	if a.Ask == "" || len(a.Ask) > maxAskID {
		return nil, askBad("ask: want an ID of 1 to %d bytes", maxAskID)
	}
	if len(a.Input) == 0 || len(a.Input) > maxAskInput {
		return nil, askBad("input: want 1 to %d bytes", maxAskInput)
	}
	out := &modAsk{wake: make(chan struct{}), view: AskView{
		ID: a.Ask, Kind: a.Kind, AgentID: a.AgentID, AgentType: a.AgentType, Opened: now.Round(0), Tool: a.Tool,
	}}
	switch a.Kind {
	case AskQuestion:
		return out, parseQuestions(out, a.Input)
	case AskPermission:
		return out, parsePermission(out, a)
	}
	return nil, askBad("kind: want %q or %q, not %q", AskQuestion, AskPermission, a.Kind)
}

// parseQuestions reads an AskUserQuestion input into out.
func parseQuestions(out *modAsk, input json.RawMessage) error {
	var in struct {
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return askBad("input: %v", err)
	}
	var qs []struct {
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
			Preview     string `json:"preview"`
		} `json:"options"`
	}
	if err := json.Unmarshal(in.Questions, &qs); err != nil {
		return askBad("questions: %v", err)
	}
	if len(qs) == 0 || len(qs) > maxAskQuestions {
		return askBad("questions: want 1 to %d, not %d", maxAskQuestions, len(qs))
	}
	seen := map[string]bool{}
	for _, q := range qs {
		if q.Question == "" || seen[q.Question] {
			return askBad("questions: each wants its own text")
		}
		seen[q.Question] = true
		if len(q.Options) == 0 || len(q.Options) > maxAskOptions {
			return askBad("question %q: want 1 to %d options", q.Question, maxAskOptions)
		}
		aq := AskQuestion{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
		labels := map[string]bool{}
		for _, o := range q.Options {
			if o.Label == "" || labels[o.Label] {
				return askBad("question %q: each option wants its own label", q.Question)
			}
			labels[o.Label] = true
			aq.Options = append(aq.Options, AskOption{Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		out.view.Questions = append(out.view.Questions, aq)
	}
	out.questions = in.Questions
	return nil
}

// parsePermission reads a permission request into out.
func parsePermission(out *modAsk, a ModAsk) error {
	if a.Tool == "" {
		return askBad("tool: want the tool's name")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(a.Input, &obj); err != nil {
		return askBad("input: want the tool's input object: %v", err)
	}
	if len(a.Suggestions) > maxAskSuggestions {
		return askBad("suggestions: want at most %d", maxAskSuggestions)
	}
	for _, raw := range a.Suggestions {
		var s struct {
			Type        string `json:"type"`
			Destination string `json:"destination"`
			Behavior    string `json:"behavior"`
			Mode        string `json:"mode"`
			Rules       []struct {
				ToolName    string `json:"toolName"`
				RuleContent string `json:"ruleContent"`
			} `json:"rules"`
			Directories []string `json:"directories"`
		}
		if err := json.Unmarshal(raw, &s); err != nil || s.Type == "" {
			return askBad("suggestion: want a typed permission update")
		}
		as := AskSuggestion{Type: s.Type, Destination: s.Destination, Behavior: s.Behavior, Mode: s.Mode, Directories: s.Directories}
		for _, r := range s.Rules {
			as.Rules = append(as.Rules, AskRule{ToolName: r.ToolName, RuleContent: r.RuleContent})
		}
		out.view.Suggestions = append(out.view.Suggestions, as)
		out.suggestions = append(out.suggestions, raw)
	}
	out.view.Summary = hooks.ToolSummary(a.Tool, a.Input)
	out.view.InputJSON = string(a.Input)
	return nil
}

// ask is the open ask id on l, nil for none.
func (l *modLink) ask(id string) *modAsk {
	for _, a := range l.asks {
		if a.view.ID == id {
			return a
		}
	}
	return nil
}

// closeAsk removes the open ask id, ends its held poll, and remembers it
// closed, so a re-post never reopens it.
func (l *modLink) closeAsk(id string) {
	for i, a := range l.asks {
		if a.view.ID == id {
			close(a.wake)
			l.asks = slices.Delete(l.asks, i, i+1)
			break
		}
	}
	if l.closedAsks == nil {
		l.closedAsks = map[string]bool{}
	}
	if !l.closedAsks[id] {
		l.closedAsks[id] = true
		l.closedOrder = append(l.closedOrder, id)
		if len(l.closedOrder) > maxAskTombstones {
			delete(l.closedAsks, l.closedOrder[0])
			l.closedOrder = l.closedOrder[1:]
		}
	}
}

// askLink is the instance and link of a mod's ask call: launch must be
// served, and poller the latest hello's (or unnamed).
func (m *Model) askLink(launch, poller string) (*session.Instance, *modLink, error) {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return nil, nil, ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	if poller != "" && l.poller != "" && poller != l.poller {
		return nil, nil, ErrModSuperseded
	}
	return inst, l, nil
}

// ModAsk holds an ask launch's mod posts. One posted again stays as it is;
// one that closed is ErrModAskClosed; one the model can't hold is
// ErrModBadAsk, and the mod leaves it to Claude's dialog.
func (m *Model) ModAsk(launch, poller string, a ModAsk) error {
	inst, l, err := m.askLink(launch, poller)
	if err != nil {
		return err
	}
	if l.closedAsks[a.Ask] {
		return ErrModAskClosed
	}
	if l.ask(a.Ask) != nil {
		return nil
	}
	ask, err := parseAsk(a, time.Now())
	if err != nil {
		log.For("core").Warn("mod.ask_refused", "instance", inst.Title, "kind", a.Kind, "tool", a.Tool, "err", err)
		return err
	}
	l.asks = append(l.asks, ask)
	m.asksMoved = true
	log.For("core").Info("mod.ask", "instance", inst.Title, "ask", a.Ask, "kind", a.Kind, "tool", a.Tool, "agent", a.AgentID)
	return nil
}

// ModAnswer is the answer to launch's ask id once the phone gave one, and
// the channel a held poll waits on until then. Taking the answer closes the
// ask, answers the Answer's request, and sends the person's words that
// follow it (a deny's message, a chat's text) as their own reply. A nil
// channel and no answer: the daemon is stopping.
func (m *Model) ModAnswer(launch, poller, id string) (json.RawMessage, <-chan struct{}, error) {
	inst, l, err := m.askLink(launch, poller)
	if err != nil {
		return nil, nil, err
	}
	if l.closedAsks[id] {
		return nil, nil, ErrModAskClosed
	}
	a := l.ask(id)
	switch {
	case a == nil:
		return nil, nil, ErrModUnknownAsk
	case m.modsStopped:
		return nil, nil, nil
	case a.answer == nil:
		return nil, a.wake, nil
	}
	payload, req, follow := a.answer, a.req, a.follow
	l.closeAsk(id)
	m.asksMoved = true
	log.For("core").Info("mod.ask_answered", "instance", inst.Title, "ask", id)
	m.reply(req, Reply{ID: m.idOf(inst)})
	if follow != "" {
		m.enqueue(inst, &modMessage{text: follow, asUser: true, origin: OriginWeb})
	}
	return payload, nil, nil
}

// ModAskClosed closes launch's ask id, which Claude's own dialog answered
// (how: "elsewhere" for a question, "settled" for a permission whose call
// settled). An answer still waiting for the mod is refused. An unknown ask
// closes quietly.
func (m *Model) ModAskClosed(launch, poller, id, how string) error {
	inst, l, err := m.askLink(launch, poller)
	if err != nil {
		return err
	}
	if a := l.ask(id); a != nil {
		log.For("core").Info("mod.ask_closed", "instance", inst.Title, "ask", id, "how", how)
		m.closeAsk(inst, l, a, "it was answered in Claude first")
	}
	return nil
}

// closeAsk closes a on l, refusing an answer still waiting for the mod with
// why.
func (m *Model) closeAsk(inst *session.Instance, l *modLink, a *modAsk, why string) {
	if a.answer != nil {
		m.refuse(a.req, m.idOf(inst), fmt.Errorf("%s %s: %s", opAnswer, inst.Title, why))
	}
	l.closeAsk(a.view.ID)
	m.asksMoved = true
}

// dropAsks forgets every ask l holds as its launch ends or its mod goes:
// a held poll ends, and an answer still waiting is refused with why. No
// tombstones: a mod that comes back posts what it still holds.
func (m *Model) dropAsks(inst *session.Instance, l *modLink, why string) {
	for _, a := range l.asks {
		if a.answer != nil {
			m.refuse(a.req, m.idOf(inst), fmt.Errorf("%s %s: %s before Claude took the answer", opAnswer, inst.Title, why))
		}
		close(a.wake)
	}
	if len(l.asks) > 0 {
		m.asksMoved = true
	}
	l.asks = nil
}

// asksSeeStatus closes inst's main-loop permission asks when its reported
// status leaves Prompting after the model saw it Prompting with the ask
// open: Claude's roster turns busy within a second of a terminal answer,
// and no hook fires for one. A subagent's ask waits for its call to settle,
// since the roster reads busy while a background agent's prompt waits.
func (m *Model) asksSeeStatus(inst *session.Instance, prompting bool) {
	l := m.mods[inst]
	if l == nil {
		return
	}
	for _, a := range slices.Clone(l.asks) {
		if a.view.Kind != AskPermission || a.view.AgentID != "" {
			continue
		}
		switch {
		case prompting:
			a.seenPrompting = true
		case a.seenPrompting:
			log.For("core").Info("mod.ask_closed", "instance", inst.Title, "ask", a.view.ID, "how", "status")
			m.closeAsk(inst, l, a, "it was answered in Claude first")
		}
	}
}

// askTick withdraws every answer no poll took within askTakeTimeout: a late
// poll must not deliver an answer the phone was told failed.
func (m *Model) askTick(now time.Time) {
	for inst, l := range m.mods {
		for _, a := range l.asks {
			if a.answer == nil || now.Sub(a.at) < askTakeTimeout {
				continue
			}
			m.refuse(a.req, m.idOf(inst), fmt.Errorf("%s %s: the session did not take the answer within %s; answer in Claude", opAnswer, inst.Title, askTakeTimeout))
			a.answer, a.req, a.follow = nil, 0, ""
			m.asksMoved = true
		}
	}
}

// asksOf is inst's open asks as views, oldest first; nil for none.
func (m *Model) asksOf(inst *session.Instance) []AskView {
	l := m.mods[inst]
	if l == nil || len(l.asks) == 0 {
		return nil
	}
	out := make([]AskView, len(l.asks))
	for i, a := range l.asks {
		out[i] = a.view
		out[i].Answering = a.answer != nil
	}
	return cloneAsks(out)
}

// cloneAsks deep-copies asks: every slice of every view.
func cloneAsks(asks []AskView) []AskView {
	if asks == nil {
		return nil
	}
	out := slices.Clone(asks)
	for i := range out {
		out[i].Questions = slices.Clone(out[i].Questions)
		for j := range out[i].Questions {
			out[i].Questions[j].Options = slices.Clone(out[i].Questions[j].Options)
		}
		out[i].Suggestions = slices.Clone(out[i].Suggestions)
		for j := range out[i].Suggestions {
			out[i].Suggestions[j].Rules = slices.Clone(out[i].Suggestions[j].Rules)
			out[i].Suggestions[j].Directories = slices.Clone(out[i].Suggestions[j].Directories)
		}
	}
	return out
}

// AskLine is an ask in one line, as the phone's fleet row shows it: a
// question's text, or a permission's tool and summary.
func AskLine(a AskView) string {
	if a.Kind == AskQuestion && len(a.Questions) > 0 {
		return a.Questions[0].Question
	}
	if a.Summary == "" {
		return a.Tool
	}
	return strings.TrimSpace(a.Tool + ": " + a.Summary)
}
```

(`opAnswer` is Package B's; until then declare it in A so the package builds: add `opAnswer requestOp = "answer"` to the `requestOp` block in `core/requests.go` now, with its precondition case in B.)

- [ ] **A5: wire the link, the views and the status.**
  - `core/mod.go`, `modLink`: add after `since`:

```go
	// asks are the open asks the mod holds, oldest first (core/asks.go);
	// closedAsks remembers the closed ones, oldest first in closedOrder, so
	// a re-post never reopens one.
	asks        []*modAsk
	closedAsks  map[string]bool
	closedOrder []string
```

  - `endLink`: inside its `causedBy`, after `m.endTurns(inst, l, why)`, add `m.dropAsks(inst, l, why)`. `modGone`: inside its `causedBy`, after `m.endTurns(inst, l, took)`, add `m.dropAsks(inst, l, took)`.
  - `modTick`: first line `m.askTick(now)`.
  - `ModHost`: add

```go
	// ModAsk holds an ask the mod posts (ErrModBadAsk for one it can't
	// hold, ErrModAskClosed for one that closed); ModAnswer is its answer,
	// or the channel a held poll waits on (nil: the daemon is stopping;
	// ErrModUnknownAsk, ErrModAskClosed); ModAskClosed closes one Claude's
	// own dialog answered. poller is as ModInbox's.
	ModAsk(launch, poller string, ask ModAsk) error
	ModAnswer(launch, poller, ask string) (json.RawMessage, <-chan struct{}, error)
	ModAskClosed(launch, poller, ask, how string) error
```
    (import `encoding/json` in `core/mod.go`).
  - `core/model.go`, beside `noWake`: `asksMoved bool // an ask opened, closed or was answered: modCall wakes the client`.
  - `core/loop_mod.go`: add the three forwarders (`ModAsk`, `ModAnswer`, `ModAskClosed`) in `modCall`, as `ModInbox`'s; and in `modCall` wake on asks too:

```go
	l.do(func(m *Model) {
		f(m)
		if len(m.out.Events) > 0 || m.asksMoved {
			m.asksMoved = false
			l.signal()
		}
	})
```
  - `core/view.go`, `InstanceView`, after `WaitDetail`:

```go
	// Asks are the dialogs the session's Claude holds open that the phone
	// can answer (core/asks.go), oldest first; nil for none.
	Asks []AskView
```
  - `core/views.go`: `viewOf` sets `Asks: m.asksOf(inst),`; `cloneViews` adds `out[i].Asks = cloneAsks(out[i].Asks)` and its comment names Asks beside Subagents.
  - `core/claude_status.go`, `adoptClaudeStatus`: after the if/else, `m.asksSeeStatus(inst, ok && status == session.Prompting)`.

- [ ] **A6: modserver routes.** In `internal/modserver/server.go`: register

```go
	mux.HandleFunc(route("POST", "/asks"), s.asks)
	mux.HandleFunc(route("GET", "/answer"), s.answerPoll)
	mux.HandleFunc(route("POST", "/asks/close"), s.askClosed)
```
  with the bodies and handlers

```go
type askBody struct {
	Launch string `json:"launch"`
	Poller string `json:"poller"`
	core.ModAsk
}

type askClosedBody struct {
	Launch string `json:"launch"`
	Poller string `json:"poller"`
	Ask    string `json:"ask"`
	How    string `json:"how"`
}

func (s *Server) asks(w http.ResponseWriter, r *http.Request) {
	var b askBody
	if decode(w, r, &b) {
		answer(w, s.host.ModAsk(b.Launch, b.Poller, b.ModAsk))
	}
}

func (s *Server) askClosed(w http.ResponseWriter, r *http.Request) {
	var b askClosedBody
	if decode(w, r, &b) {
		answer(w, s.host.ModAskClosed(b.Launch, b.Poller, b.Ask, b.How))
	}
}

// answerPoll answers an ask's answer at once, or holds the poll until it may
// have come (204), the hold ends (204) or the daemon stops (503).
func (s *Server) answerPoll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	payload, wake, err := s.host.ModAnswer(q.Get("launch"), q.Get("poller"), q.Get("ask"))
	switch {
	case err != nil:
		answer(w, err)
	case payload != nil:
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	case wake == nil:
		http.Error(w, "the loom daemon is stopping", http.StatusServiceUnavailable)
	default:
		s.holdUntil(w, r, wake)
	}
}
```
  move the inbox's hold into `holdUntil(w, r, wake)` (the timer and the four-way select) and call it from both; extend `answer`'s switch with `ErrModBadAsk` → 400, `ErrModUnknownAsk` → 404, `ErrModAskClosed` → 410 (`http.StatusGone`). Update the package comment to name the three routes.

- [ ] **A7: modserver tests.** Give `fakeHost` an `asks []core.ModAsk`, `answers map[string]json.RawMessage`, `closes []string` and an ask wake, implementing the three methods for launch "L1" (`"old"` poller → `ErrModSuperseded`; ask `"bad"` → `ErrModBadAsk`; `"gone"` → `ErrModAskClosed`; unknown ask → `ErrModUnknownAsk`); `stoppedHost.ModAnswer` returns `nil, nil, nil`. Add:

```go
func TestAsks(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/asks"), `{"launch":"L1","poller":"p","ask":"q1","kind":"question","tool":"AskUserQuestion","input":{"questions":[]}}`))
	assert.Equal(t, "q1", h.asks[0].Ask)
	assert.JSONEq(t, `{"questions":[]}`, string(h.asks[0].Input))
	assert.Equal(t, http.StatusBadRequest, post(t, c, api("/asks"), `{"launch":"L1","ask":"bad"}`))
	assert.Equal(t, http.StatusGone, post(t, c, api("/asks"), `{"launch":"L1","ask":"gone"}`))
	assert.Equal(t, http.StatusNotFound, post(t, c, api("/asks"), `{"launch":"L9","ask":"q1"}`))
	assert.Equal(t, http.StatusConflict, post(t, c, api("/asks"), `{"launch":"L1","poller":"old","ask":"q1"}`))
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/asks/close"), `{"launch":"L1","ask":"q1","how":"elsewhere"}`))
	assert.Equal(t, []string{"q1 elsewhere"}, h.closes)
}

func TestAnswer_HandsOutAnAnswerAtOnce(t *testing.T) {
	h := &fakeHost{answers: map[string]json.RawMessage{"q1": json.RawMessage(`{"result":{"answers":{}}}`)}}
	_, c := listen(t, h, time.Hour)
	resp, err := c.Get("http://loom" + api("/answer?launch=L1&ask=q1"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"result":{"answers":{}}}`, string(body))
}

func TestAnswer_Statuses(t *testing.T) {
	_, c := listen(t, &fakeHost{}, time.Hour)
	assert.Equal(t, http.StatusNotFound, <-get(c, api("/answer?launch=L1&ask=nope")), "an unknown ask: the mod posts it again")
	assert.Equal(t, http.StatusGone, <-get(c, api("/answer?launch=L1&ask=gone")))
	assert.Equal(t, http.StatusConflict, <-get(c, api("/answer?launch=L1&poller=old&ask=q1")))
	assert.Equal(t, http.StatusNotFound, <-get(c, api("/answer?launch=L9&ask=q1")))
}

func TestAnswer_HoldsUntilWoken(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Hour)
	done := get(c, api("/answer?launch=L1&ask=q1"))
	require.Eventually(t, h.askPolled, 5*time.Second, 10*time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("answered %d before any wake", code)
	case <-time.After(100 * time.Millisecond):
	}
	h.mu.Lock()
	close(h.askWake)
	h.mu.Unlock()
	assert.Equal(t, http.StatusNoContent, <-done)
}

func TestAnswer_AStopEndsAHoldWith503(t *testing.T) {
	h := &fakeHost{}
	s, c := listen(t, h, time.Hour)
	done := get(c, api("/answer?launch=L1&ask=q1"))
	require.Eventually(t, h.askPolled, 5*time.Second, 10*time.Millisecond)
	s.Close()
	assert.Equal(t, http.StatusServiceUnavailable, <-done)
}

func TestAnswer_AStoppedModelIs503(t *testing.T) {
	_, c := listen(t, &stoppedHost{}, time.Hour)
	assert.Equal(t, http.StatusServiceUnavailable, <-get(c, api("/answer?launch=L1&ask=q1")))
}
```
  `queueHost` (realclaude_test.go) gets the three methods too, storing asks and serving answers the test sets (Package C uses them).

- [ ] **A8: run and fix to green:** `CGO_ENABLED=0 go test ./core ./internal/modserver ./session/hooks`. Then `CGO_ENABLED=0 go vet ./core ./internal/modserver` and gofmt.

- [ ] **A9: commit** `feat(core,modserver): hold the asks a loom mod posts, and serve their answers` with `core/asks.go core/asks_test.go core/mod.go core/loop_mod.go core/model.go core/view.go core/views.go core/claude_status.go core/requests.go session/hooks/event.go internal/modserver/server.go internal/modserver/server_test.go internal/modserver/realclaude_test.go`.

## Package B: `Core.Answer`

**Files:**
- Modify: `core/iface.go`, `core/loop_core.go`, `core/requests.go` (precondition), `core/asks.go` (Answer, composeAnswer, clarifyText), `core/asks_test.go`, `core/requests_test.go`
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Modify: `internal/web/api_test.go` (`recordingBackend` gains nothing: it embeds `*core.Loop`)

- [ ] **B1: failing tests** in `core/asks_test.go`:

```go
// answerTaken has the mod take the answer to launch L1's ask id and returns
// what it carries.
func answerTaken(t *testing.T, m *Model, id string) string {
	t.Helper()
	payload, _, err := m.ModAnswer("L1", "", id)
	require.NoError(t, err)
	require.NotNil(t, payload)
	return string(payload)
}

func TestAnswer_AQuestionComposesClaudesResult(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	_, wake, _ := m.ModAnswer("L1", "", "q1")

	m.Answer(m.idOf(inst), "q1", AskAnswer{Questions: map[string]QuestionAnswer{
		"Which color do you prefer?": {Labels: []string{"Blue"}, Note: "  greener the better "},
		"Which pets do you have?":    {Labels: []string{"Fish", "Cat"}, Other: "Hamster"},
	}}, 7)
	assert.Empty(t, replies(m.Drain()), "no reply until the mod takes it")
	assert.True(t, closed(wake), "the held poll ends")
	assert.True(t, asksOf(m, inst)[0].Answering)

	got := answerTaken(t, m, "q1")
	assert.JSONEq(t, `{"result":{"questions":`+questionsOf(t, questionAsk("q1"))+`,
		"answers":{"Which color do you prefer?":"Blue","Which pets do you have?":"Cat, Fish, Hamster"},
		"annotations":{"Which color do you prefer?":{"notes":"greener the better"}}}}`, got)
	assert.Equal(t, []Reply{{Req: 7, ID: m.idOf(inst)}}, replies(m.Drain()))
	assert.Empty(t, asksOf(m, inst))
}

func questionsOf(t *testing.T, a ModAsk) string {
	t.Helper()
	var in struct{ Questions json.RawMessage }
	require.NoError(t, json.Unmarshal(a.Input, &in))
	return string(in.Questions)
}

func TestAnswer_ChatDeniesWithClaudesClarifyText(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))

	m.Answer(m.idOf(inst), "q1", AskAnswer{Chat: true, ChatText: " the colors are wrong "}, 7)
	var got struct{ Deny string }
	require.NoError(t, json.Unmarshal([]byte(answerTaken(t, m, "q1")), &got))

	assert.Equal(t, "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\n"+
		"The user wants to clarify these questions.\n"+
		"    This means they may have additional information, context or questions for you.\n"+
		"    Take their response into account and then reformulate the questions if appropriate.\n"+
		"    Start by asking them what they would like to clarify.\n\n"+
		"    Questions asked:\n"+
		"- \"Which color do you prefer?\"\n  (No answer provided)\n"+
		"- \"Which pets do you have?\"\n  (No answer provided)", got.Deny)
	msgs, _, _ := m.ModInbox("L1", "")
	require.Len(t, msgs, 1, "the chat's words follow as the person's reply")
	assert.Equal(t, "the colors are wrong", msgs[0].Text)
	assert.True(t, msgs[0].AsUser)
}

func TestAnswer_APermissionComposesClaudesDecision(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	for _, tc := range []struct {
		name   string
		answer AskAnswer
		want   string
		follow string
	}{
		{"allow", AskAnswer{Allow: true}, `{"decision":{"behavior":"allow"}}`, ""},
		{"allow with a rule", AskAnswer{Allow: true, Suggestion: 2},
			`{"decision":{"behavior":"allow","updatedPermissions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test:*"}],"behavior":"allow","destination":"localSettings"}]}}`, ""},
		{"deny", AskAnswer{}, `{"decision":{"behavior":"deny","interrupt":true}}`, ""},
		{"deny with a message", AskAnswer{Message: " use third-dir "}, `{"decision":{"behavior":"deny","interrupt":true}}`, "use third-dir"},
	} {
		id := "p-" + tc.name
		require.NoError(t, m.ModAsk("L1", "", permissionAsk(id, "")))
		m.Answer(m.idOf(inst), id, tc.answer, 9)
		assert.JSONEq(t, tc.want, answerTaken(t, m, id), tc.name)
		msgs, _, _ := m.ModInbox("L1", "")
		if tc.follow == "" {
			assert.Empty(t, msgs, tc.name)
			continue
		}
		require.Len(t, msgs, 1, tc.name)
		assert.Equal(t, tc.follow, msgs[0].Text)
		require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	}
}

func TestAnswer_Refusals(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", questionAsk("q1")))
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	id := m.idOf(inst)
	both := func(color, pets QuestionAnswer) map[string]QuestionAnswer {
		return map[string]QuestionAnswer{"Which color do you prefer?": color, "Which pets do you have?": pets}
	}
	cat := QuestionAnswer{Labels: []string{"Cat"}}
	for _, tc := range []struct {
		name   string
		ask    string
		answer AskAnswer
		want   string
	}{
		{"an unknown ask", "zzz", AskAnswer{Allow: true}, "answer x: it asks nothing by that ID now"},
		{"a question left out", "q1", AskAnswer{Questions: map[string]QuestionAnswer{"Which pets do you have?": cat}}, `answer x: "Which color do you prefer?" has no answer`},
		{"an empty answer", "q1", AskAnswer{Questions: both(QuestionAnswer{}, cat)}, `answer x: "Which color do you prefer?" has no answer`},
		{"a label not offered", "q1", AskAnswer{Questions: both(QuestionAnswer{Labels: []string{"Green"}}, cat)}, `answer x: "Green" is not an option of "Which color do you prefer?"`},
		{"two answers to a single select", "q1", AskAnswer{Questions: both(QuestionAnswer{Labels: []string{"Red"}, Other: "Teal"}, cat)}, `answer x: "Which color do you prefer?" takes one answer`},
		{"a question not asked", "q1", AskAnswer{Questions: map[string]QuestionAnswer{"Which color do you prefer?": {Other: "x"}, "Which pets do you have?": cat, "Why?": {Other: "y"}}}, `answer x: it asks no question "Why?"`},
		{"chat and answers", "q1", AskAnswer{Chat: true, Questions: both(cat, cat)}, "answer x: chat about the questions or answer them, not both"},
		{"allow on a question", "q1", AskAnswer{Allow: true}, "answer x: a question takes answers or a chat, not an allow or a deny"},
		{"answers on a permission", "p1", AskAnswer{Questions: both(cat, cat)}, "answer x: a permission takes an allow or a deny"},
		{"a suggestion not offered", "p1", AskAnswer{Allow: true, Suggestion: 3}, "answer x: suggestion 3 was not offered"},
		{"a message on an allow", "p1", AskAnswer{Allow: true, Message: "x"}, "answer x: a message goes with a deny"},
		{"a suggestion on a deny", "p1", AskAnswer{Suggestion: 1}, "answer x: a suggestion goes with an allow"},
	} {
		m.Answer(id, tc.ask, tc.answer, 3)
		r := replies(m.Drain())
		require.Len(t, r, 1, tc.name)
		require.Error(t, r[0].Err, tc.name)
		assert.Equal(t, tc.want, r[0].Err.Error(), tc.name)
		assert.ErrorIs(t, r[0].Err, ErrRefused, tc.name)
	}
}

func TestAnswer_TheFirstAnswerWins(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)
	m.Answer(m.idOf(inst), "p1", AskAnswer{}, 4)
	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.Equal(t, ReqID(4), r[0].Req)
	assert.EqualError(t, r[0].Err, "answer x: it was already answered from another device")
	assert.JSONEq(t, `{"decision":{"behavior":"allow"}}`, answerTaken(t, m, "p1"))
}

func TestAnswer_ClaudeAnsweringFirstRefusesAWaitingAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	require.NoError(t, m.ModAskClosed("L1", "", "p1", "settled"))

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: it was answered in Claude first")
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 5)
	assert.EqualError(t, replies(m.Drain())[0].Err, "answer x: it was answered in Claude first")
}

func TestAnswer_AnAnswerNoPollTakesIsWithdrawn(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	m.modTick(time.Now().Add(askTakeTimeout - time.Second))
	assert.Empty(t, replies(m.Drain()))
	m.modTick(time.Now().Add(askTakeTimeout + time.Second))

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: the session did not take the answer within 30s; answer in Claude")
	payload, wake, err := m.ModAnswer("L1", "", "p1")
	require.NoError(t, err)
	assert.Nil(t, payload, "a late poll gets nothing")
	assert.NotNil(t, wake)
	assert.False(t, asksOf(m, inst)[0].Answering)
}

func TestAnswer_AnEndingLinkRefusesAWaitingAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModAsk("L1", "", permissionAsk("p1", "")))
	m.Answer(m.idOf(inst), "p1", AskAnswer{Allow: true}, 3)

	m.endLink(inst, m.linkFor(inst), "the session stopped")

	r := replies(m.Drain())
	require.Len(t, r, 1)
	assert.EqualError(t, r[0].Err, "answer x: the session stopped before Claude took the answer")
}
```
  and in `core/requests_test.go`'s `TestRequests_RefuseWhatTheTUIRefuses` table:

```go
		{"answer an unstarted session", func(f fixture) { f.m.Answer(f.id["new"], "a", AskAnswer{Allow: true}, 1) }, "answer new: the session has not started"},
		{"answer a paused session", func(f fixture) { f.m.Answer(f.id["paused"], "a", AskAnswer{Allow: true}, 1) }, "answer paused: the session is paused"},
```

- [ ] **B2: see them fail** (`m.Answer` undefined): `CGO_ENABLED=0 go test ./core -run 'TestAnswer_|TestRequests_' 2>&1 | head`.

- [ ] **B3: implement.** In `core/iface.go`, after `Send`:

```go
	// Answer answers a dialog the session's Claude holds open (an
	// InstanceView.Asks entry) from the phone; the Reply follows once the
	// session's loom mod took the answer.
	Answer(id InstanceID, ask string, answer AskAnswer, req ReqID)
```
  In `core/loop_core.go`, its forwarder as its neighbours'. In `core/requests.go`'s `precondition`, `case opSend, opAnswer:` (the same gate: started and not Paused). Append to `core/asks.go`:

```go
// AskAnswer is the phone's answer to an ask. A question ask takes Questions,
// each keyed by its question's text, or Chat (the person chose "Chat about
// this"), with ChatText their optional words. A permission ask takes Allow,
// with Suggestion the 1-based index of an offered rule to allow with it (0
// for none), or a deny (Allow false), with Message the person's optional
// words.
type AskAnswer struct {
	Questions  map[string]QuestionAnswer
	Chat       bool
	ChatText   string
	Allow      bool
	Suggestion int
	Message    string
}

// QuestionAnswer is one question's answer: the options chosen, an Other
// answer, and a note.
type QuestionAnswer struct {
	Labels []string
	Other  string
	Note   string
}

// Answer answers session id's open ask with the phone's answer: it checks
// the answer against the ask, composes what the mod hands Claude
// (composeAnswer) and wakes the ask's poll. The Reply comes when the mod
// takes it (ModAnswer); a refusal comes at once, and so does "answered in
// Claude first" for an ask that closed.
func (m *Model) Answer(id InstanceID, ask string, ans AskAnswer, req ReqID) {
	inst, _, ok := m.admit(opAnswer, id, req)
	if !ok {
		return
	}
	l := m.mods[inst]
	var a *modAsk
	if l != nil {
		a = l.ask(ask)
	}
	switch {
	case a == nil && l != nil && l.closedAsks[ask]:
		m.refuse(req, id, fmt.Errorf("%s %s: it was answered in Claude first", opAnswer, inst.Title))
		return
	case a == nil:
		m.refuse(req, id, fmt.Errorf("%s %s: it asks nothing by that ID now", opAnswer, inst.Title))
		return
	case a.answer != nil:
		m.refuse(req, id, fmt.Errorf("%s %s: it was already answered from another device", opAnswer, inst.Title))
		return
	}
	payload, follow, err := composeAnswer(a, ans)
	if err != nil {
		m.refuse(req, id, fmt.Errorf("%s %s: %w", opAnswer, inst.Title, err))
		return
	}
	a.answer, a.req, a.at, a.follow = payload, req, time.Now(), follow
	a.poke()
	m.asksMoved = true
	log.For("core").Info("mod.ask_answer", "instance", inst.Title, "ask", ask)
}

// composeAnswer is what the mod returns to Claude for ans, and the words to
// send after it as the person's reply ("" for none): a question's result
// (Claude's own shape: the questions as given, each answer the chosen
// labels in option order then Other, joined by ", ", each note under
// annotations) or a chat's deny (Claude's clarify text); a permission's
// decision (allow, with the one chosen suggestion as given, or a deny that
// ends the turn, as the terminal's "No" does).
func composeAnswer(a *modAsk, ans AskAnswer) (json.RawMessage, string, error) {
	switch a.view.Kind {
	case AskQuestion:
		return composeQuestion(a, ans)
	case AskPermission:
		return composePermission(a, ans)
	}
	return nil, "", fmt.Errorf("an ask of kind %q takes no answer", a.view.Kind)
}

func composeQuestion(a *modAsk, ans AskAnswer) (json.RawMessage, string, error) {
	if ans.Allow || ans.Suggestion != 0 || ans.Message != "" {
		return nil, "", errors.New("a question takes answers or a chat, not an allow or a deny")
	}
	if ans.Chat {
		if len(ans.Questions) > 0 {
			return nil, "", errors.New("chat about the questions or answer them, not both")
		}
		payload, err := json.Marshal(struct {
			Deny string `json:"deny"`
		}{clarifyText(a.view.Questions)})
		return payload, strings.TrimSpace(ans.ChatText), err
	}
	if ans.ChatText != "" {
		return nil, "", errors.New("a chat's words go with a chat")
	}
	asked := map[string]bool{}
	for _, q := range a.view.Questions {
		asked[q.Question] = true
	}
	for text := range ans.Questions {
		if !asked[text] {
			return nil, "", fmt.Errorf("it asks no question %q", text)
		}
	}
	answers := map[string]string{}
	notes := map[string]any{}
	for _, q := range a.view.Questions {
		qa := ans.Questions[q.Question]
		offered := map[string]bool{}
		for _, o := range q.Options {
			offered[o.Label] = true
		}
		chosen := map[string]bool{}
		for _, label := range qa.Labels {
			if !offered[label] {
				return nil, "", fmt.Errorf("%q is not an option of %q", label, q.Question)
			}
			chosen[label] = true
		}
		var parts []string
		for _, o := range q.Options {
			if chosen[o.Label] {
				parts = append(parts, o.Label)
			}
		}
		if other := strings.TrimSpace(qa.Other); other != "" {
			parts = append(parts, other)
		}
		switch {
		case len(parts) == 0:
			return nil, "", fmt.Errorf("%q has no answer", q.Question)
		case !q.MultiSelect && len(parts) > 1:
			return nil, "", fmt.Errorf("%q takes one answer", q.Question)
		}
		answers[q.Question] = strings.Join(parts, ", ")
		if note := strings.TrimSpace(qa.Note); note != "" {
			notes[q.Question] = map[string]string{"notes": note}
		}
	}
	payload, err := json.Marshal(struct {
		Result struct {
			Questions   json.RawMessage   `json:"questions"`
			Answers     map[string]string `json:"answers"`
			Annotations map[string]any    `json:"annotations"`
		} `json:"result"`
	}{Result: struct {
		Questions   json.RawMessage   `json:"questions"`
		Answers     map[string]string `json:"answers"`
		Annotations map[string]any    `json:"annotations"`
	}{a.questions, answers, notes}})
	return payload, "", err
}

func composePermission(a *modAsk, ans AskAnswer) (json.RawMessage, string, error) {
	if len(ans.Questions) > 0 || ans.Chat || ans.ChatText != "" {
		return nil, "", errors.New("a permission takes an allow or a deny")
	}
	type decision struct {
		Behavior           string            `json:"behavior"`
		UpdatedPermissions []json.RawMessage `json:"updatedPermissions,omitempty"`
		Interrupt          bool              `json:"interrupt,omitempty"`
	}
	d := decision{Behavior: "deny", Interrupt: true}
	follow := ""
	switch {
	case ans.Allow && ans.Message != "":
		return nil, "", errors.New("a message goes with a deny")
	case !ans.Allow && ans.Suggestion != 0:
		return nil, "", errors.New("a suggestion goes with an allow")
	case ans.Suggestion < 0 || ans.Suggestion > len(a.suggestions):
		return nil, "", fmt.Errorf("suggestion %d was not offered", ans.Suggestion)
	case ans.Allow:
		d = decision{Behavior: "allow"}
		if ans.Suggestion > 0 {
			d.UpdatedPermissions = []json.RawMessage{a.suggestions[ans.Suggestion-1]}
		}
	default:
		follow = strings.TrimSpace(ans.Message)
	}
	payload, err := json.Marshal(struct {
		Decision decision `json:"decision"`
	}{d})
	return payload, follow, err
}

// clarifyText is the error result Claude's own dialog gives the model when
// the person picks "Chat about this", verbatim (the spike's record), naming
// every question unanswered.
func clarifyText(qs []AskQuestion) string {
	var b strings.Builder
	b.WriteString("The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\n")
	b.WriteString("The user wants to clarify these questions.\n")
	b.WriteString("    This means they may have additional information, context or questions for you.\n")
	b.WriteString("    Take their response into account and then reformulate the questions if appropriate.\n")
	b.WriteString("    Start by asking them what they would like to clarify.\n\n")
	b.WriteString("    Questions asked:")
	for _, q := range qs {
		b.WriteString("\n- " + strconv.Quote(q.Question) + "\n  (No answer provided)")
	}
	return b.String()
}
```
  (`strconv` import; `strconv.Quote` matches Claude's `"…"` for plain questions — the spike's test strings have no quotes or escapes to differ on.)

- [ ] **B4: regenerate the wire:** `go generate ./core/rpc` and `go test ./core/rpc -run TestProtocolReference -update`; then `CGO_ENABLED=0 go test ./core/... ./internal/web ./app/...` until green (the app's fakes of `Core`, if any fail to compile, embed or forward the new method as their neighbours do).

- [ ] **B5: commit** `feat(core,rpc): Answer, a dialog answered from the phone` with `core/asks.go core/asks_test.go core/iface.go core/loop_core.go core/requests.go core/requests_test.go core/rpc/methods_gen.go docs/specs/protocol.md` (plus any fake that needed the method).

## Package C: the mod holds asks

**Files:**
- Modify: `session/claudemod/mod/hooks/register.ts`, `session/claudemod/realclaude_test.go` (the validator's hooks line), `internal/modserver/realclaude_test.go` (a new opt-in test)

- [ ] **C1: the module.** In `register.ts`, after `ensurePump`, add:

```ts
// Asks: the dialogs loom's daemon can answer from the phone (the spec,
// docs/superpowers/specs/2026-11-03-pwa-answer-asks-design.md). The module
// holds each ask while Claude's own dialog stays up, posts it, and polls for
// the daemon's answer; whichever answers first wins. It returns what the
// daemon hands it and decides nothing itself.
type Ask = {
  ask: string
  kind: 'question' | 'permission'
  agentId?: string
  agentType?: string
  toolUseId?: string
  tool: string
  input: unknown
  suggestions?: unknown[]
}
type AskAnswer = { result?: unknown; deny?: string; decision?: unknown }

// PERMISSION_SLEEP_MS bounds the backoff a permission hook may sleep: its
// $.clock waits count against the hook's 10 s budget once its next has
// settled. A question's next holds the clock, so its hook may wait longer.
const PERMISSION_SLEEP_MS = 6000

// A call in flight, by tool and canonical input: a permission request names
// no tool_use_id, so its ask pairs with the oldest unpaired call of the same
// tool and input, and closes when that call settles (the person answered in
// Claude: next.signal never aborts for that).
type Call = { id: string; paired: boolean; settled: Promise<void>; settle: () => void }
const inFlight = new Map<string, Call[]>()

function canonical(v: unknown): string {
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']'
  if (v !== null && typeof v === 'object') {
    const o = v as Record<string, unknown>
    return '{' + Object.keys(o).sort().map(k => JSON.stringify(k) + ':' + canonical(o[k])).join(',') + '}'
  }
  return JSON.stringify(v) ?? 'null'
}

function callKey(tool: string, input: unknown): string {
  return tool + '\u0000' + canonical(input)
}

function newAskID(): string {
  return Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2)
}

// ready makes sure of the pump and says whether this launch talks to loom.
async function ready($: EngineInterface): Promise<boolean> {
  await ensurePump($)
  return socket !== '' && launch !== ''
}

// waitAnswer posts the ask and polls for its answer: the answer, or
// undefined once the daemon refuses or closes the ask, a newer module
// instance holds the launch, stop() says the hook settled, or its backoff
// would pass sleepMs in all.
async function waitAnswer($: EngineInterface, a: Ask, stop: () => boolean, sleepMs: number): Promise<AskAnswer | undefined> {
  let posted = false
  let delay = 0
  let slept = 0
  for (;;) {
    if (stop()) return undefined
    if (delay > 0) {
      if (slept + delay > sleepMs) return undefined
      slept += delay
      await $.clock.sleep(delay)
      if (stop()) return undefined
    }
    try {
      if (!posted) {
        const r = await call($, 'POST', '/asks', { launch, poller: POLLER, ...a })
        if (r.status === 400 || r.status === 409 || r.status === 410) return undefined
        if (r.status !== 204) {
          delay = backoff(delay)
          continue
        }
        posted = true
        delay = 0
      }
      const r = await call($, 'GET', `/answer?launch=${encodeURIComponent(launch)}&poller=${POLLER}&ask=${encodeURIComponent(a.ask)}`)
      if (r.status === 200) return JSON.parse(r.text) as AskAnswer
      if (r.status === 204) continue
      if (r.status === 404) {
        posted = false
        continue
      }
      if (r.status === 409 || r.status === 410) return undefined
      delay = backoff(delay)
    } catch (err) {
      $.ui.log(`loom: ask ${a.ask}: ${String(err)}`, { to: 'debug' })
      delay = backoff(delay)
    }
  }
}

// closeAsk tells the daemon Claude's own dialog answered the ask. Not
// awaited by the hooks: the answer is already Claude's.
async function closeAsk($: EngineInterface, ask: string, how: string) {
  try {
    await call($, 'POST', '/asks/close', { launch, poller: POLLER, ask, how })
  } catch (err) {
    $.ui.log(`loom: close ask ${ask}: ${String(err)}`, { to: 'debug' })
  }
}
```
  and in `register`, after the `turn.complete` hook:

```ts
  // Every tool call, so a permission ask finds its call. It passes the call
  // on unchanged.
  on('tool.call', async ($, e, next) => {
    const { tool, tool_use_id, agentId, consent, ...input } = e as any
    const key = callKey(tool, input)
    let settle = () => {}
    const c: Call = { id: tool_use_id, paired: false, settled: new Promise<void>(r => (settle = r)), settle: () => settle() }
    inFlight.set(key, [...(inFlight.get(key) ?? []), c])
    try {
      return await next(e)
    } finally {
      const rest = (inFlight.get(key) ?? []).filter(x => x !== c)
      if (rest.length > 0) inFlight.set(key, rest)
      else inFlight.delete(key)
      c.settle()
    }
  })

  // A question: Claude's dialog runs beneath (next) while the daemon may
  // answer; returning with next pending closes the dialog.
  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    if (!(await ready($))) return next(e)
    const { tool, tool_use_id, agentId, consent, ...input } = e as any
    const a: Ask = { ask: newAskID(), kind: 'question', agentId, toolUseId: tool_use_id, tool, input }
    let done = false
    const fromClaude = next(e).then(
      r => ({ r }),
      err => ({ err }),
    )
    const fromLoom = waitAnswer($, a, () => done, Infinity).then(ans => (ans === undefined ? fromClaude : { ans }))
    const first: any = await Promise.race([fromClaude, fromLoom])
    done = true
    if (first.ans !== undefined) {
      const ans = first.ans as AskAnswer
      return (ans.deny !== undefined ? { deny: ans.deny } : { result: ans.result }) as any
    }
    void closeAsk($, a.ask, 'elsewhere')
    if ('err' in first) throw first.err
    return first.r
  })

  // A permission: loom's status hook runs beneath (next), then Claude's
  // dialog runs while this hook holds. Every question raises one too, which
  // the question hook holds.
  on('classic.PermissionRequest', async ($, e, next) => {
    const r = await next(e)
    const req = e as any
    if ((r as any).decision !== undefined || req.tool_name === 'AskUserQuestion' || !(await ready($))) return r
    const c = (inFlight.get(callKey(req.tool_name, req.tool_input)) ?? []).find(x => !x.paired)
    if (c === undefined) return r // nothing would close it: leave it to Claude's dialog
    c.paired = true
    const a: Ask = {
      ask: newAskID(),
      kind: 'permission',
      agentId: req.agent_id,
      agentType: req.agent_type,
      toolUseId: c.id,
      tool: req.tool_name,
      input: req.tool_input,
      suggestions: req.permission_suggestions,
    }
    let done = false
    const ans = await Promise.race([waitAnswer($, a, () => done, PERMISSION_SLEEP_MS), c.settled.then(() => undefined)])
    done = true
    if (ans !== undefined && ans.decision !== undefined) return { ...r, decision: ans.decision } as any
    void closeAsk($, a.ask, 'settled')
    return r
  })
```
  Update the file's header comment to say it also carries asks.

- [ ] **C2: the validator test.** Run `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudemod -run TestRealClaude_ValidatesTheMod -v`; it fails on the hooks line. Update its `assert.Contains` to the line the validator prints (expected: `hooks: session.start, turn.start, turn.complete, tool.call, tool.call{tool=AskUserQuestion}, classic.PermissionRequest`) and rerun to green; the call list must stay within `allowedCalls` (no new `$` noun).

- [ ] **C3: the opt-in end-to-end test** `TestRealClaude_AnswersAsksThroughTheDaemon` in `internal/modserver/realclaude_test.go`, built on the same harness as `TestRealClaude_TheModCarriesAMessage` (private tmux server, haiku, `--permission-mode default`, trust dialog, hello), with `queueHost` holding asks and answers:
  1. Type a prompt asking for one `AskUserQuestion` with header Lunch and options Soup and Salad; wait for the question ask; set its answer to `{"result":{"questions":<as posted>,"answers":{"<q>":"Salad"},"annotations":{}}}`; wait for the turn's `complete` event (the prompt asks Claude to repeat the answer) and require it to contain `Salad`.
  2. Type `Run this exact bash command: mkdir made-by-loom. Then reply DONE.`; wait for the permission ask (tool Bash, a toolUseId); set `{"decision":{"behavior":"allow"}}`; require `made-by-loom` to exist.
  3. Type `Run this exact bash command: mkdir made-at-desk. Then reply DONE.`; wait for the ask and the dialog on screen; press Enter; require a close for that ask with how `settled` within 30 s, and the directory to exist.
  4. Type a question prompt again; wait for the ask; press Enter (the first option) in the terminal; require a close with how `elsewhere`.
  It runs only with `LOOM_TEST_REAL_CLAUDE=1`, and its doc comment says what it spends.

- [ ] **C4: run it:** `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./internal/modserver -run TestRealClaude_AnswersAsksThroughTheDaemon -v -timeout 10m`. Then `TestRealClaude_TheModCarriesAMessage` again (the new hooks must not disturb the carry).

- [ ] **C5: commit** `feat(claudemod): hold Claude's questions and permission prompts for the daemon to answer` with `session/claudemod/mod/hooks/register.ts session/claudemod/realclaude_test.go internal/modserver/realclaude_test.go`.

## Package D: the phone

**Files:**
- Create: `internal/web/asks.go`, `internal/web/asks_test.go`, `internal/web/static/js/asks.mjs`
- Modify: `internal/web/fleet.go` (`sessionJSON.Asks`, the wait detail), `internal/web/api.go` (the route), `internal/web/static/js/session.mjs`, `internal/web/static/app.css`, `internal/web/README.md`

- [ ] **D1: failing projection and route tests** in `internal/web/asks_test.go`:

```go
package web

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
)

func TestAskOf_AQuestion(t *testing.T) {
	got := askOf(core.AskView{ID: "q1", Kind: core.AskQuestion, AgentType: "general-purpose", Questions: []core.AskQuestion{
		{Question: "Which?", Header: "Pick", MultiSelect: true, Options: []core.AskOption{{Label: "A", Description: "a", Preview: "pre"}}},
	}})
	assert.Equal(t, askJSON{ID: "q1", Kind: "question", Agent: "general-purpose", Questions: []askQuestionJSON{
		{Question: "Which?", Header: "Pick", Multi: true, Options: []askOptionJSON{{Label: "A", Description: "a", Preview: "pre"}}},
	}}, got)
}

func TestInputBlocks_PerTool(t *testing.T) {
	for _, tc := range []struct {
		tool, input string
		want        []blockJSON
	}{
		{"Bash", `{"command":"ls -la","description":"List files"}`, []blockJSON{{Label: "command", Text: "ls -la", Mono: true}, {Label: "description", Text: "List files"}}},
		{"Edit", `{"file_path":"/a.go","old_string":"x","new_string":"y"}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "replace", Text: "x", Mono: true}, {Label: "with", Text: "y", Mono: true}}},
		{"MultiEdit", `{"file_path":"/a.go","edits":[{"old_string":"x","new_string":"y"}]}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "replace", Text: "x", Mono: true}, {Label: "with", Text: "y", Mono: true}}},
		{"Write", `{"file_path":"/a.go","content":"package a"}`, []blockJSON{{Label: "file", Text: "/a.go", Mono: true}, {Label: "content", Text: "package a", Mono: true}}},
		{"WebFetch", `{"url":"https://x.dev","prompt":"read it"}`, []blockJSON{{Label: "url", Text: "https://x.dev", Mono: true}, {Label: "prompt", Text: "read it"}}},
		{"mcp__gh__issue", `{"b":1,"a":"x"}`, []blockJSON{{Label: "input", Text: "{\n  \"a\": \"x\",\n  \"b\": 1\n}", Mono: true}}},
		{"Bash", `{"command":7}`, []blockJSON{{Label: "input", Text: "{\n  \"command\": 7\n}", Mono: true}}},
	} {
		assert.Equal(t, tc.want, inputBlocks(tc.tool, tc.input), tc.tool+" "+tc.input)
	}
}

func TestInputBlocks_CapsALongText(t *testing.T) {
	long := make([]byte, maxBlockRunes+10)
	for i := range long {
		long[i] = 'x'
	}
	b := inputBlocks("Bash", `{"command":"`+string(long)+`"}`)
	assert.Equal(t, maxBlockRunes, len([]rune(b[0].Text)))
	assert.True(t, b[0].Cut)
}

func TestSuggestionOf_NamesWhatAndHowLong(t *testing.T) {
	for _, tc := range []struct {
		s    core.AskSuggestion
		want suggestionJSON
	}{
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "localSettings", Rules: []core.AskRule{{ToolName: "Bash", RuleContent: "npm test:*"}}},
			suggestionJSON{Label: "Yes, and don't ask again for Bash(npm test:*) in this project, for you", Persists: true}},
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "projectSettings", Rules: []core.AskRule{{ToolName: "Read"}}},
			suggestionJSON{Label: "Yes, and don't ask again for Read in this project", Persists: true}},
		{core.AskSuggestion{Type: "addRules", Behavior: "allow", Destination: "userSettings", Rules: []core.AskRule{{ToolName: "WebFetch", RuleContent: "domain:x.dev"}}},
			suggestionJSON{Label: "Yes, and don't ask again for WebFetch(domain:x.dev) in every project", Persists: true}},
		{core.AskSuggestion{Type: "setMode", Mode: "acceptEdits", Destination: "session"},
			suggestionJSON{Label: "Yes, and switch to accept edits for this session"}},
		{core.AskSuggestion{Type: "addDirectories", Directories: []string{"/w"}, Destination: "session"},
			suggestionJSON{Label: "Yes, and allow access to /w for this session"}},
		{core.AskSuggestion{Type: "brandNew", Destination: "session"},
			suggestionJSON{Label: "Yes, and apply Claude's brandNew suggestion for this session"}},
	} {
		assert.Equal(t, tc.want, suggestionOf(tc.s))
	}
}

func TestSessionOf_TheFirstAskIsTheWaitDetail(t *testing.T) {
	v := core.InstanceView{WaitDetail: "ls", Asks: []core.AskView{
		{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "mkdir x"},
		{ID: "q1", Kind: core.AskQuestion, Questions: []core.AskQuestion{{Question: "Which?"}}},
	}}
	s := sessionOf(1, v)
	assert.Equal(t, "Bash: mkdir x (+1 more)", s.Wait.Detail)
	assert.Len(t, s.Asks, 2)
	assert.NotNil(t, sessionOf(1, core.InstanceView{}).Asks, "an empty list, never null")
}
```
  and the route tests in `internal/web/api_test.go`'s style (an `apiFixture` daemon whose instance is started on a Claude launch with a mod is heavy; the route is a thin `doAction`, so test it through a backend that records the call): a body with `answers` reaches `Core.Answer` keyed by question text; an unknown field is 400; an `ask` over 128 bytes is 400; a model refusal comes back 409 with its words. Add to `recordingBackend` an `answered []core.AskAnswer` and an `Answer` override that records and refuses with `core.Reply{Req: req, ID: id, Err: errors.New("answer x: it asks nothing by that ID now")}` through the loop the fixture uses, matching how `Send` is recorded today.

- [ ] **D2: see them fail:** `CGO_ENABLED=0 go test ./internal/web -run 'TestAskOf|TestInputBlocks|TestSuggestionOf|TestSessionOf_TheFirstAsk|TestAnswer' 2>&1 | head`.

- [ ] **D3: write `internal/web/asks.go`:**

```go
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/core"
)

// The asks a waiting session holds (core.AskView) as the app reads them, and
// the route that answers one. The app draws what this decides: the labels of
// a permission's suggestions and the blocks of its input are words written
// here, once.

const (
	// maxBlockRunes caps one block of a permission's input.
	maxBlockRunes = 4000
	// maxAskPath bounds an ask's ID in the path, maxAnswers and maxLabels an
	// answer's questions and each one's labels.
	maxAskPath = 128
	maxAnswers = 8
	maxLabels  = 8
)

type askJSON struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Agent       string            `json:"agent"`
	Opened      string            `json:"opened"`
	Answering   bool              `json:"answering"`
	Questions   []askQuestionJSON `json:"questions,omitempty"`
	Tool        string            `json:"tool,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Input       []blockJSON       `json:"input,omitempty"`
	Suggestions []suggestionJSON  `json:"suggestions,omitempty"`
}

type askQuestionJSON struct {
	Question string          `json:"question"`
	Header   string          `json:"header"`
	Multi    bool            `json:"multi"`
	Options  []askOptionJSON `json:"options"`
}

type askOptionJSON struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview,omitempty"`
}

// blockJSON is one labelled part of a permission's input: Mono for code,
// Cut when it was cut to maxBlockRunes.
type blockJSON struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Mono  bool   `json:"mono,omitempty"`
	Cut   bool   `json:"cut,omitempty"`
}

// suggestionJSON is one rule Claude suggests allowing with a permission:
// Persists when it is saved to a settings file, outliving the session.
type suggestionJSON struct {
	Label    string `json:"label"`
	Persists bool   `json:"persists,omitempty"`
}

func askOf(a core.AskView) askJSON {
	out := askJSON{ID: a.ID, Kind: a.Kind, Agent: a.AgentType, Answering: a.Answering, Tool: a.Tool, Summary: a.Summary}
	if a.AgentID != "" && out.Agent == "" {
		out.Agent = "a subagent"
	}
	if !a.Opened.IsZero() {
		out.Opened = a.Opened.UTC().Format(time.RFC3339)
	}
	for _, q := range a.Questions {
		jq := askQuestionJSON{Question: q.Question, Header: q.Header, Multi: q.MultiSelect, Options: []askOptionJSON{}}
		for _, o := range q.Options {
			jq.Options = append(jq.Options, askOptionJSON{Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		out.Questions = append(out.Questions, jq)
	}
	if a.Kind == core.AskPermission {
		out.Input = inputBlocks(a.Tool, a.InputJSON)
		for _, s := range a.Suggestions {
			out.Suggestions = append(out.Suggestions, suggestionOf(s))
		}
	}
	return out
}

// block is a capped block.
func block(label, text string, mono bool) blockJSON {
	b := blockJSON{Label: label, Text: text, Mono: mono}
	if utf8.RuneCountInString(text) > maxBlockRunes {
		b.Text, b.Cut = string([]rune(text)[:maxBlockRunes]), true
	}
	return b
}

// inputBlocks is a permission's input as the app shows it: the fields that
// say what the tool will do for Bash, Edit, MultiEdit, Write and WebFetch,
// and any other tool's input (or one whose fields are not what they should
// be) as indented JSON.
func inputBlocks(tool, input string) []blockJSON {
	var f struct {
		Command     *string `json:"command"`
		Description string  `json:"description"`
		FilePath    *string `json:"file_path"`
		OldString   string  `json:"old_string"`
		NewString   string  `json:"new_string"`
		Content     *string `json:"content"`
		Edits       []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
		URL    *string `json:"url"`
		Prompt string  `json:"prompt"`
	}
	if json.Unmarshal([]byte(input), &f) == nil {
		switch {
		case tool == "Bash" && f.Command != nil:
			out := []blockJSON{block("command", *f.Command, true)}
			if f.Description != "" {
				out = append(out, block("description", f.Description, false))
			}
			return out
		case tool == "Edit" && f.FilePath != nil:
			return []blockJSON{block("file", *f.FilePath, true), block("replace", f.OldString, true), block("with", f.NewString, true)}
		case tool == "MultiEdit" && f.FilePath != nil:
			out := []blockJSON{block("file", *f.FilePath, true)}
			for _, e := range f.Edits {
				out = append(out, block("replace", e.OldString, true), block("with", e.NewString, true))
			}
			return out
		case tool == "Write" && f.FilePath != nil && f.Content != nil:
			return []blockJSON{block("file", *f.FilePath, true), block("content", *f.Content, true)}
		case tool == "WebFetch" && f.URL != nil:
			out := []blockJSON{block("url", *f.URL, true)}
			if f.Prompt != "" {
				out = append(out, block("prompt", f.Prompt, false))
			}
			return out
		}
	}
	return []blockJSON{block("input", indentJSON(input), true)}
}

// indentJSON is s indented, with object keys sorted; s itself when it is no
// JSON.
func indentJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return s
	}
	return strings.TrimRight(b.String(), "\n")
}

// suggestionOf is a suggestion's button label, naming what it does and for
// how long, and whether it persists past the session.
func suggestionOf(s core.AskSuggestion) suggestionJSON {
	var what string
	switch s.Type {
	case "addRules", "replaceRules":
		verb := "don't ask again for"
		switch s.Behavior {
		case "deny":
			verb = "always refuse"
		case "ask":
			verb = "always ask for"
		}
		what = verb + " " + rulesText(s.Rules)
	case "removeRules":
		what = "forget the rules for " + rulesText(s.Rules)
	case "setMode":
		what = "switch to " + modeName(s.Mode)
	case "addDirectories":
		what = "allow access to " + strings.Join(s.Directories, ", ")
	case "removeDirectories":
		what = "remove access to " + strings.Join(s.Directories, ", ")
	default:
		what = "apply Claude's " + s.Type + " suggestion"
	}
	scope, persists := scopeOf(s.Destination)
	return suggestionJSON{Label: "Yes, and " + what + scope, Persists: persists}
}

func rulesText(rules []core.AskRule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.RuleContent == "" {
			parts = append(parts, r.ToolName)
			continue
		}
		parts = append(parts, r.ToolName+"("+r.RuleContent+")")
	}
	return strings.Join(parts, ", ")
}

func modeName(mode string) string {
	switch mode {
	case "acceptEdits":
		return "accept edits"
	case "plan":
		return "plan mode"
	case "auto":
		return "auto mode"
	case "default":
		return "manual mode"
	case "dontAsk":
		return "don't ask mode"
	case "bypassPermissions":
		return "bypass permissions"
	}
	return mode
}

// scopeOf is how long a suggestion's change lasts, as words after its
// label, and whether it outlives the session (a settings file).
func scopeOf(destination string) (string, bool) {
	switch destination {
	case "localSettings":
		return " in this project, for you", true
	case "projectSettings":
		return " in this project", true
	case "userSettings":
		return " in every project", true
	}
	return " for this session", false
}

// askDetail is the fleet row's line for a session's asks: the first one's,
// and how many more.
func askDetail(asks []core.AskView) string {
	line := core.AskLine(asks[0])
	if len(asks) > 1 {
		line += fmt.Sprintf(" (+%d more)", len(asks)-1)
	}
	return line
}

// answerBody is POST /api/v1/sessions/{id}/asks/{ask}/answer.
type answerBody struct {
	Answers    map[string]questionAnswerBody `json:"answers"`
	Chat       bool                          `json:"chat"`
	ChatText   string                        `json:"chat_text"`
	Allow      bool                          `json:"allow"`
	Suggestion int                           `json:"suggestion"`
	Message    string                        `json:"message"`
}

type questionAnswerBody struct {
	Labels []string `json:"labels"`
	Other  string   `json:"other"`
	Note   string   `json:"note"`
}

// answerAsk answers one of a session's asks from the device. The model
// checks the answer against the ask (core.Answer) and replies once the
// session's mod took it; every refusal is 409 with its words.
func (l *Layer) answerAsk(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "answer"}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	ask := r.PathValue("ask")
	if ask == "" || len(ask) > maxAskPath {
		l.refuse(w, a, http.StatusBadRequest, fmt.Sprintf("ask: want an ID of 1 to %d bytes", maxAskPath))
		return
	}
	var body answerBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	if why := body.tooBig(); why != "" {
		l.refuse(w, a, http.StatusBadRequest, why)
		return
	}
	answer := core.AskAnswer{Chat: body.Chat, ChatText: body.ChatText, Allow: body.Allow, Suggestion: body.Suggestion, Message: body.Message}
	if len(body.Answers) > 0 {
		answer.Questions = make(map[string]core.QuestionAnswer, len(body.Answers))
		for q, qa := range body.Answers {
			answer.Questions[q] = core.QuestionAnswer{Labels: qa.Labels, Other: qa.Other, Note: qa.Note}
		}
	}
	l.doAction(w, r, a, func(req core.ReqID) {
		l.client.Answer(id, ask, answer, req)
	})
}

// tooBig says what of b is over the route's bounds, "" when nothing is.
func (b answerBody) tooBig() string {
	if len(b.Answers) > maxAnswers {
		return fmt.Sprintf("answers: want at most %d", maxAnswers)
	}
	texts := []string{b.ChatText, b.Message}
	for q, qa := range b.Answers {
		if len(qa.Labels) > maxLabels {
			return fmt.Sprintf("answers: want at most %d labels a question", maxLabels)
		}
		texts = append(texts, q, qa.Other, qa.Note)
		texts = append(texts, qa.Labels...)
	}
	for _, t := range texts {
		if len(t) > maxReplyBytes {
			return fmt.Sprintf("answers: want each text at most %d bytes", maxReplyBytes)
		}
	}
	return ""
}
```

- [ ] **D4: wire it.** `fleet.go`: `sessionJSON` gains `Asks []askJSON `json:"asks"`` (doc: the dialogs the phone can answer, oldest first); `sessionOf` sets `Asks: make([]askJSON, 0, len(v.Asks))`, appends `askOf` for each, and when `len(v.Asks) > 0` sets `s.Wait.Detail = askDetail(v.Asks)`. `api.go`: `m.Handle("POST /api/v1/sessions/{id}/asks/{ask}/answer", l.authed(l.answerAsk))`.

- [ ] **D5: the app.** Create `internal/web/static/js/asks.mjs`:

```js
// The asks a waiting session holds, each answered here or in Claude,
// whichever comes first: a question's options, Other and a note, or Chat
// about this; a permission's Allow, its suggested rules and Deny. The
// daemon checks every answer; a refusal comes back as a toast.
import { html, useState } from "../lib/preact-htm.mjs";
import { ConfirmSheet, utf8Len } from "./sheets.mjs";
import { MAX_REPLY } from "./reply.mjs";

// drafts are the unsent answers by ask id: a tab switch remounts the
// screen, and what was chosen stays. They last as long as the page.
const drafts = new Map();

function useDraft(id, initial) {
  const [draft, setShown] = useState(() => drafts.get(id) || initial());
  function set(next) {
    setShown(next);
    drafts.set(id, next);
  }
  return [draft, set];
}

function answerPath(ask) {
  return "asks/" + encodeURIComponent(ask.id) + "/answer";
}

// Asks lists a session's asks, oldest first.
export function Asks({ s, off, run }) {
  const asks = s.asks || [];
  if (asks.length === 0) return null;
  return html`<div class="sc-asks">
    ${asks.map((a) => html`<${AskCard} key=${a.id} ask=${a} off=${off} run=${run} />`)}
  </div>`;
}

function AskCard({ ask, off, run }) {
  const busy = off || ask.answering;
  return html`<div class="sc-ask">
    ${ask.agent ? html`<div class="sc-dim sc-small sc-ask-agent">asked by ${ask.agent}</div>` : null}
    ${ask.kind === "question"
      ? html`<${QuestionAsk} ask=${ask} off=${busy} run=${run} />`
      : html`<${PermissionAsk} ask=${ask} off=${busy} run=${run} />`}
    ${ask.answering ? html`<p class="sc-dim sc-small">Sending…</p>` : null}
  </div>`;
}

function emptyAnswers(ask) {
  const answers = {};
  for (const q of ask.questions) answers[q.question] = { labels: [], other: "", note: "" };
  return { answers, shown: {} };
}

function answered(a) {
  return a.labels.length > 0 || a.other.trim() !== "";
}

function QuestionAsk({ ask, off, run }) {
  const [draft, setDraft] = useDraft(ask.id, () => emptyAnswers(ask));
  const [sheet, setSheet] = useState(false);
  const [chat, setChat] = useState("");

  function update(q, change, shown) {
    const next = { ...draft.answers, [q.question]: { ...draft.answers[q.question], ...change } };
    setDraft({ answers: next, shown: shown === undefined ? draft.shown : { ...draft.shown, [q.question]: shown } });
  }

  function pick(q, o) {
    const a = draft.answers[q.question];
    if (!q.multi) {
      update(q, { labels: [o.label], other: "" }, o.label);
      return;
    }
    const on = a.labels.includes(o.label);
    update(q, { labels: on ? a.labels.filter((l) => l !== o.label) : a.labels.concat(o.label) }, on ? draft.shown[q.question] : o.label);
  }

  function other(q, text) {
    update(q, q.multi || text === "" ? { other: text } : { other: text, labels: [] });
  }

  const ready = ask.questions.every((q) => answered(draft.answers[q.question]));
  const tooLong = Object.values(draft.answers).some((a) => utf8Len(a.other) > MAX_REPLY || utf8Len(a.note) > MAX_REPLY);

  async function send() {
    const answers = {};
    for (const q of ask.questions) {
      const a = draft.answers[q.question];
      answers[q.question] = { labels: a.labels, other: a.other.trim(), note: a.note.trim() };
    }
    const r = await run(answerPath(ask), { answers });
    if (r.ok) drafts.delete(ask.id);
  }

  async function sendChat() {
    const r = await run(answerPath(ask), { chat: true, chat_text: chat.trim() });
    setSheet(false);
    if (r.ok) drafts.delete(ask.id);
  }

  return html`<form class="sc-form sc-question" onSubmit=${(e) => { e.preventDefault(); if (!off && ready && !tooLong) send(); }}>
    ${ask.questions.map((q) => {
      const a = draft.answers[q.question];
      const shown = q.options.find((o) => o.label === draft.shown[q.question] && o.preview);
      return html`<fieldset key=${q.question} class="sc-q" disabled=${off}>
        <legend><span class="sc-q-chip">${q.header}</span> ${q.question}</legend>
        ${q.options.map(
          (o) => html`<label key=${o.label} class="sc-opt">
            <input type=${q.multi ? "checkbox" : "radio"} name=${ask.id + "\u0000" + q.question}
              checked=${a.labels.includes(o.label)} onChange=${() => pick(q, o)} />
            <span><span class="sc-opt-label">${o.label}</span>${o.description ? html`<span class="sc-opt-desc">${o.description}</span>` : null}</span>
          </label>`,
        )}
        ${shown ? html`<pre class="sc-detail-box">${shown.preview}</pre>` : null}
        <input type="text" placeholder="Other…" aria-label=${"Other answer to " + q.question} value=${a.other} onInput=${(e) => other(q, e.target.value)} />
        <input type="text" placeholder="Note (optional)" aria-label=${"Note on " + q.question} value=${a.note} onInput=${(e) => update(q, { note: e.target.value })} />
      </fieldset>`;
    })}
    ${tooLong ? html`<div class="sc-small sc-err">Too long: an answer is at most 64 KiB.</div>` : null}
    <div class="sc-buttons">
      <button class="sc-btn sc-primary" type="submit" disabled=${off || !ready || tooLong}>Answer</button>
      <button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet(true)}>Chat about this</button>
    </div>
    ${sheet
      ? html`<${ConfirmSheet}
          title="Chat about this"
          confirm="Send"
          body=${html`<p>Claude puts the questions aside and asks what you want to clarify.</p>
            <textarea class="sc-quick-edit" rows="3" placeholder="What to clarify (optional)" value=${chat} onInput=${(e) => setChat(e.target.value)}></textarea>`}
          onConfirm=${sendChat}
          onCancel=${() => setSheet(false)}
        />`
      : null}
  </form>`;
}

function PermissionAsk({ ask, off, run }) {
  const [sheet, setSheet] = useState(null);
  const [message, setMessage] = useState("");

  async function answer(body) {
    await run(answerPath(ask), body);
    setSheet(null);
  }

  const rule = typeof sheet === "number" ? ask.suggestions[sheet - 1] : null;

  return html`<div class="sc-perm">
    <div class="sc-perm-tool">${ask.tool}</div>
    ${(ask.input || []).map(
      (b, i) => html`<div key=${i} class="sc-perm-block">
        <span class="sc-dim sc-small">${b.label}${b.cut ? " (cut)" : ""}</span>
        ${b.mono ? html`<pre class="sc-detail-box">${b.text}</pre>` : html`<p class="sc-perm-text">${b.text}</p>`}
      </div>`,
    )}
    <div class="sc-perm-actions">
      <button class="sc-btn sc-primary sc-block" type="button" disabled=${off} onClick=${() => answer({ allow: true })}>Allow</button>
      ${(ask.suggestions || []).map(
        (g, i) => html`<button key=${i} class="sc-btn sc-block" type="button" disabled=${off}
          onClick=${() => (g.persists ? setSheet(i + 1) : answer({ allow: true, suggestion: i + 1 }))}>${g.label}</button>`,
      )}
      <button class="sc-btn sc-block sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("deny")}>Deny…</button>
    </div>
    ${rule
      ? html`<${ConfirmSheet}
          title="Save this rule?"
          confirm="Allow and save"
          body=${html`<p>${rule.label}.</p><p>Claude writes it to a settings file, so it lasts past this session.</p>`}
          onConfirm=${() => answer({ allow: true, suggestion: sheet })}
          onCancel=${() => setSheet(null)}
        />`
      : null}
    ${sheet === "deny"
      ? html`<${ConfirmSheet}
          title="Deny this?"
          confirm="Deny"
          danger=${true}
          body=${html`<p>Claude stops this turn, as "No" does in the terminal.</p>
            <textarea class="sc-quick-edit" rows="3" placeholder="What Claude should do instead (optional)" value=${message} onInput=${(e) => setMessage(e.target.value)}></textarea>
            ${utf8Len(message) > MAX_REPLY ? html`<div class="sc-small sc-err">Too long: a message is at most 64 KiB.</div>` : null}`}
          onConfirm=${() => answer({ allow: false, message: message.trim() })}
          onCancel=${() => setSheet(null)}
        />`
      : null}
  </div>`;
}
```
  In `session.mjs`: import `{ Asks }` from `./asks.mjs`; in the needs-you box, after the reason, render `<${Asks} s=${s} off=${off} run=${run} />`, and show the `sc-detail-box` of `s.wait.detail` only when `(s.asks || []).length === 0`. In `app.css`, after `.sc-detail-box`:

```css
.sc-asks { display: flex; flex-direction: column; gap: 14px; margin: 12px 0; }
.sc-ask { display: flex; flex-direction: column; gap: 8px; padding-top: 10px; border-top: 1px solid var(--rule); }
.sc-q { display: flex; flex-direction: column; gap: 6px; margin: 0 0 10px; padding: 0; border: 0; min-width: 0; }
.sc-q > legend { padding: 0 0 4px; color: var(--title); }
.sc-q-chip { display: inline-block; margin-right: 6px; padding: 1px 6px; border: 1px solid var(--rule); border-radius: var(--radius); color: var(--dim); font-size: 11px; }
.sc-opt { display: flex; align-items: flex-start; gap: 10px; min-height: 44px; padding: 6px 0; }
.sc-opt input { width: 22px; height: 22px; margin: 0; flex: none; accent-color: var(--accent); }
.sc-opt-label { display: block; }
.sc-opt-desc { display: block; color: var(--dim); font-size: 12px; }
.sc-perm { display: flex; flex-direction: column; gap: 8px; }
.sc-perm-tool { color: var(--title); }
.sc-perm-text { margin: 4px 0 0; overflow-wrap: anywhere; }
.sc-perm-actions { display: flex; flex-direction: column; gap: 8px; margin-top: 4px; }
```
  and add `.sc-question input[type="text"]` to the `.sc-form input[type="text"]` rule's selectors only if the form class does not already style it (it does: `.sc-question` carries `sc-form`).

- [ ] **D6: green and look.** `CGO_ENABLED=0 go test ./internal/web`. Render the session screen with one question ask and one permission ask in headless Chromium against a canned server, as `docs/claude/web-layer.md` describes (fake snapshot carrying `asks`), at phone width; check the radio/checkbox rows, the preview, the suggestion buttons, both sheets, and "Sending…".

- [ ] **D7: README.** `internal/web/README.md`: the Files table gains `asks.go` (the asks' projection: `askOf`, `inputBlocks`, `suggestionOf`; the answer route `answerAsk`), and the routes list gains `POST /api/v1/sessions/{id}/asks/{ask}/answer`; the app's module list gains `asks.mjs`.

- [ ] **D8: commit** `feat(web): answer Claude's questions and permission prompts from the phone` with `internal/web/asks.go internal/web/asks_test.go internal/web/api.go internal/web/api_test.go internal/web/fleet.go internal/web/README.md internal/web/static/js/asks.mjs internal/web/static/js/session.mjs internal/web/static/app.css core/asks.go core/asks_test.go`.

## Package E: docs and the final gates

- [ ] **E1: rules.** Within the budgets (`go run ./tools/claudemd -v`; `core/CLAUDE.md` has under 100 bytes of headroom, so its rule goes to a guide):
  - `session/claudemod/CLAUDE.md`: the module's description names the ask hooks; a rule: **Return only what the daemon hands for an ask, pair a permission ask with its call or leave it to Claude, and skip the `PermissionRequest` every question raises.** Why: the module stays a carrier; an unpaired ask would never close (`next.signal` never aborts on a person's answer); a question's permission request would post every question twice. **Convention**, checked by the opt-in `TestRealClaude_AnswersAsksThroughTheDaemon`.
  - `internal/modserver/CLAUDE.md`: the package line names the three ask routes; the 404/409 rule gains 410 for a closed ask and 400 for one the model can't hold. **Enforced** by `TestAsks`, `TestAnswer_Statuses`.
  - `docs/claude/web-layer.md`: a short section on asks (the model's `seenPrompting` close, a Reply only once the mod took the answer, a refusal always 409) and the route; `docs/claude/INDEX.md`'s line for it names asks.
  - `docs/ARCHITECTURE.md`: where the mod section lists its routes, add the ask routes and `Core.Answer`.
- [ ] **E2: the gates.** `CGO_ENABLED=0 go test ./...`; `CC=clang CGO_ENABLED=1 go test -race ./core ./internal/modserver ./internal/web`; `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`; `CGO_ENABLED=0 go vet ./...`; `go run ./tools/claudemd`; `GOOS=windows CGO_ENABLED=0 go build ./...`; golangci-lint skipped (dev shell's v2), said so.
- [ ] **E3: commit** `docs: answering asks from the phone` with the docs touched.
- [ ] **E4: the iPhone checklist** (the owner's, after deploy): pause and resume a session so it launches with the new mod; trigger a question, answer it from the phone (options, Other, a note) and check Claude's reply quotes it; trigger a Bash prompt, allow it; another, deny with a message; answer one in the terminal and watch the phone drop it; a background subagent's prompt, allowed from the phone.
