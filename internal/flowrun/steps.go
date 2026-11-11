package flowrun

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The step kinds a run's names carry (engine.next's prefixes).
const (
	KindPremise = "premise"
	KindDev     = "dev"
	KindGates   = "gates"
	KindReview  = "review"
	KindFix     = "fix"
	KindDocs    = "docs"
)

// Step is what an attempt's name says: the story it belongs to ("" for the
// premises and the final round), whether it is the final round's, its kind
// and its attempt number (0 when a question names a kind, not an attempt).
type Step struct {
	Story string
	Final bool
	Kind  string
	K     int
}

// attempt is a name's tail past its story or final prefix: a kind and an
// attempt number from 1, as engine.next counts them.
var attempt = regexp.MustCompile(`^(dev|gates|review|fix|docs)([1-9][0-9]*)$`)

// premiseAttempt is a premise step's tail.
var premiseAttempt = regexp.MustCompile(`^premise([1-9][0-9]*)$`)

// StepOf parses run's attempt name <run>-<prefix><k> (engine.next), with
// the run's stories, so that an id storyPrefix keeps with a '-' in it
// matches whole: the longest story prefix wins. ok is false for another
// run's name, a story the run doesn't have, or a kind next doesn't make.
func StepOf(run, name string, stories []Story) (Step, bool) {
	rest, ok := strings.CutPrefix(name, run+"-")
	if !ok {
		return Step{}, false
	}
	if m := premiseAttempt.FindStringSubmatch(rest); m != nil {
		n, _ := strconv.Atoi(m[1])
		return Step{Kind: KindPremise, K: n}, true
	}
	if tail, ok := strings.CutPrefix(rest, finalStory+"-"); ok {
		m := attempt.FindStringSubmatch(tail)
		if m == nil || m[1] == KindDev || m[1] == KindDocs { // the final round has no developer and no doc pass
			return Step{}, false
		}
		n, _ := strconv.Atoi(m[2])
		return Step{Final: true, Kind: m[1], K: n}, true
	}
	ids := make([]string, len(stories))
	for i, s := range stories {
		ids[i] = s.ID
	}
	sort.SliceStable(ids, func(i, j int) bool { return len(storyPrefix(ids[i])) > len(storyPrefix(ids[j])) })
	for _, id := range ids {
		tail, ok := strings.CutPrefix(rest, storyPrefix(id)+"-")
		if !ok {
			continue
		}
		if m := attempt.FindStringSubmatch(tail); m != nil {
			n, _ := strconv.Atoi(m[2])
			return Step{Story: id, Kind: m[1], K: n}, true
		}
	}
	return Step{}, false
}

// QuestionStep is the step a parked run's question is about, for the
// board's amber cell: a key that names an attempt (tree:, ctx:, failed:,
// blocked:, gates:, strays:, limit:) is that attempt; a review's question (design:,
// unresolved:, plan:) is its story's review, or the final round's; the
// premises' question is the premise row's. ok is false for a key this
// build doesn't know.
func QuestionStep(run string, q Question, stories []Story) (Step, bool) {
	head, rest, _ := strings.Cut(q.Key, ":")
	switch head {
	case "tree", "ctx", "failed", "blocked", "gates", "strays", "limit":
		name, _, _ := strings.Cut(rest, ":")
		return StepOf(run, name, stories)
	case "design", "unresolved", "plan":
		if q.Story == finalStory || rest == finalStory || strings.HasPrefix(rest, finalStory+":") {
			return Step{Final: true, Kind: KindReview}, true
		}
		if q.Story == "" {
			return Step{}, false
		}
		return Step{Story: q.Story, Kind: KindReview}, true
	case "premise-design":
		return Step{Kind: KindPremise}, true
	}
	return Step{}, false
}
