package feedback

import (
	"fmt"
	"slices"
	"time"
)

// Inbox is feedback.json: every note and thread, and the counters.
type Inbox struct {
	Version    int      `json:"version"`
	NextNote   NoteID   `json:"next_note"`
	NextThread ThreadID `json:"next_thread"`
	Notes      []Note   `json:"notes"`
	Threads    []Thread `json:"threads"`
}

// Empty is an inbox with nothing in it.
func Empty() Inbox { return Inbox{Version: Version, NextNote: 1, NextThread: 1} }

// Clone deep-copies in.
func (in Inbox) Clone() Inbox {
	in.Notes = slices.Clone(in.Notes)
	in.Threads = slices.Clone(in.Threads)
	for i := range in.Threads {
		in.Threads[i].Notes = slices.Clone(in.Threads[i].Notes)
	}
	return in
}

func (in *Inbox) noteAt(id NoteID) int {
	return slices.IndexFunc(in.Notes, func(n Note) bool { return n.ID == id })
}

func (in *Inbox) threadAt(id ThreadID) int {
	return slices.IndexFunc(in.Threads, func(t Thread) bool { return t.ID == id })
}

// Note is note id.
func (in Inbox) Note(id NoteID) (Note, bool) {
	if i := in.noteAt(id); i >= 0 {
		return in.Notes[i], true
	}
	return Note{}, false
}

// Thread is thread id.
func (in Inbox) Thread(id ThreadID) (Thread, bool) {
	if i := in.threadAt(id); i >= 0 {
		return in.Threads[i], true
	}
	return Thread{}, false
}

// Add files n under the next ID, pending, in no thread, and returns the ID.
func (in *Inbox) Add(n Note) NoteID {
	n.ID = max(in.NextNote, 1)
	in.NextNote = n.ID + 1
	n.Thread, n.Grouping, n.RetryAt, n.GroupErr = 0, Pending, time.Time{}, ""
	in.Notes = append(in.Notes, n)
	return n.ID
}

// Attach puts note n in thread t, which must exist and not be closed.
func (in *Inbox) Attach(n NoteID, t ThreadID) error {
	i, j := in.noteAt(n), in.threadAt(t)
	if i < 0 || j < 0 || in.Threads[j].Status == Closed {
		return fmt.Errorf("attach note %d to thread %d: no such note or open thread", n, t)
	}
	note := &in.Notes[i]
	note.Thread, note.Grouping, note.RetryAt, note.GroupErr = t, Grouped, time.Time{}, ""
	th := &in.Threads[j]
	th.Notes = append(th.Notes, n)
	if note.At.After(th.Last) {
		th.Last = note.At
	}
	return nil
}

// OpenThread opens a thread for note n titled title (n's own title when
// empty) and puts n in it.
func (in *Inbox) OpenThread(n NoteID, title string) (ThreadID, error) {
	i := in.noteAt(n)
	if i < 0 {
		return 0, fmt.Errorf("open a thread for note %d: no such note", n)
	}
	note := in.Notes[i]
	t := max(in.NextThread, 1)
	in.NextThread = t + 1
	if title == "" {
		title = note.Title
	}
	in.Threads = append(in.Threads, Thread{ID: t, Title: title, Kind: note.Kind, Status: Open, First: note.At, Last: note.At})
	return t, in.Attach(n, t)
}

// Fail records a failed grouping of n: ungrouped, why, retried at retryAt.
// A grouped note is left as it is.
func (in *Inbox) Fail(n NoteID, why string, retryAt time.Time) {
	if i := in.noteAt(n); i >= 0 && in.Notes[i].Grouping != Grouped {
		in.Notes[i].Grouping, in.Notes[i].GroupErr, in.Notes[i].RetryAt = Ungrouped, why, retryAt
	}
}

// Close closes thread t; an unknown or closed thread is an error.
func (in *Inbox) Close(t ThreadID) error {
	j := in.threadAt(t)
	switch {
	case j < 0:
		return fmt.Errorf("thread %d doesn't exist", t)
	case in.Threads[j].Status == Closed:
		return fmt.Errorf("thread %d is already closed", t)
	}
	in.Threads[j].Status = Closed
	return nil
}

// SetFix records thread t's fix session and marks it fixing; an unknown or
// closed thread is an error.
func (in *Inbox) SetFix(t ThreadID, f Fix) error {
	j := in.threadAt(t)
	if j < 0 || in.Threads[j].Status == Closed {
		return fmt.Errorf("thread %d doesn't exist or is closed", t)
	}
	in.Threads[j].Status, in.Threads[j].Fix = Fixing, f
	return nil
}

// Due is the next note to group at now: the lowest pending note, else the
// lowest ungrouped one whose retry time has come.
func (in Inbox) Due(now time.Time) (Note, bool) {
	retry := -1
	for i, n := range in.Notes {
		switch {
		case n.Grouping == Pending:
			return n, true
		case n.Grouping == Ungrouped && !now.Before(n.RetryAt) && retry < 0:
			retry = i
		}
	}
	if retry >= 0 {
		return in.Notes[retry], true
	}
	return Note{}, false
}

// Offered is every thread the grouper may attach to: those not closed.
func (in Inbox) Offered() []Thread {
	var out []Thread
	for _, t := range in.Threads {
		if t.Status != Closed {
			out = append(out, t)
		}
	}
	return out
}

// Sessions is how many distinct sessions filed thread t's notes.
func (in Inbox) Sessions(t Thread) int {
	seen := map[uint64]bool{}
	for _, id := range t.Notes {
		if n, ok := in.Note(id); ok {
			seen[n.From.ID] = true
		}
	}
	return len(seen)
}
