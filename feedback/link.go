package feedback

import (
	"fmt"
	"regexp"
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
