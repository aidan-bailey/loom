package feedback

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInbox_AddCountsAndStartsPending(t *testing.T) {
	in := Empty()
	stale := note(1, "a", 0)
	stale.Thread, stale.Grouping, stale.GroupErr = 9, Grouped, "x"
	assert.Equal(t, NoteID(1), in.Add(stale))
	assert.Equal(t, NoteID(2), in.Add(note(1, "b", 1)))
	assert.Equal(t, NoteID(3), in.Add(note(1, "c", 2)))
	assert.Equal(t, NoteID(4), in.NextNote)
	n, ok := in.Note(1)
	require.True(t, ok)
	assert.Equal(t, Pending, n.Grouping)
	assert.Zero(t, n.Thread)
	assert.Empty(t, n.GroupErr)
}

func TestInbox_OpenAndAttach(t *testing.T) {
	in := Empty()
	a := in.Add(note(1, "first", 0))
	b := in.Add(note(2, "second", 5))
	c := in.Add(note(3, "third", 9))
	th, err := in.OpenThread(a, "")
	require.NoError(t, err)
	got, _ := in.Thread(th)
	assert.Equal(t, Thread{ID: 1, Title: "first", Kind: KindBug, Status: Open, Notes: []NoteID{a}, First: t0, Last: t0}, got)

	require.NoError(t, in.Attach(b, th))
	got, _ = in.Thread(th)
	assert.Equal(t, []NoteID{a, b}, got.Notes)
	assert.Equal(t, t0.Add(5*time.Minute), got.Last)
	nb, _ := in.Note(b)
	assert.Equal(t, Grouped, nb.Grouping)
	assert.Equal(t, th, nb.Thread)

	require.NoError(t, in.Close(th, "", t0))
	before := in.Clone()
	assert.Error(t, in.Attach(c, th), "a closed thread")
	assert.Error(t, in.Attach(c, 42), "an unknown thread")
	assert.Equal(t, before, in, "a refused attach changes nothing")
}

func TestInbox_DuePrefersPendingThenADueRetry(t *testing.T) {
	in := Empty()
	a := in.Add(note(1, "a", 0))
	b := in.Add(note(1, "b", 1))
	in.Fail(a, "boom", t0.Add(10*time.Minute))
	n, ok := in.Due(t0)
	require.True(t, ok)
	assert.Equal(t, b, n.ID, "a pending note beats an ungrouped one")

	_, err := in.OpenThread(b, "")
	require.NoError(t, err)
	_, ok = in.Due(t0)
	assert.False(t, ok, "an ungrouped note waits for its retry")
	n, ok = in.Due(t0.Add(10 * time.Minute))
	require.True(t, ok)
	assert.Equal(t, a, n.ID)
	assert.Equal(t, "boom", n.GroupErr)

	_, ok = Empty().Due(t0)
	assert.False(t, ok)
}

func TestInbox_FailLeavesAGroupedNote(t *testing.T) {
	in := Empty()
	a := in.Add(note(1, "a", 0))
	_, err := in.OpenThread(a, "")
	require.NoError(t, err)
	in.Fail(a, "late", t0)
	n, _ := in.Note(a)
	assert.Equal(t, Grouped, n.Grouping)
}

func TestInbox_OfferedLeavesClosedOut(t *testing.T) {
	in := Empty()
	t1, _ := in.OpenThread(in.Add(note(1, "a", 0)), "")
	t2, _ := in.OpenThread(in.Add(note(1, "b", 0)), "")
	require.NoError(t, in.Close(t1, "", t0))
	offered := in.Offered()
	require.Len(t, offered, 1)
	assert.Equal(t, t2, offered[0].ID)
	assert.Equal(t, 1, OpenThreads(in))
}

