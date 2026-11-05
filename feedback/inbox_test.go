package feedback

import (
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

	require.NoError(t, in.Close(th))
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
	require.NoError(t, in.Close(t1))
	offered := in.Offered()
	require.Len(t, offered, 1)
	assert.Equal(t, t2, offered[0].ID)
	assert.Equal(t, 1, OpenThreads(in))
}

func TestInbox_CloseRefusesUnknownAndClosed(t *testing.T) {
	in := Empty()
	th, _ := in.OpenThread(in.Add(note(1, "a", 0)), "")
	assert.EqualError(t, in.Close(7), "thread 7 doesn't exist")
	require.NoError(t, in.Close(th))
	assert.EqualError(t, in.Close(th), "thread 1 is already closed")
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
	require.NoError(t, in.Close(th))
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
