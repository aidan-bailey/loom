package feedback

import (
	"cmp"
	"slices"
	"time"
)

// Row is one line of the inbox's list: a thread, or a note no thread holds
// yet (Thread 0).
type Row struct {
	Thread   ThreadID  `json:"thread,omitempty"`
	Note     NoteID    `json:"note,omitempty"` // a row that is a note
	Kind     Kind      `json:"kind"`
	Title    string    `json:"title"`
	Status   Status    `json:"status,omitempty"`   // a thread's
	Grouping Grouping  `json:"grouping,omitempty"` // a note's
	Sessions int       `json:"sessions"`
	Notes    int       `json:"notes"`
	Last     time.Time `json:"last"`
	Fix      Fix       `json:"fix,omitzero"`
}

// Rank is the list (spec §8): every thread not closed (with all, the
// closed ones too) and every note no thread holds, by distinct sessions,
// then the latest note, newest first, then ID.
func Rank(in Inbox, all bool) []Row {
	var rows []Row
	for _, t := range in.Threads {
		if t.Status == Closed && !all {
			continue
		}
		rows = append(rows, Row{Thread: t.ID, Kind: t.Kind, Title: t.Title, Status: t.Status,
			Sessions: in.Sessions(t), Notes: len(t.Notes), Last: t.Last, Fix: t.Fix})
	}
	for _, n := range in.Notes {
		if n.Thread == 0 {
			rows = append(rows, Row{Note: n.ID, Kind: n.Kind, Title: n.Title, Grouping: n.Grouping, Sessions: 1, Notes: 1, Last: n.At})
		}
	}
	slices.SortStableFunc(rows, func(a, b Row) int {
		return cmp.Or(cmp.Compare(b.Sessions, a.Sessions), b.Last.Compare(a.Last),
			cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Note, b.Note))
	})
	return rows
}

// OpenThreads counts the threads that are not closed.
func OpenThreads(in Inbox) int { return len(in.Offered()) }