func TestInbox_CloseRefusesUnknownAndClosed(t *testing.T) {
	in := Empty()
	th, _ := in.OpenThread(in.Add(note(1, "a", 0)), "")
	assert.EqualError(t, in.Close(7, "", t0), "thread 7 doesn't exist")
	require.NoError(t, in.Close(th, "", t0))
	assert.EqualError(t, in.Close(th, "", t0), "thread 1 is already closed")
}

func TestInbox_SetFixMarksFixing(t *testing.T) {
	in := Empty()
	th, _ := in.OpenThread(in.Add(note(1, "a", 0)), "")
	f := Fix{ID: 77, Title: "fb-1-a", Workspace: "loom", At: t0}
	require.NoError(t, in.SetFix(th, f))
	got, _ := in.Thread(th)
	assert.Equal(t, Fixing, got.Status)
	assert.Equal(t, f, got.Fix)
	assert.Error(t, in.SetFix(9, f))
	require.NoError(t, in.Close(th, "", t0))
	assert.Error(t, in.SetFix(th, f), "a closed thread")
}

func TestInbox_SessionsCountsDistinctSessions(t *testing.T) {
	in := Empty()
	th, _ := in.OpenThread(in.Add(note(1, "a", 0)), "")
	require.NoError(t, in.Attach(in.Add(note(1, "a again", 1)), th))
	require.NoError(t, in.Attach(in.Add(note(2, "a too", 2)), th))
	got, _ := in.Thread(th)
	assert.Equal(t, 2, in.Sessions(got))
}

func TestInbox_CloneIsDeep(t *testing.T) {
	in := Empty()
	_, _ = in.OpenThread(in.Add(note(1, "a", 0)), "")
	c := in.Clone()
	c.Threads[0].Notes[0] = 99
	c.Notes[0].Title = "changed"
	assert.Equal(t, NoteID(1), in.Threads[0].Notes[0])
	assert.Equal(t, "a", in.Notes[0].Title)
}

func TestValidateNote(t *testing.T) {
	long := make([]rune, MaxTitle+1)
	for i := range long {
		long[i] = 'é'
	}
	big := make([]byte, MaxText+1)
	for i := range big {
		big[i] = 'x'
	}
	cases := []struct {
		kind        Kind
		title, text string
		want        string
	}{
		{KindBug, "t", "x", ""},
		{"wrong", "t", "x", `kind "wrong": it must be one of bug, friction, missing, docs`},
		{KindDocs, "  ", "x", "the title is empty"},
		{KindDocs, "two\nlines", "x", "the title must be one line"},
		{KindDocs, string(long), "x", "the title is 121 characters: at most 120"},
		{KindMissing, "t", " \n ", "the text is empty"},
		{KindFriction, "t", string(big), "the text is 20001 bytes: at most 20000"},
	}
	for _, c := range cases {
		err := ValidateNote(c.kind, c.title, c.text)
		if c.want == "" {
			assert.NoError(t, err)
		} else {
			assert.EqualError(t, err, c.want)
		}
	}
}

const loomRepo = "aidan-bailey/loom"

// threadOf opens a thread of n notes from one session and returns its ID and
// the notes' IDs in the order they joined.
func threadOf(t *testing.T, in *Inbox, n int) (ThreadID, []NoteID) {
	t.Helper()
	first := in.Add(note(1, "n", 0))
	th, err := in.OpenThread(first, "")
	require.NoError(t, err)
	ids := []NoteID{first}
	for i := 1; i < n; i++ {
		id := in.Add(note(1, "n", i))
		require.NoError(t, in.Attach(id, th))
		ids = append(ids, id)
	}
	return th, ids
}

func TestInbox_PublishLinksAndMarksPublished(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 2)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[1], t0))
	got, _ := in.Thread(th)
	assert.Equal(t, Published, got.Status)
	assert.Equal(t, Issue{Repo: loomRepo, Number: 57, URL: "https://github.com/aidan-bailey/loom/issues/57", At: t0, Posted: ids[1]}, got.Issue)
}

