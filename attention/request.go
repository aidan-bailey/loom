package attention

import "unicode/utf8"

// MaxPrompt bounds the last prompt a request carries, in bytes.
const MaxPrompt = 2 << 10

// Request is one call's input, sent as JSON on stdin. PromptFrom says who
// sent the last prompt: "owner" (the person), "session <name>" (another
// session), "plugin <name>", "task notification", or "" when unknown.
type Request struct {
	Title        string `json:"title"`
	PromptFrom   string `json:"prompt_from"`
	LastPrompt   string `json:"last_prompt"`
	LastMessage  string `json:"last_message"`
	JobsInFlight int    `json:"jobs_in_flight"`
}

// NewRequest is the request for a turn, its prompt cut to MaxPrompt bytes.
// The message comes capped by the hooks (hooks.MaxMessageBytes).
func NewRequest(title, from, prompt, message string, jobs int) Request {
	return Request{Title: title, PromptFrom: from, LastPrompt: cutText(prompt, MaxPrompt), LastMessage: message, JobsInFlight: jobs}
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
