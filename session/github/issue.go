package github

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// slugMax caps the slug portion of SlugTitle (after "gh-<n>-"), in
// bytes; slugs are ASCII so bytes equal runes.
const slugMax = 40

// SlugTitle yields the session title for an issue: "gh-<n>-<slug>",
// where slug is the lowercased title with every non-alphanumeric run
// collapsed to one dash, capped at slugMax and trimmed of a trailing
// dash. A title with no usable characters yields "gh-<n>".
func SlugTitle(is Issue) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(is.Title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := b.String()
	if len(slug) > slugMax {
		slug = slug[:slugMax]
	}
	slug = strings.TrimRight(slug, "-")
	if slug == "" {
		return fmt.Sprintf("gh-%d", is.Number)
	}
	return fmt.Sprintf("gh-%d-%s", is.Number, slug)
}

// SeedPrompt composes the agent's initial prompt from an issue: a
// preamble naming the issue and URL, the title as a heading, then the
// body verbatim.
func SeedPrompt(is Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are working on GitHub issue #%d (%s).\n\n# %s\n", is.Number, is.URL, is.Title)
	if body := strings.TrimSpace(is.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

// ParseShorthand recognizes a prompt whose first token is "#<n>" and
// returns the number and the remaining text (trimmed). ok is false for
// anything else, including "#0".
func ParseShorthand(prompt string) (n int, rest string, ok bool) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "#") {
		return 0, "", false
	}
	n, err := strconv.Atoi(fields[0][1:])
	if err != nil || n <= 0 {
		return 0, "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prompt), fields[0]))
	return n, rest, true
}