func TestInbox_PublishKeepsAFixRecord(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 1)
	f := Fix{ID: 77, Title: "fb-1-n", Workspace: "loom", At: t0}
	require.NoError(t, in.SetFix(th, f))
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	got, _ := in.Thread(th)
	assert.Equal(t, Published, got.Status, "a fixing thread becomes published")
	assert.Equal(t, f, got.Fix, "and keeps its fix record")
}

func TestInbox_PublishAgainWithTheSameIssueOnlyAdvancesPosted(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 3)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	require.NoError(t, in.Publish(th, "Aidan-Bailey/loom", 57, ids[2], t0.Add(time.Hour)), "GitHub names are not case sensitive")
	got, _ := in.Thread(th)
	assert.Equal(t, Issue{Repo: loomRepo, Number: 57, URL: "https://github.com/aidan-bailey/loom/issues/57", At: t0, Posted: ids[2]}, got.Issue,
		"the first link's repo, URL and time stay; Posted advances")
	assert.Equal(t, Published, got.Status)
}

func TestInbox_PublishRefusesAnotherIssue(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 2)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	before := in.Clone()
	for name, c := range map[string]struct {
		repo string
		n    int
	}{"another number": {loomRepo, 58}, "another repository": {"aidan-bailey/other", 57}} {
		assert.EqualError(t, in.Publish(th, c.repo, c.n, ids[1], t0.Add(time.Hour)), "thread 1 is linked to aidan-bailey/loom#57", name)
		assert.Equal(t, before, in, name+": a refused publish changes nothing")
	}
}

func TestInbox_PublishRefusesAnEmptyRepoOrANumberBelowOne(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 1)
	before := in.Clone()
	assert.EqualError(t, in.Publish(th, "", 57, ids[0], t0), "the issue repository is empty")
	assert.EqualError(t, in.Publish(th, loomRepo, 0, ids[0], t0), "issue number 0: want at least 1")
	assert.EqualError(t, in.Publish(th, loomRepo, -3, ids[0], t0), "issue number -3: want at least 1")
	assert.Equal(t, before, in, "a refused publish changes nothing: the thread is not marked published with an unlinked Issue")
	require.NoError(t, in.Publish(th, loomRepo, 1, ids[0], t0), "issue 1 is the lowest that links")
}

func TestInbox_PublishRefusesWhatCannotLink(t *testing.T) {
	in := Empty()
	th, _ := threadOf(t, &in, 1)
	_, other := threadOf(t, &in, 1)
	closed, closedIDs := threadOf(t, &in, 1)
	require.NoError(t, in.Close(closed, "", t0))
	before := in.Clone()
	assert.EqualError(t, in.Publish(7, loomRepo, 57, 1, t0), "thread 7 doesn't exist")
	assert.EqualError(t, in.Publish(closed, loomRepo, 57, closedIDs[0], t0), "thread 3 is already closed")
	assert.EqualError(t, in.Publish(th, loomRepo, 57, other[0], t0), "note n2 is not in thread 1", "a note of another thread")
	assert.EqualError(t, in.Publish(th, loomRepo, 57, 99, t0), "note n99 is not in thread 1", "a note that doesn't exist")
	assert.Equal(t, before, in, "refused publishes change nothing")
}

func TestInbox_PublishNeverMovesPostedBack(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 1)
	// Notes 2 and 3 are filed in ID order but join in the order 3, 2: a
	// retried note joins after a newer one. Thread.Notes is [1 3 2].
	x := in.Add(note(1, "x", 5))
	y := in.Add(note(1, "y", 6))
	require.NoError(t, in.Attach(y, th))
	require.NoError(t, in.Attach(x, th))
	got, _ := in.Thread(th)
	require.Equal(t, []NoteID{ids[0], y, x}, got.Notes)

	posted := func() NoteID {
		got, _ := in.Thread(th)
		return got.Issue.Posted
	}
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	assert.Equal(t, ids[0], posted(), "the first publish sets it")
	require.NoError(t, in.Publish(th, loomRepo, 57, y, t0))
	assert.Equal(t, y, posted(), "a later note advances it")
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	assert.Equal(t, y, posted(), "an earlier note never moves it back")
	require.NoError(t, in.Publish(th, loomRepo, 57, x, t0))
	assert.Equal(t, x, posted(), "note 2 joined after note 3, so it is later though its ID is lower")
	require.NoError(t, in.Publish(th, loomRepo, 57, y, t0))
	assert.Equal(t, x, posted(), "and note 3 is now earlier than Posted")
}

