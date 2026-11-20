package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fbView is an inbox with thread 1 (two notes from two sessions) and note
// 3, which no thread holds, an hour old.
func fbView(t *testing.T, now time.Time) core.FeedbackView {
	t.Helper()
	in := feedback.Empty()
	at := now.Add(-time.Hour)
	n1 := in.Add(feedback.Note{Kind: feedback.KindBug, Title: "--after refused", Text: "line one\nline two\nline three\nline four\nline five",
		From: feedback.Session{ID: 1, Title: "builder", Workspace: "loom", Model: "claude-opus-5-5", Effort: "xhigh"}, Build: "0.15.0 abc", At: at})
	th, err := in.OpenThread(n1, "loom job run: --after refuses")
	require.NoError(t, err)
	require.NoError(t, in.Attach(in.Add(feedback.Note{Kind: feedback.KindBug, Title: "after again", Text: "same",
		From: feedback.Session{ID: 2, Title: "tester", Workspace: "loom"}, At: at}), th))
	in.Add(feedback.Note{Kind: feedback.KindMissing, Title: "a loose note", Text: "y", From: feedback.Session{ID: 3, Title: "s3"}, At: at})
	return core.FeedbackView{Inbox: in, Grouper: "turned off (feedback.grouper.enabled)"}
}

func plainLines(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

func TestRenderFeedback_ListsRankedRows(t *testing.T) {
	now := time.Now()
	out := plainLines(RenderFeedback(120, 20, FeedbackData{View: fbView(t, now), Now: now}))
	assert.Contains(t, out[0], "Feedback · 1 open")
	assert.Contains(t, out[1], "grouper off: turned off (feedback.grouper.enabled)")
	assert.Regexp(t, `^ +ID +KIND +SESS +NOTES +LAST +STATUS +TITLE`, out[2])
	assert.Regexp(t, `^ ▸ 1 +bug +2 +2 +1h ago +open +loom job run: --after refuses`, out[3])
	assert.Regexp(t, `^ +n3 +missing +1 +1 +1h ago +pending +a loose note`, out[4])
}

func TestRenderFeedback_ExpandsTheSelectedRow(t *testing.T) {
	now := time.Now()
	out := ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: fbView(t, now), Expanded: true, Now: now}))
	for _, want := range []string{"n1 bug · builder in loom · 1h ago · claude-opus-5-5 at xhigh effort · loom 0.15.0 abc", "--after refused",
		"line three", "… 2 more lines (loom feedback show n1)", "n2 bug · tester in loom", "after again"} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "line four", "three lines of a note's text")

	one := fbView(t, now)
	one.Inbox.Notes[0].Text = "a\nb\nc\nd"
	assert.Contains(t, ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: one, Expanded: true, Now: now})), "… 1 more line (loom feedback show n1)")

	out = ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: fbView(t, now), Selected: 1, Expanded: true, Now: now}))
	assert.Contains(t, out, "n3 missing · s3", "a loose note expands to itself")
	assert.NotContains(t, out, "n1 bug ·", "only the selected row expands")
}

func TestRenderFeedback_FitsItsBox(t *testing.T) {
	now := time.Now()
	for _, size := range [][2]int{{120, 20}, {60, 6}, {30, 3}} {
		out := RenderFeedback(size[0], size[1], FeedbackData{View: fbView(t, now), Expanded: true, Now: now})
		lines := strings.Split(out, "\n")
		assert.Len(t, lines, size[1])
		for _, l := range lines {
			assert.Equal(t, size[0], ansi.StringWidth(l), "every line fills the box, or the layout centers it: %q", l)
		}
	}
	assert.Empty(t, RenderFeedback(0, 10, FeedbackData{}))
}

func TestRenderFeedback_SaysWhenEmptyAndShowsTheInboxError(t *testing.T) {
	out := ansi.Strip(RenderFeedback(100, 5, FeedbackData{View: core.FeedbackView{Inbox: feedback.Empty(), Err: "feedback.json is from a newer loom"}}))
	assert.Contains(t, out, "inbox: feedback.json is from a newer loom")
	assert.Contains(t, out, "No feedback yet. Sessions file it with loom feedback add.")
}

