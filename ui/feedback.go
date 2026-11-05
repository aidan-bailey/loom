package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/charmbracelet/x/ansi"
)

// FeedbackData is what the feedback view renders (session feedback spec
// §9): the published inbox, the selected row (an index into Rows), whether
// it is expanded to its notes, and the time.
type FeedbackData struct {
	View     core.FeedbackView
	Selected int
	Expanded bool
	Now      time.Time
}

// FeedbackRows is the view's rows: the ranked list `loom feedback list`
// prints, closed threads left out.
func FeedbackRows(v core.FeedbackView) []feedback.Row { return feedback.Rank(v.Inbox, false) }

// The fixed columns of a feedback row; the title takes the rest.
const (
	fbIDW       = 5
	fbKindW     = 9
	fbSessionsW = 4
	fbNotesW    = 5
	fbLastW     = 8
	fbStatusW   = 20
)

// fbNoteLines is how many lines of a note's text an expanded row shows.
const fbNoteLines = 3

// RenderFeedback is the feedback view: a header with the open threads'
// count, the grouper's state or the inbox's error, the ranked rows (the
// selected one in the selection colors), and under the selected row, when
// expanded, its notes: who filed each, on what, when, its title and its
// text's first lines. It fits width × height.
func RenderFeedback(width, height int, d FeedbackData) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	v := d.View
	left := fmt.Sprintf(" Feedback · %d open", feedback.OpenThreads(v.Inbox))
	out := []string{workTitleStyle.Render(left) + workRightHint(width, left, "F/esc back ")}
	switch {
	case v.Err != "":
		out = append(out, " "+workErrStyle.Render("inbox: "+CleanText(v.Err)))
	case v.Grouper != "":
		out = append(out, " "+workDimStyle.Render("grouper off: "+CleanText(v.Grouper)))
	}
	rows := FeedbackRows(v)
	if len(rows) == 0 {
		out = append(out, " "+workDimStyle.Render("No feedback yet. Sessions file it with loom feedback add."))
		return workBox(out, width, height)
	}
	titleW := max(8, width-3-fbIDW-fbKindW-fbSessionsW-fbNotesW-fbLastW-fbStatusW-12)
	out = append(out, workSectionStyle.Render(fbCells("   ", "ID", "KIND", "SESS", "NOTES", "LAST", "STATUS", "TITLE", titleW)))

	sel := max(0, min(d.Selected, len(rows)-1))
	var lines []string
	selLine := 0
	for i, r := range rows {
		if i == sel {
			selLine = len(lines)
		}
		lines = append(lines, fbRow(r, titleW, d.Now, i == sel))
		if i == sel && d.Expanded {
			lines = append(lines, fbNotes(v.Inbox, r, width, d.Now)...)
		}
	}
	out = append(out, workWindow(lines, selLine, height-len(out))...)
	return workBox(out, width, height)
}

// fbCells lays out one row's cells, fitted to their columns.
func fbCells(marker, id, kind, sessions, notes, last, status, title string, titleW int) string {
	return workFit(marker, 3) + workFit(id, fbIDW) + "  " + workFit(kind, fbKindW) + "  " + workFit(sessions, fbSessionsW) + "  " +
		workFit(notes, fbNotesW) + "  " + workFit(last, fbLastW) + "  " + workFit(status, fbStatusW) + "  " + workFit(title, titleW)
}

// fbRow draws one row: a thread by its number, a note no thread holds by
// n and its number.
func fbRow(r feedback.Row, titleW int, now time.Time, selected bool) string {
	marker := "   "
	if selected {
		marker = " ▸ "
	}
	id := "n" + strconv.Itoa(int(r.Note))
	status := string(r.Grouping)
	if r.Thread != 0 {
		id, status = strconv.Itoa(int(r.Thread)), string(r.Status)
		if r.Status == feedback.Fixing && r.Fix.Title != "" {
			status = "fixing " + r.Fix.Title
		}
	}
	line := fbCells(marker, id, string(r.Kind), strconv.Itoa(r.Sessions), strconv.Itoa(r.Notes),
		workAgo(now.Sub(r.Last)), CleanText(status), CleanText(r.Title), titleW)
	if selected {
		return workSelectedStyle.Render(line)
	}
	return line
}

// fbNotes are an expanded row's notes: a thread's every note, or the
// note itself.
func fbNotes(in feedback.Inbox, r feedback.Row, width int, now time.Time) []string {
	ids := []feedback.NoteID{r.Note}
	if t, ok := in.Thread(r.Thread); ok && r.Thread != 0 {
		ids = t.Notes
	}
	var out []string
	for _, id := range ids {
		n, ok := in.Note(id)
		if !ok {
			continue
		}
		head := fmt.Sprintf("n%d %s · %s in %s · %s", n.ID, n.Kind, n.From.Title, n.From.Workspace, workAgo(now.Sub(n.At)))
		if me := feedback.ModelEffort(n.From); me != "" {
			head += " · " + me
		}
		if n.Build != "" {
			head += " · loom " + n.Build
		}
		out = append(out, "      "+workDimStyle.Render(ansi.Truncate(CleanText(head), max(0, width-6), "…")))
		out = append(out, "      "+ansi.Truncate(CleanText(n.Title), max(0, width-6), "…"))
		text := strings.Split(strings.TrimSpace(n.Text), "\n")
		for i, l := range text {
			if i == fbNoteLines {
				out = append(out, "        "+workDimStyle.Render(fmt.Sprintf("… %d more lines (loom feedback show n%d)", len(text)-fbNoteLines, n.ID)))
				break
			}
			out = append(out, "        "+workDimStyle.Render(ansi.Truncate(CleanText(l), max(0, width-8), "…")))
		}
	}
	return out
}
