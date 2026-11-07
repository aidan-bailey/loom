package attention

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The verdicts.
const (
	NeedsYou = "needs_you"
	Done     = "done"
	Waiting  = "waiting"
	Stopped  = "stopped"
)

// verdicts are the four, in the order the prompt names them.
var verdicts = []string{NeedsYou, Done, Waiting, Stopped}

// MaxGist bounds a gist, in characters.
const MaxGist = 100

// Schema is the answer's JSON schema, passed to --json-schema.
const Schema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["needs_you","done","waiting","stopped"]},"gist":{"type":"string","maxLength":100}},"required":["verdict","gist"],"additionalProperties":false}`

// Answer is the classifier's answer: a verdict and its one-line gist.
type Answer struct {
	Verdict string `json:"verdict"`
	Gist    string `json:"gist"`
}

// ParseVerdict checks raw: one of the four verdicts, and a gist that,
// trimmed, is one line of at most MaxGist characters. A bad answer is an
// error, which the caller counts as a failed call.
func ParseVerdict(raw json.RawMessage) (Answer, error) {
	var a Answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return Answer{}, fmt.Errorf("the classifier's answer: %w", err)
	}
	a.Gist = strings.TrimSpace(a.Gist)
	switch {
	case !slices.Contains(verdicts, a.Verdict):
		return Answer{}, fmt.Errorf("the classifier's verdict %q is not one of %s", a.Verdict, strings.Join(verdicts, ", "))
	case strings.ContainsAny(a.Gist, "\r\n") || utf8.RuneCountInString(a.Gist) > MaxGist:
		return Answer{}, fmt.Errorf("the classifier's gist is not one line of at most %d characters", MaxGist)
	}
	return a, nil
}
