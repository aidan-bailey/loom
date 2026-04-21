package overlay

import (
	"sort"
	"strings"
	"unicode"
)

// FileMatch is a single scored result from FuzzyMatch. Path is the
// candidate as supplied by the caller (relative, forward-slash
// separated by convention); Score is the heuristic rank (higher is
// better); MatchedIdx holds the rune indices within Path that the
// pattern's runes matched against, so renderers can highlight them.
type FileMatch struct {
	Path       string
	Score      int
	MatchedIdx []int
}

// FuzzyMatch returns all candidates containing pattern as a
// case-insensitive subsequence, ranked best-first. Empty pattern is a
// pass-through that preserves input order with Score=0 so an
// unfiltered view renders in the caller's preferred (usually
// alphabetical) order.
//
// The scoring heuristics favor what humans intuitively expect from a
// file picker:
//
//   - filename-prefix hits (typing "ma" should surface main.go first),
//   - contiguous streaks (typing "conf" should beat "c-o-n-f" scatter),
//   - word-boundary starts (after '/', '-', '_', or case transitions),
//   - matches in the filename over matches in a directory segment,
//   - earlier-starting matches over later ones.
//
// Ties break by shorter path first, so short canonical files win over
// verbose ones.
func FuzzyMatch(pattern string, candidates []string) []FileMatch {
	if pattern == "" {
		out := make([]FileMatch, len(candidates))
		for i, c := range candidates {
			out[i] = FileMatch{Path: c}
		}
		return out
	}

	lowerPattern := strings.ToLower(pattern)
	patternRunes := []rune(lowerPattern)

	out := make([]FileMatch, 0, len(candidates))
	for _, c := range candidates {
		score, matched, ok := scorePath(patternRunes, c)
		if !ok {
			continue
		}
		out = append(out, FileMatch{Path: c, Score: score, MatchedIdx: matched})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return len(out[i].Path) < len(out[j].Path)
	})
	return out
}

// scorePath walks a single candidate and tries to consume every rune
// of pattern as a subsequence. Returns the aggregate score, the rune
// indices where each pattern rune landed, and whether the full
// pattern was consumed.
func scorePath(pattern []rune, candidate string) (int, []int, bool) {
	filenameStart := strings.LastIndex(candidate, "/") + 1
	lowerCandidate := strings.ToLower(candidate)
	candidateRunes := []rune(lowerCandidate)

	score := 0
	matched := make([]int, 0, len(pattern))
	pi := 0
	streak := 0
	firstMatchAt := -1

	// Case-independent filename-prefix bonus: if the filename (last
	// path segment) starts with the lowercased pattern, a big +32
	// ensures "main.go" ranks above "command.go" for query "ma".
	filenameLower := lowerCandidate[filenameStart:]
	if strings.HasPrefix(filenameLower, string(pattern)) {
		score += 32
	}

	for ci, cr := range candidateRunes {
		if pi >= len(pattern) {
			break
		}
		if cr != pattern[pi] {
			streak = 0
			continue
		}
		if firstMatchAt < 0 {
			firstMatchAt = ci
		}
		matched = append(matched, ci)

		// Every match gets a small base score; the real ranking
		// signal comes from the bonuses below.
		score += 4

		// Streak bonus rewards contiguous runs so "foo" on "foo.go"
		// beats "foo" on "f_o_o.go". The bonus only fires from the
		// second rune of a streak onward because the first rune of a
		// streak is scored via the base +4 / boundary / filename
		// bonuses.
		if streak > 0 {
			score += 12
		}
		streak++

		// Word-boundary bonus: the matched rune sits at the start of a
		// segment (/-_) or immediately after punctuation.
		if ci == 0 || isBoundary(candidateRunes[ci-1]) {
			score += 8
		}

		// Filename-body bonus: rewards landing inside the filename
		// rather than wandering through a directory path.
		if ci >= filenameStart {
			score += 4
		}

		pi++
	}

	if pi < len(pattern) {
		return 0, nil, false
	}

	// Leading skip penalty: prefer matches that start near the head
	// of the path. Uses the first match position, capped so truly
	// long paths aren't ruled out entirely.
	if firstMatchAt > 0 {
		penalty := firstMatchAt
		if penalty > 16 {
			penalty = 16
		}
		score -= penalty
	}

	return score, matched, true
}

// isBoundary reports whether r is the kind of rune that separates
// word-like segments in a path. Used to detect match positions that
// "start a word" for the +8 word-boundary bonus.
func isBoundary(r rune) bool {
	switch r {
	case '/', '-', '_', '.', ' ':
		return true
	}
	return unicode.IsSpace(r)
}
