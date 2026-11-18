package ui

import (
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
)

// The spinner's tick redraws the whole TUI twelve times a second, so it runs
// only while a card on screen shows it: a Running or Loading row inside the
// rail's visible window.
func TestList_DrawsSpinnerOnlyForAVisibleRunningRow(t *testing.T) {
	l, src := newPageNavList(6) // three cards fit; all Running
	for i := range src.items {
		src.items[i].Status = session.Ready
	}
	if l.DrawsSpinner() {
		t.Fatal("no running row, yet the rail draws a spinner")
	}

	src.items[5].Status = session.Running // below the window
	if l.DrawsSpinner() {
		t.Fatal("a running row scrolled out of the window counts as drawn")
	}

	src.items[1].Status = session.Loading
	if !l.DrawsSpinner() {
		t.Fatal("a Loading row in the window draws the spinner")
	}

	src.items[1].Status = session.Ready
	l.SelectID(src.items[5].ID) // scrolls the running row into view
	if !l.DrawsSpinner() {
		t.Fatal("a Running row in the window draws the spinner")
	}
}

// ShowsSpinner is what the app's tick goes by, so it must name exactly the
// statuses whose card label draws the spinner: one missing from it freezes
// that card's spinner, an extra one ticks for nothing.
func TestShowsSpinner_MatchesTheCardLabel(t *testing.T) {
	const frame = "⣾"
	for s := session.Running; s <= session.Recoverable; s++ {
		drawn := strings.Contains(CardData{Status: s, Spinner: frame}.statusLabel(), frame)
		if drawn != ShowsSpinner(s) {
			t.Errorf("status %v: the card draws the spinner %v, ShowsSpinner says %v", s, drawn, ShowsSpinner(s))
		}
	}
}