func TestInbox_PublishLinksSeveralThreadsToOneIssue(t *testing.T) {
	in := Empty()
	a, aIDs := threadOf(t, &in, 1)
	b, bIDs := threadOf(t, &in, 1)
	require.NoError(t, in.Publish(a, loomRepo, 57, aIDs[0], t0))
	require.NoError(t, in.Publish(b, loomRepo, 57, bIDs[0], t0))
	got, _ := in.Thread(b)
	assert.Equal(t, Published, got.Status)
	assert.Equal(t, 57, got.Issue.Number)
}

func TestInbox_SetFixRefusesAPublishedThread(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 1)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	before := in.Clone()
	assert.EqualError(t, in.SetFix(th, Fix{ID: 9, Title: "fb-1-n", Workspace: "loom", At: t0}),
		"thread 1 is published as aidan-bailey/loom#57; the issue watcher fixes it")
	assert.Equal(t, before, in, "the thread stays published, with no Fix")
	open, _ := threadOf(t, &in, 1)
	require.NoError(t, in.SetFix(open, Fix{ID: 9, Title: "fb-2-n", Workspace: "loom", At: t0}), "an open thread still takes a fix")
}

func TestInbox_CloseStoresTheReason(t *testing.T) {
	in := Empty()
	a, _ := threadOf(t, &in, 1)
	b, _ := threadOf(t, &in, 1)
	require.NoError(t, in.Close(a, "  issue #57 closed (completed)\n", t0.Add(time.Hour)))
	got, _ := in.Thread(a)
	assert.Equal(t, Closed, got.Status)
	assert.Equal(t, Closure{At: t0.Add(time.Hour), Reason: "issue #57 closed (completed)"}, got.Closed, "trimmed, with the time")
	require.NoError(t, in.Close(b, "", t0))
	got, _ = in.Thread(b)
	assert.Equal(t, Closure{At: t0}, got.Closed, "no reason is allowed")
}

func TestInbox_CloseRefusesABadReason(t *testing.T) {
	in := Empty()
	th, _ := threadOf(t, &in, 1)
	before := in.Clone()
	assert.EqualError(t, in.Close(th, "two\nlines", t0), "the close reason must be one line")
	assert.EqualError(t, in.Close(th, "one\rtwo", t0), "the close reason must be one line")
	assert.EqualError(t, in.Close(th, strings.Repeat("x", MaxCloseReason+1), t0), "the close reason is 301 bytes: at most 300")
	assert.Equal(t, before, in, "a refused close changes nothing")
	require.NoError(t, in.Close(th, strings.Repeat("x", MaxCloseReason), t0), "300 bytes fit")
}

func TestInbox_CloseKeepsTheLink(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 1)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[0], t0))
	require.NoError(t, in.Close(th, "no fix wanted", t0.Add(time.Hour)))
	got, _ := in.Thread(th)
	assert.Equal(t, Closed, got.Status)
	assert.Equal(t, 57, got.Issue.Number, "a closed thread still names its issue")
}

