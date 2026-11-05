package feedback

import "time"

// t0 is the tests' clock.
var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// note is a note from session sess at t0 plus min minutes.
func note(sess uint64, title string, min int) Note {
	return Note{Kind: KindBug, Title: title, Text: title + ": what happened", From: Session{ID: sess, Title: "s", Workspace: "loom"}, At: t0.Add(time.Duration(min) * time.Minute)}
}
