package flowrun

import (
	"fmt"
	"slices"
	"strings"
)

// The question kinds (spec §5.4).
const (
	KindNeedsContext  = "needs-context"
	KindBlocked       = "blocked-decision"
	KindDesignFinding = "design-finding"
	KindPremiseDesign = "premise-design"
	KindUnresolved    = "unresolved-findings"
	KindGateFailure   = "gate-failure"
	KindAgentFailed   = "agent-failed"
	KindTreeMoved     = "tree-moved"
	KindDocsStrayed   = "docs-strayed"
	// KindUsageLimit is an agent stopped on a usage limit that resets more
	// than LimitWaitWithin away, or at a time it didn't say (usage-limit
	// hold spec §3).
	KindUsageLimit = "usage-limit"
)

// The answer words.
const (
	WordStop     = "stop"
	WordAccept   = "accept"
	WordRetry    = "retry"
	WordContinue = "continue"
	// WordWait resubmits a step a usage limit stopped; the queue holds it
	// until the limit resets.
	WordWait = "wait"
)

var allWords = []string{WordStop, WordAccept, WordRetry, WordContinue, WordWait}

// takes is each kind's words, and whether it takes text (a decision).
var takes = map[string]struct {
	words []string
	text  bool
}{
	KindNeedsContext:  {nil, true},
	KindBlocked:       {[]string{WordStop}, true},
	KindDesignFinding: {[]string{WordAccept, WordStop}, true},
	KindPremiseDesign: {[]string{WordStop}, true},
	KindUnresolved:    {[]string{WordAccept, WordStop}, true},
	KindGateFailure:   {[]string{WordRetry, WordAccept, WordStop}, true},
	KindAgentFailed:   {[]string{WordRetry, WordStop}, true},
	KindTreeMoved:     {[]string{WordContinue, WordStop}, false},
	KindDocsStrayed:   {[]string{WordAccept, WordStop}, true},
	KindUsageLimit:    {[]string{WordWait, WordStop}, false},
}

// CheckAnswer checks answer against kind: the word it is, or "" for a text
// answer. An answer that is exactly a word is that word.
func CheckAnswer(kind, answer string) (string, error) {
	t, ok := takes[kind]
	if !ok {
		return "", fmt.Errorf("unknown question kind %q", kind)
	}
	a := strings.TrimSpace(answer)
	switch {
	case a == "":
		return "", fmt.Errorf("the answer is empty")
	case slices.Contains(t.words, a):
		return a, nil
	case slices.Contains(allWords, a):
		return "", fmt.Errorf("a %s question doesn't take %q; it takes %s", kind, a, Takes(kind))
	case !t.text:
		return "", fmt.Errorf("a %s question takes only %s", kind, Takes(kind))
	}
	return "", nil
}

// Takes is what kind takes, for the manager: "a decision, accept or stop".
func Takes(kind string) string {
	t := takes[kind]
	var parts []string
	if kind == KindNeedsContext {
		parts = append(parts, "an answer")
	} else if t.text {
		parts = append(parts, "a decision")
	}
	parts = append(parts, t.words...)
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}
