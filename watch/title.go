// Package watch is the issue watcher's logic: the loom-watch.json config, the
// title template and the branch pattern derived from it, prompt rendering,
// and the rest of what core calls from its loop and its poll job. It holds no
// model state and imports nothing from core.
package watch

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/aidan-bailey/loom/session/git"
)

// maxTitleWidth is the widest title, in terminal cells, loom accepts for a new
// session: core.MaxTitleWidth, which this package can't import (core imports
// it). TestMaxTitleWidth_MatchesCore keeps the two equal.
const maxTitleWidth = 32

// issueVar is the template variable that holds the issue number.
const issueVar = "issue"

var (
	varName    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	titleValue = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	neverMatch = regexp.MustCompile(`\b\B`)
)

// titlePart is a literal, or, when name is set, a {name} variable.
type titlePart struct {
	literal string
	name    string
}

// TitleTemplate is a parsed `title` of loom-watch.json (spec §3.2): literal
// text with {issue} once and any other {var} variables.
type TitleTemplate struct {
	parts []titlePart
	vars  []string
	re    *regexp.Regexp
}

// ParseTitleTemplate parses tmpl. It refuses a malformed brace, a variable
// name outside [a-z][a-z0-9_]*, a template that does not start with a literal
// word, one without {issue} or with a variable twice, two variables with no
// literal between them, and one no title could ever fit in maxTitleWidth.
func ParseTitleTemplate(tmpl string) (TitleTemplate, error) {
	parts, err := splitTitle(tmpl)
	if err == nil {
		err = checkTitle(parts)
	}
	if err != nil {
		return TitleTemplate{}, fmt.Errorf("title %q: %w", tmpl, err)
	}
	t := TitleTemplate{parts: parts}
	var re strings.Builder
	re.WriteString(`^`)
	for _, p := range parts {
		switch {
		case p.name == "":
			re.WriteString(regexp.QuoteMeta(p.literal))
		case p.name == issueVar:
			re.WriteString(`([0-9]+)`)
		default:
			re.WriteString(`([A-Za-z0-9._-]+)`)
			t.vars = append(t.vars, p.name)
		}
	}
	re.WriteString(`$`)
	t.re = regexp.MustCompile(re.String())
	return t, nil
}