func TestInbox_CloseIssueClosesEveryPublishedThreadLinkedToIt(t *testing.T) {
	in := Empty()
	a, aIDs := threadOf(t, &in, 1)
	b, bIDs := threadOf(t, &in, 1)
	c, cIDs := threadOf(t, &in, 1)
	d, dIDs := threadOf(t, &in, 1)
	e, _ := threadOf(t, &in, 1)
	f, fIDs := threadOf(t, &in, 1)
	g, gIDs := threadOf(t, &in, 1)
	linkedBy := t0.Add(30 * time.Minute) // when the issue poll began
	require.NoError(t, in.Publish(a, loomRepo, 57, aIDs[0], t0))
	require.NoError(t, in.Publish(b, loomRepo, 57, bIDs[0], linkedBy), "linked at the moment the poll began: not after it")
	require.NoError(t, in.Publish(c, loomRepo, 58, cIDs[0], t0))
	require.NoError(t, in.Publish(d, loomRepo, 57, dIDs[0], t0))
	require.NoError(t, in.Close(d, "no fix", t0))
	require.NoError(t, in.Publish(f, "aidan-bailey/other", 57, fIDs[0], t0))
	require.NoError(t, in.Publish(g, loomRepo, 57, gIDs[0], linkedBy.Add(time.Second)), "linked during the poll")
	slices.Reverse(in.Threads) // the slice holds threads in descending ID order, so only a sort puts the result by ID

	later := t0.Add(time.Hour)
	got := in.CloseIssue("Aidan-Bailey/loom", 57, "issue #57 closed (completed)", linkedBy, later)
	assert.Equal(t, []ThreadID{a, b}, got, "the open links to that issue, by ID")
	for _, id := range got {
		th, _ := in.Thread(id)
		assert.Equal(t, Closed, th.Status)
		assert.Equal(t, Closure{At: later, Reason: "issue #57 closed (completed)"}, th.Closed)
		assert.Equal(t, 57, th.Issue.Number, "the link stays")
	}
	th, _ := in.Thread(c)
	assert.Equal(t, Published, th.Status, "another issue")
	th, _ = in.Thread(d)
	assert.Equal(t, Closure{At: t0, Reason: "no fix"}, th.Closed, "a thread closed earlier keeps its own reason")
	th, _ = in.Thread(e)
	assert.Equal(t, Open, th.Status, "an unlinked thread")
	th, _ = in.Thread(f)
	assert.Equal(t, Published, th.Status, "the same number in another repository")
	assert.Equal(t, Closure{}, th.Closed)
	th, _ = in.Thread(g)
	assert.Equal(t, Published, th.Status, "a thread linked after the poll began stays published")
	assert.Equal(t, Closure{}, th.Closed)
	assert.Equal(t, []ThreadID{g}, in.CloseIssue(loomRepo, 57, "issue #57 closed (completed)", later, later), "the next poll, which began later, closes it")
	assert.Empty(t, in.CloseIssue(loomRepo, 57, "again", later, later), "nothing left to close")
}

func TestInbox_PublishedListsOnlyPublishedThreadsByID(t *testing.T) {
	in := Empty()
	_, _ = threadOf(t, &in, 1) // open
	b, bIDs := threadOf(t, &in, 1)
	c, _ := threadOf(t, &in, 1)
	d, dIDs := threadOf(t, &in, 1)
	e, eIDs := threadOf(t, &in, 1)
	require.NoError(t, in.SetFix(c, Fix{ID: 9, Title: "fb-3-n", Workspace: "loom", At: t0})) // fixing, not published
	require.NoError(t, in.Publish(d, loomRepo, 60, dIDs[0], t0))
	require.NoError(t, in.Publish(b, loomRepo, 59, bIDs[0], t0))
	require.NoError(t, in.Publish(e, loomRepo, 61, eIDs[0], t0))
	require.NoError(t, in.Close(e, "", t0)) // published, then closed
	slices.Reverse(in.Threads)              // descending ID order in the slice, so only a sort puts the result by ID

	var ids []ThreadID
	for _, th := range in.Published() {
		ids = append(ids, th.ID)
	}
	assert.Equal(t, []ThreadID{b, d}, ids, "not open, fixing or closed ones; by ID though the slice holds them in descending order")
	assert.Empty(t, Empty().Published())
}
