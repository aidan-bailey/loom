package feedback

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRank_BySessionsThenLatest(t *testing.T) {
	in := Empty()
	// Thread 1: five notes, one session.
	chatty, _ := in.OpenThread(in.Add(note(1, "chatty", 0)), "")
	for i := 1; i < 5; i++ {
		require.NoError(t, in.Attach(in.Add(note(1, "chatty", i)), chatty))
	}
	// Thread 2: three notes, three sessions.
	shared, _ := in.OpenThread(in.Add(note(2, "shared", 0)), "")
	require.NoError(t, in.Attach(in.Add(note(3, "shared", 1)), shared))
	require.NoError(t, in.Attach(in.Add(note(4, "shared", 2)), shared))
	// Thread 3: one session, newer than thread 1.
	newer, _ := in.OpenThread(in.Add(note(5, "newer", 30)), "")
	// Thread 4: closed.
	closed, _ := in.OpenThread(in.Add(note(6, "closed", 40)), "")
	require.NoError(t, in.Close(closed, "", t0))
	// An ungrouped note.
	loose := in.Add(note(7, "loose", 20))

	rows := Rank(in, false)
	var order []any
	for _, r := range rows {
		if r.Thread != 0 {
			order = append(order, r.Thread)
		} else {
			order = append(order, r.Note)
		}
	}
	assert.Equal(t, []any{shared, newer, loose, chatty}, order)
	assert.Equal(t, Row{Thread: shared, Kind: KindBug, Title: "shared", Status: Open, Sessions: 3, Notes: 3, Last: t0.Add(2 * 60e9)}, rows[0])
	assert.Equal(t, Pending, rows[2].Grouping)

	all := Rank(in, true)
	assert.Len(t, all, 5)
	assert.Contains(t, all, Row{Thread: closed, Kind: KindBug, Title: "closed", Status: Closed, Closed: Closure{At: t0}, Sessions: 1, Notes: 1, Last: t0.Add(40 * 60e9)})
}

func TestRank_RowsCarryTheLinkTheClosureAndNewNotes(t *testing.T) {
	in := Empty()
	th, ids := threadOf(t, &in, 3)
	require.NoError(t, in.Publish(th, loomRepo, 57, ids[1], t0))
	rows := Rank(in, false)
	require.Len(t, rows, 1)
	assert.Equal(t, Published, rows[0].Status)
	assert.Equal(t, Issue{Repo: loomRepo, Number: 57, URL: IssueURL(loomRepo, 57), At: t0, Posted: ids[1]}, rows[0].Issue)
	assert.Equal(t, 1, rows[0].New, "the third note joined after the one posted through")
	assert.Equal(t, "published #57 +1 new", StatusText(rows[0]))

	in.CloseIssue(loomRepo, 57, "issue #57 closed (completed)", t0, t0.Add(time.Hour))
	assert.Empty(t, Rank(in, false), "closed threads stay out of the default list")
	all := Rank(in, true)
	require.Len(t, all, 1)
	assert.Equal(t, Closure{At: t0.Add(time.Hour), Reason: "issue #57 closed (completed)"}, all[0].Closed)
	assert.Equal(t, 57, all[0].Issue.Number, "a closed row still names its issue")
	assert.Zero(t, all[0].New, "a closed thread has no new notes")
}

func TestRow_JSONOmitsTheLinkFieldsWhenZero(t *testing.T) {
	plain, err := json.Marshal(Row{Thread: 1, Kind: KindBug, Title: "t", Status: Open})
	require.NoError(t, err)
	for _, key := range []string{`"issue"`, `"closed"`, `"new"`} {
		assert.NotContains(t, string(plain), key)
	}
	linked, err := json.Marshal(Row{Thread: 1, Kind: KindBug, Title: "t", Status: Published, Issue: Issue{Repo: loomRepo, Number: 57}, New: 2})
	require.NoError(t, err)
	assert.Contains(t, string(linked), `"issue":{"repo":"aidan-bailey/loom","number":57,`)
	assert.Contains(t, string(linked), `"new":2`)
}