// fbPublished is fbView with thread 1 published through its first note, so
// its second note is new.
func fbPublished(t *testing.T, now time.Time) core.FeedbackView {
	t.Helper()
	v := fbView(t, now)
	require.NoError(t, v.Inbox.Publish(1, "aidan-bailey/loom", 57, 1, now))
	return v
}

func TestRenderFeedback_ShowsThePublishedStatusAndNewCount(t *testing.T) {
	now := time.Now()
	out := plainLines(RenderFeedback(120, 20, FeedbackData{View: fbPublished(t, now), Now: now}))
	assert.Regexp(t, `^ ▸ 1 +bug +2 +2 +1h ago +published #57 \+1 new +loom job run: --after refuses`, out[3])
	assert.Regexp(t, `^ +n3 +missing +1 +1 +1h ago +pending`, out[4])
}

func TestRenderFeedback_StatusTextForEveryRow(t *testing.T) {
	now := time.Now()
	v := fbView(t, now)
	require.NoError(t, v.Inbox.SetFix(1, feedback.Fix{Title: "fb-1-x"}))
	out := ansi.Strip(RenderFeedback(140, 20, FeedbackData{View: v, Now: now}))
	assert.Contains(t, out, "fixing fb-1-x")
	assert.Contains(t, out, "pending")

	v = fbView(t, now)
	require.NoError(t, v.Inbox.SetFix(1, feedback.Fix{}))
	assert.Regexp(t, `1 +bug +2 +2 +1h ago +fixing +loom job`, plainLines(RenderFeedback(140, 20, FeedbackData{View: v, Now: now}))[3])

	v = fbView(t, now)
	v.Inbox.Fail(3, "boom", now.Add(time.Hour))
	assert.Regexp(t, `n3 +missing +1 +1 +1h ago +ungrouped`, ansi.Strip(RenderFeedback(140, 20, FeedbackData{View: v, Now: now})))
}

func TestRenderFeedback_ExpandedPublishedRowShowsTheLinkAndThePollError(t *testing.T) {
	now := time.Now()
	v := fbPublished(t, now)
	v.IssueErrs = map[feedback.ThreadID]string{1: "gh: auth\nretry"}
	out := ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: v, Expanded: true, Now: now}))
	link := "issue: https://github.com/aidan-bailey/loom/issues/57 · posted through n1"
	assert.Contains(t, out, link)
	assert.Contains(t, out, "issue check failed: gh: auth retry")
	head := strings.Index(out, "n1 bug · builder")
	assert.Less(t, strings.Index(out, link), head)
	assert.Less(t, strings.Index(out, "issue check failed"), head)

	v.IssueErrs = nil
	out = ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: v, Expanded: true, Now: now}))
	assert.Contains(t, out, "issue:")
	assert.NotContains(t, out, "issue check failed")

	v.IssueErrs = map[feedback.ThreadID]string{1: "gh: auth\nretry"}
	for _, l := range plainLines(RenderFeedback(60, 30, FeedbackData{View: v, Expanded: true, Now: now})) {
		assert.LessOrEqual(t, ansi.StringWidth(l), 60, l)
	}
}

func TestRenderFeedback_CollapsedAndUnpublishedRowsShowNoLink(t *testing.T) {
	now := time.Now()
	v := fbPublished(t, now)
	v.IssueErrs = map[feedback.ThreadID]string{1: "gh: auth"}
	out := ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: v, Now: now}))
	assert.NotContains(t, out, "issue:")
	assert.NotContains(t, out, "issue check failed")

	u := fbView(t, now)
	u.IssueErrs = map[feedback.ThreadID]string{2: "gh: auth"}
	out = ansi.Strip(RenderFeedback(140, 30, FeedbackData{View: u, Expanded: true, Now: now}))
	assert.NotContains(t, out, "issue:")
	assert.NotContains(t, out, "issue check failed")
}
