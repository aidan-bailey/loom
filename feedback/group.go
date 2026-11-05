package feedback

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// SystemPrompt is the grouper's system prompt.
//
//go:embed prompt.md
var SystemPrompt string

// Request limits: each offered note's text is cut to RequestText bytes,
// and a thread offers its last RequestNotes notes.
const (
	RequestText  = 300
	RequestNotes = 20
)

// RequestNote is a note as the grouper sees it.
type RequestNote struct {
	Kind  Kind   `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// RequestThread is an offered thread.
type RequestThread struct {
	ID    ThreadID      `json:"id"`
	Title string        `json:"title"`
	Kind  Kind          `json:"kind"`
	Notes []RequestNote `json:"notes"`
}

// Request is one grouping call's input, sent as JSON on stdin.
type Request struct {
	Note    RequestNote     `json:"note"`
	Threads []RequestThread `json:"threads"`
}

// BuildRequest is the request for note n, with the IDs of the threads it
// offers (every thread not closed).
func BuildRequest(in Inbox, n Note) (Request, []ThreadID) {
	req := Request{Note: RequestNote{Kind: n.Kind, Title: n.Title, Text: n.Text}, Threads: []RequestThread{}}
	var ids []ThreadID
	for _, t := range in.Offered() {
		rt := RequestThread{ID: t.ID, Title: t.Title, Kind: t.Kind, Notes: []RequestNote{}}
		for _, id := range t.Notes[max(0, len(t.Notes)-RequestNotes):] {
			if m, ok := in.Note(id); ok {
				rt.Notes = append(rt.Notes, RequestNote{Kind: m.Kind, Title: m.Title, Text: cutText(m.Text, RequestText)})
			}
		}
		req.Threads = append(req.Threads, rt)
		ids = append(ids, t.ID)
	}
	return req, ids
}

// Schema is the answer's JSON schema.
const Schema = `{"type":"object","properties":{"thread":{"type":"integer","minimum":0},"title":{"type":"string","maxLength":120},"why":{"type":"string","maxLength":300}},"required":["thread","title","why"],"additionalProperties":false}`

// Answer is the grouper's answer: a thread to attach to, or 0 and a title
// for a new one.
type Answer struct {
	Thread ThreadID `json:"thread"`
	Title  string   `json:"title"`
	Why    string   `json:"why"`
}

// ParseAnswer checks raw: a thread ID of 0 or more, and a title that,
// trimmed, is at most MaxTitle characters on one line. A bad answer is an
// error, which the caller counts as a failed call.
func ParseAnswer(raw json.RawMessage) (Answer, error) {
	var a Answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return Answer{}, fmt.Errorf("the grouper's answer: %w", err)
	}
	a.Title, a.Why = strings.TrimSpace(a.Title), strings.TrimSpace(a.Why)
	switch {
	case a.Thread < 0:
		return Answer{}, fmt.Errorf("the grouper's answer names thread %d", a.Thread)
	case strings.ContainsAny(a.Title, "\r\n") || utf8.RuneCountInString(a.Title) > MaxTitle:
		return Answer{}, fmt.Errorf("the grouper's title is not one line of at most %d characters", MaxTitle)
	}
	return a, nil
}

// Apply files note n by answer a: into a.Thread when the request offered it
// and it is not closed now, else into a new thread titled a.Title (the
// note's own title when empty). It returns the thread.
func Apply(in *Inbox, n NoteID, a Answer, offered []ThreadID) (ThreadID, error) {
	if _, ok := in.Note(n); !ok {
		return 0, errors.New("the note is gone")
	}
	if t, ok := in.Thread(a.Thread); ok && t.Status != Closed && slices.Contains(offered, a.Thread) {
		return a.Thread, in.Attach(n, a.Thread)
	}
	return in.OpenThread(n, a.Title)
}

// Resolve is the grouper's settings block, with the advisor's defaults and
// bounds under its own key.
func Resolve(s *config.AgentSettings) (advisor.Settings, error) {
	return advisor.ResolveBlock("feedback.grouper", s)
}

// cutText is s cut to at most n bytes on a rune boundary, "…" marking a cut.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
