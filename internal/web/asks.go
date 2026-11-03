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
	if a.Kind == core.AskKindPermission {
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