// splitTitle cuts tmpl into literals and {name} variables.
func splitTitle(tmpl string) ([]titlePart, error) {
	var parts []titlePart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, titlePart{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(tmpl); {
		switch tmpl[i] {
		case '{':
			end := strings.IndexByte(tmpl[i:], '}')
			if end < 0 {
				return nil, errors.New("unclosed {")
			}
			name := tmpl[i+1 : i+end]
			if !varName.MatchString(name) {
				return nil, fmt.Errorf("{%s} is not a variable name ([a-z][a-z0-9_]*)", name)
			}
			flush()
			parts = append(parts, titlePart{name: name})
			i += end + 1
		case '}':
			return nil, errors.New("stray }")
		default:
			lit.WriteByte(tmpl[i])
			i++
		}
	}
	flush()
	return parts, nil
}

// checkTitle applies spec §3.2's rules to the split template.
func checkTitle(parts []titlePart) error {
	if len(parts) == 0 {
		return errors.New("is empty")
	}
	if first := parts[0]; first.name != "" || !isWordStart(first.literal[0]) {
		return errors.New("must start with a literal word")
	}
	seen := map[string]bool{}
	width := 0
	for i, p := range parts {
		if p.name == "" {
			width += runewidth.StringWidth(p.literal)
			continue
		}
		if seen[p.name] {
			return fmt.Errorf("{%s} appears more than once", p.name)
		}
		seen[p.name] = true
		width++
		if i > 0 && parts[i-1].name != "" {
			return fmt.Errorf("{%s} and {%s} need a literal between them", parts[i-1].name, p.name)
		}
	}
	if !seen[issueVar] {
		return errors.New("must contain {issue}")
	}
	if width > maxTitleWidth {
		return fmt.Errorf("is %d cells wide with every variable one cell, over the %d-cell limit: no title fits", width, maxTitleWidth)
	}
	return nil
}

func isWordStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Vars returns the variable names other than issue, in template order.
func (t TitleTemplate) Vars() []string {
	return slices.Clone(t.vars)
}

// Render fills the template with issue and vars. A variable the template
// names must be in vars and match [A-Za-z0-9._-]+ (extra entries are
// ignored); the title may not be wider than maxTitleWidth cells.
func (t TitleTemplate) Render(issue int, vars map[string]string) (string, error) {
	if len(t.parts) == 0 {
		return "", errors.New("title template is empty")
	}
	if issue < 1 {
		return "", fmt.Errorf("issue %d: want a number of 1 or more", issue)
	}
	var b strings.Builder
	for _, p := range t.parts {
		switch {
		case p.name == "":
			b.WriteString(p.literal)
		case p.name == issueVar:
			b.WriteString(strconv.Itoa(issue))
		default:
			v, ok := vars[p.name]
			if !ok {
				return "", fmt.Errorf("title variable {%s} is missing", p.name)
			}
			if !titleValue.MatchString(v) {
				return "", fmt.Errorf("title variable {%s} = %q: want [A-Za-z0-9._-]+", p.name, v)
			}
			b.WriteString(v)
		}
	}
	title := b.String()
	if w := runewidth.StringWidth(title); w > maxTitleWidth {
		return "", fmt.Errorf("title %q is %d cells wide, over the %d-cell limit", title, w, maxTitleWidth)
	}
	return title, nil
}

// Parse reads a title back: the issue number (canonical, at least 1) and the
// other variables' values. ok is false when title is not this template's.
func (t TitleTemplate) Parse(title string) (issue int, vars map[string]string, ok bool) {
	if t.re == nil {
		return 0, nil, false
	}
	m := t.re.FindStringSubmatch(title)
	if m == nil {
		return 0, nil, false
	}
	vars = map[string]string{}
	g := 1
	for _, p := range t.parts {
		if p.name == "" {
			continue
		}
		v := m[g]
		g++
		if p.name != issueVar {
			vars[p.name] = v
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || strconv.Itoa(n) != v {
			return 0, nil, false
		}
		issue = n
	}
	return issue, vars, true
}

// BranchPattern is the anchored regexp of the branches a session for issue
// gets under prefix (the workspace's branch prefix): git.SanitizeBranchName of
// prefix plus the template, each non-issue variable matching [a-z0-9._-]+.
func (t TitleTemplate) BranchPattern(prefix string, issue int) *regexp.Regexp {
	if len(t.parts) == 0 {
		return neverMatch
	}
	var text strings.Builder
	text.WriteString(prefix)
	for _, p := range t.parts {
		text.WriteString(p.literal)
	}
	mark, ok := freeLetter(text.String())
	if !ok {
		return neverMatch
	}

	// Each variable becomes mark + its index + mark. mark occurs nowhere
	// else, so splitting the sanitized string on it recovers the literal
	// pieces (even indexes) and the variable indexes (odd) exactly.
	var in strings.Builder
	in.WriteString(prefix)
	issueAt, n := -1, 0
	for _, p := range t.parts {
		if p.name == "" {
			in.WriteString(p.literal)
			continue
		}
		if p.name == issueVar {
			issueAt = n
		}
		in.WriteString(mark + strconv.Itoa(n) + mark)
		n++
	}
	pieces := strings.Split(git.SanitizeBranchName(in.String()), mark)
	if len(pieces)%2 == 0 {
		return neverMatch
	}

	var re strings.Builder
	re.WriteString(`^`)
	for i, piece := range pieces {
		if i%2 == 0 {
			re.WriteString(regexp.QuoteMeta(piece))
			continue
		}
		if idx, err := strconv.Atoi(piece); err == nil && idx == issueAt {
			re.WriteString(strconv.Itoa(issue))
		} else {
			re.WriteString(`[a-z0-9._-]+`)
		}
	}
	re.WriteString(`$`)
	return regexp.MustCompile(re.String())
}

// freeLetter returns a lower-case letter that text does not contain, in any
// case, or false when it holds all 26.
func freeLetter(text string) (string, bool) {
	low := strings.ToLower(text)
	for c := 'a'; c <= 'z'; c++ {
		if !strings.ContainsRune(low, c) {
			return string(c), true
		}
	}
	return "", false
}
