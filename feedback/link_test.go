package feedback

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const issueRefWant = ": want https://github.com/<owner>/<name>/issues/<N> or <owner>/<name>#<N>"

func TestParseIssueRef(t *testing.T) {
	for _, c := range []struct {
		ref  string
		repo string
		n    int
	}{
		{"https://github.com/aidan-bailey/loom/issues/57", "aidan-bailey/loom", 57},
		{"https://github.com/aidan-bailey/loom/issues/57/", "aidan-bailey/loom", 57},
		{"aidan-bailey/loom#57", "aidan-bailey/loom", 57},
		{"  aidan-bailey/loom#7\n", "aidan-bailey/loom", 7},
		{"a_b/c.d-e#1", "a_b/c.d-e", 1},
	} {
		repo, n, err := ParseIssueRef(c.ref)
		assert.NoError(t, err, c.ref)
		assert.Equal(t, c.repo, repo, c.ref)
		assert.Equal(t, c.n, n, c.ref)
	}
}

func TestParseIssueRef_RefusesWhatIsNotAnIssueOnGitHub(t *testing.T) {
	for _, ref := range []string{
		"",
		"57",
		"#57",
		"loom#57",
		"aidan-bailey/loom",
		"aidan-bailey/loom#0",
		"aidan-bailey/loom#-3",
		"aidan-bailey/loom#x",
		"aidan-bailey/loom#99999999999999999999",
		"aidan-bailey/loom/extra#5",
		"aidan-bailey/loom#5x",
		"xhttps://github.com/aidan-bailey/loom/issues/57",
		"../loom#5",
		"https://gitlab.com/aidan-bailey/loom/issues/57",
		"http://github.com/aidan-bailey/loom/issues/57",
		"https://github.com.evil.example/aidan-bailey/loom/issues/57",
		"https://github.com/aidan-bailey/loom/pull/57",
		"https://github.com/aidan-bailey/loom/issues/57/comments",
		"https://github.com/aidan-bailey/issues/57",
	} {
		repo, n, err := ParseIssueRef(ref)
		assert.EqualError(t, err, "issue reference "+ref+issueRefWant, ref)
		assert.Empty(t, repo, ref)
		assert.Zero(t, n, ref)
	}
}

func TestIssueURL(t *testing.T) {
	assert.Equal(t, "https://github.com/aidan-bailey/loom/issues/57", IssueURL("aidan-bailey/loom", 57))
}

func TestPublishedFixError(t *testing.T) {
	th := Thread{ID: 4, Status: Published, Issue: Issue{Repo: "aidan-bailey/loom", Number: 57}}
	assert.EqualError(t, PublishedFixError(th), "thread 4 is published as aidan-bailey/loom#57; the issue watcher fixes it")
}
