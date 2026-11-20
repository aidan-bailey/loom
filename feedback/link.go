package feedback

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The two spellings of an issue reference. Owner and name start with a letter,
// digit or underscore, so `.` and `..` are no repository.
var (
	issueURLRef   = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_][A-Za-z0-9_.-]*)/([A-Za-z0-9_][A-Za-z0-9_.-]*)/issues/([0-9]+)/?$`)
	issueShortRef = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*)/([A-Za-z0-9_][A-Za-z0-9_.-]*)#([0-9]+)$`)
)

// ParseIssueRef reads an issue reference, https://github.com/<owner>/<name>/issues/<N>
// or <owner>/<name>#<N>, into its repository ("<owner>/<name>") and number
// (at least 1). Surrounding space is ignored; any other host or shape is an
// error.
func ParseIssueRef(ref string) (repo string, number int, err error) {
	s := strings.TrimSpace(ref)
	m := issueURLRef.FindStringSubmatch(s)
	if m == nil {
		m = issueShortRef.FindStringSubmatch(s)
	}
	if m != nil {
		if n, aerr := strconv.Atoi(m[3]); aerr == nil && n >= 1 {
			return m[1] + "/" + m[2], n, nil
		}
	}
	return "", 0, fmt.Errorf("issue reference %s: want https://github.com/<owner>/<name>/issues/<N> or <owner>/<name>#<N>", ref)
}

// IssueURL is issue number of repo ("<owner>/<name>") on GitHub.
func IssueURL(repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, number)
}

// PublishedFixError is the refusal both clients give to starting a fix
// session for a published thread: the issue watcher fixes it.
func PublishedFixError(t Thread) error {
	return fmt.Errorf("thread %d is published as %s#%d; the issue watcher fixes it", t.ID, t.Issue.Repo, t.Issue.Number)
}

// NewNotes is how many of a published thread's notes joined after
// Issue.Posted, in Thread.Notes order; 0 for a thread that is not published.
func (t Thread) NewNotes() int {
	if t.Status != Published {
		return 0
	}
	return len(t.Notes) - 1 - slices.Index(t.Notes, t.Issue.Posted)
}

// StatusText is a row's status as both clients show it: a note's grouping;
// a thread's `open`, `fixing <fix title>` (bare `fixing` with no title),
// `published #<N>` plus ` +<k> new` when k > 0, or `closed`.
func StatusText(r Row) string {
	if r.Thread == 0 {
		return string(r.Grouping)
	}
	switch r.Status {
	case Fixing:
		if r.Fix.Title != "" {
			return "fixing " + r.Fix.Title
		}
		return "fixing"
	case Published:
		s := fmt.Sprintf("published #%d", r.Issue.Number)
		if r.New > 0 {
			s += fmt.Sprintf(" +%d new", r.New)
		}
		return s
	}
	return string(r.Status)
}
