package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/feedback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fbPublished files a note, opens a thread for it and links the thread to
// repo#number as published, straight in m's inbox. It returns the thread.
func fbPublished(t *testing.T, m *Model, repo string, number int) feedback.ThreadID {
	t.Helper()
	now := time.Now()
	n := m.feedback.inbox.Add(feedback.Note{Kind: feedback.KindBug, Title: "t", Text: "x", At: now})
	id, err := m.feedback.inbox.OpenThread(n, "")
	require.NoError(t, err)
	require.NoError(t, m.feedback.inbox.Publish(id, repo, number, n, now))
	return id
}

func TestFeedbackView_IssueErrsAreCopiedAndOnlyForPublishedThreads(t *testing.T) {
	m := NewForTest(Options{})
	a := fbPublished(t, m, "o/r", 5)
	b := fbPublished(t, m, "o/r", 6)
	m.feedback.issueErrs = map[feedback.ThreadID]string{a: "gh issue view: exit status 1", b: "timeout"}

	v := m.Feedback()
	assert.Equal(t, map[feedback.ThreadID]string{a: "gh issue view: exit status 1", b: "timeout"}, v.IssueErrs)

	v.IssueErrs[a] = "edited"
	assert.Equal(t, "gh issue view: exit status 1", m.feedback.issueErrs[a], "the view is a copy of the model's map")

	c := v.Clone()
	c.IssueErrs[b] = "edited"
	assert.Equal(t, "timeout", v.IssueErrs[b], "Clone deep-copies the map")

	m.feedback.inbox.CloseIssue("o/r", 6, "closed by hand", time.Now(), time.Now())
	assert.Equal(t, map[feedback.ThreadID]string{a: "gh issue view: exit status 1"}, m.Feedback().IssueErrs,
		"a thread that is no longer published drops out")

	m.feedback.inbox.CloseIssue("o/r", 5, "closed by hand", time.Now(), time.Now())
	assert.Nil(t, m.Feedback().IssueErrs, "none left: nil, not an empty map")
	assert.Nil(t, FeedbackView{}.Clone().IssueErrs)
}
