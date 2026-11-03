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
	AskKindQuestion   = "question"
	AskKindPermission = "permission"
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
	case AskKindQuestion:
		return out, parseQuestions(out, a.Input)
	case AskKindPermission:
		return out, parsePermission(out, a)
	}
	return nil, askBad("kind: want %q or %q, not %q", AskKindQuestion, AskKindPermission, a.Kind)
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
		if a.view.Kind != AskKindPermission || a.view.AgentID != "" {
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
	if a.Kind == AskKindQuestion && len(a.Questions) > 0 {
		return a.Questions[0].Question
	}
	if a.Summary == "" {
		return a.Tool
	}
	return strings.TrimSpace(a.Tool + ": " + a.Summary)
}
