package feedback

import (
	"testing"

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
	require.NoError(t, in.Close(closed))
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
	assert.Contains(t, all, Row{Thread: closed, Kind: KindBug, Title: "closed", Status: Closed, Sessions: 1, Notes: 1, Last: t0.Add(40 * 60e9)})
}
