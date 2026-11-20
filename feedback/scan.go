package feedback

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ScanKind is what a scan hit is.
type ScanKind string

// The kinds of hit Scan reports.
const (
	ScanPath      ScanKind = "path"
	ScanWorkspace ScanKind = "workspace"
	ScanSession   ScanKind = "session"
	ScanIdentity  ScanKind = "identity"
	ScanThread    ScanKind = "thread"
	ScanEmail     ScanKind = "email"
	ScanUUID      ScanKind = "uuid"
	ScanToken     ScanKind = "token"
)

// ScanTerms is what is private on this host, as the caller found it. Scan
// only matches; it asks nobody.
type ScanTerms struct {
	// Private are path prefixes (home, every served workspace's repository,
	// raw and canonical) that are hits in any position, at a component
	// boundary.
	Private []string
	// Workspaces are workspace names, matched as whole words.
	Workspaces []string
	// Sessions are session titles, matched as whole word sequences.
	Sessions []string
	// Identity is the user, host, emails and account names.
	Identity []string
	// Thread are a thread's notes' Claude session IDs, transcript paths and
	// branches.
	Thread []string
	// ThreadNames are the session titles and workspace names of a thread's
	// notes, matched as whole word sequences and reported as ScanThread.
	ThreadNames []string
}

// ScanHit is one match: its 1-based line and column (in characters) and
// the text as written.
type ScanHit struct {
	Line  int
	Col   int
	Kind  ScanKind
	Match string
}

// Scan reports what text holds that terms or the credential, email and UUID
// shapes call private, in the order it appears. A hit whose whole match is
// a word in allow (any case) is returned in allowed instead. Two matches
// of one span are one hit, of the first kind that found it: credentials,
// the thread's terms, identity, paths, workspaces, sessions, emails, UUIDs.
func Scan(text string, terms ScanTerms, allow []string) (hits []ScanHit, allowed []ScanHit) {
	finders := scanFinders(terms)
	for i, line := range strings.Split(text, "\n") {
		seen := map[scanSpan]bool{}
		var found []scanFound
		for _, f := range finders {
			for _, s := range f.find(line) {
				if !seen[s] {
					seen[s] = true
					found = append(found, scanFound{scanSpan: s, kind: f.kind})
				}
			}
		}
		sort.SliceStable(found, func(a, b int) bool { return found[a].start < found[b].start })
		for _, f := range found {
			h := ScanHit{Line: i + 1, Col: utf8.RuneCountInString(line[:f.start]) + 1, Kind: f.kind, Match: line[f.start:f.end]}
			if scanAllowed(h.Match, allow) {
				allowed = append(allowed, h)
			} else {
				hits = append(hits, h)
			}
		}
	}
	return hits, allowed
}

// scanAllowed reports whether match is a word the caller allows.
func scanAllowed(match string, allow []string) bool {
	for _, a := range allow {
		if a != "" && strings.EqualFold(a, match) {
			return true
		}
	}
	return false
}

// scanSpan is a match's byte range in one line.
type scanSpan struct{ start, end int }

// scanFound is a span and the kind that found it.
type scanFound struct {
	scanSpan
	kind ScanKind
}

// finder finds one kind's spans in a line.
type finder struct {
	kind ScanKind
	find func(line string) []scanSpan
}

// scanFinders are the finders in priority order: the first to find a span
// names its kind.
func scanFinders(terms ScanTerms) []finder {
	var fs []finder
	fs = append(fs, tokenFinders()...)
	fs = append(fs, wordFinders(ScanThread, terms.Thread)...)
	fs = append(fs, wordFinders(ScanThread, terms.ThreadNames)...)
	fs = append(fs, wordFinders(ScanIdentity, terms.Identity)...)
	fs = append(fs, wordFinders(ScanWorkspace, terms.Workspaces)...)
	fs = append(fs, wordFinders(ScanSession, terms.Sessions)...)
	fs = append(fs, patternFinder(ScanEmail, scanEmailRe), patternFinder(ScanUUID, scanUUIDRe))
	return fs
}

// bounded is an expression for p, in group 1, that may not start inside a
// word.
func bounded(p string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(` + p + `)`)
}

var scanTokenRes = []*regexp.Regexp{
	bounded(`gh[pousr]_[A-Za-z0-9]{20,}`),
	bounded(`github_pat_[A-Za-z0-9_]{20,}`),
	bounded(`sk-[A-Za-z0-9_-]{20,}`),
	bounded(`xox[abp]-[A-Za-z0-9-]{10,}`),
	bounded(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(-----BEGIN [A-Z ]*PRIVATE KEY-----)`),
}

var (
	scanEmailRe = bounded(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	scanUUIDRe  = bounded(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// tokenFinders is one ScanToken finder per credential expression.
func tokenFinders() []finder {
	fs := make([]finder, 0, len(scanTokenRes))
	for _, re := range scanTokenRes {
		fs = append(fs, patternFinder(ScanToken, re))
	}
	return fs
}

// patternFinder finds re's group 1.
func patternFinder(kind ScanKind, re *regexp.Regexp) finder {
	return finder{kind: kind, find: func(line string) []scanSpan {
		var out []scanSpan
		for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
			out = append(out, scanSpan{start: m[2], end: m[3]})
		}
		return out
	}}
}

// wordRune reports whether r is part of a word: a letter, a digit or `_`.
func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordFinders is one finder of kind for each of terms that holds a word.
func wordFinders(kind ScanKind, terms []string) []finder {
	var out []finder
	for _, t := range terms {
		if f := wordFinder(t); f != nil {
			out = append(out, finder{kind: kind, find: f})
		}
	}
	return out
}

// wordFinder finds term, any case, with any run of whitespace between its
// words, where it does not start or end inside a word: a term that begins
// (ends) with a word character may not follow (precede) another. A term
// with no word finds nothing.
func wordFinder(term string) func(line string) []scanSpan {
	words := strings.Fields(strings.ToValidUTF8(term, ""))
	if len(words) == 0 {
		return nil
	}
	first, _ := utf8.DecodeRuneInString(words[0])
	last, _ := utf8.DecodeLastRuneInString(words[len(words)-1])
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = regexp.QuoteMeta(w)
	}
	re, err := regexp.Compile(`(?i)` + strings.Join(quoted, `\s+`))
	if err != nil {
		return nil
	}
	return func(line string) []scanSpan {
		var out []scanSpan
		for pos := 0; pos < len(line); {
			loc := re.FindStringIndex(line[pos:])
			if loc == nil {
				break
			}
			s, e := pos+loc[0], pos+loc[1]
			before, _ := utf8.DecodeLastRuneInString(line[:s])
			after, _ := utf8.DecodeRuneInString(line[e:])
			startOK := s == 0 || !wordRune(first) || !wordRune(before)
			endOK := e == len(line) || !wordRune(last) || !wordRune(after)
			if startOK && endOK {
				out = append(out, scanSpan{start: s, end: e})
				pos = e
				continue
			}
			_, w := utf8.DecodeRuneInString(line[s:])
			pos = s + w
		}
		return out
	}
}
