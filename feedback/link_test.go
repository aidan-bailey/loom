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

func TestThread_NewNotes(t *testing.T) {
	// Notes joined in the order 1, 3, 2, 4.
	th := Thread{ID: 1, Status: Published, Notes: []NoteID{1, 3, 2, 4}, Issue: Issue{Posted: 3}}
	assert.Equal(t, 2, th.NewNotes(), "notes 2 and 4 joined after note 3, though note 2's ID is lower")
	th.Issue.Posted = 4
	assert.Equal(t, 0, th.NewNotes(), "posted through the last note")
	th.Issue.Posted = 1
	assert.Equal(t, 3, th.NewNotes())
	th.Issue.Posted = 2
	assert.Equal(t, 1, th.NewNotes())
	for _, s := range []Status{Open, Fixing, Closed} {
		th.Issue.Posted, th.Status = 1, s
		assert.Equal(t, 0, th.NewNotes(), string(s)+": only a published thread has new notes")
	}
}

func TestStatusText(t *testing.T) {
	for name, c := range map[string]struct {
		row  Row
		want string
	}{
		"open":                 {Row{Thread: 1, Status: Open}, "open"},
		"fixing":               {Row{Thread: 1, Status: Fixing, Fix: Fix{Title: "fb-1-a"}}, "fixing fb-1-a"},
		"fixing with no title": {Row{Thread: 1, Status: Fixing}, "fixing"},
		"published":            {Row{Thread: 1, Status: Published, Issue: Issue{Number: 57}}, "published #57"},
		"published with new":   {Row{Thread: 1, Status: Published, Issue: Issue{Number: 57}, New: 2}, "published #57 +2 new"},
		"closed":               {Row{Thread: 1, Status: Closed, Issue: Issue{Number: 57}, Closed: Closure{Reason: "no fix"}}, "closed"},
		"a pending note":       {Row{Note: 3, Grouping: Pending}, "pending"},
		"an ungrouped note":    {Row{Note: 4, Grouping: Ungrouped}, "ungrouped"},
	} {
		assert.Equal(t, c.want, StatusText(c.row), name)
	}
}
