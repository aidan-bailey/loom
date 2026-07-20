package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func plain(s string) string { return ansi.Strip(s) }

func TestTailLines_TrimsBlanksAndStripsANSI(t *testing.T) {
	screen := "one\n\x1b[31mtwo\x1b[0m\nthree\n\n   \n"
	assert.Equal(t, []string{"two", "three"}, TailLines(screen, 2))
	assert.Equal(t, []string{"one", "two", "three"}, TailLines(screen, 5))
	assert.Nil(t, TailLines("", 3))
	assert.Nil(t, TailLines("\n \n", 3))
}

func TestRenderCard_RailShowsTitleAndTail(t *testing.T) {
	d := CardData{Title: "auth-refactor", Index: 1, Status: session.Running,
		TailLines: []string{"✻ running tests"}, Spinner: "⠋"}
	out := plain(RenderCard(d, DensityRail, 30))
	lines := strings.Split(out, "\n")
	assert.Len(t, lines, 2)
	assert.Contains(t, lines[0], "auth-refactor")
	assert.Contains(t, lines[1], "✻ running tests")
	for _, l := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(l), 30)
	}
}

func TestRenderCard_LineDensityIsOneLine(t *testing.T) {
	d := CardData{Title: "auth-refactor", Index: 2, Status: session.Ready}
	out := plain(RenderCard(d, DensityLine, 30))
	assert.NotContains(t, out, "\n")
	assert.Contains(t, out, "auth-refactor")
}

func TestRenderCard_PromptingShowsWaitAge(t *testing.T) {
	d := CardData{Title: "db-migration", Index: 1, Status: session.Prompting,
		StatusAge: 4 * time.Minute}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.Contains(t, out, "4m")
}

func TestRenderCard_TruncatesLongTitles(t *testing.T) {
	d := CardData{Title: strings.Repeat("x", 60), Index: 1, Status: session.Ready}
	out := plain(RenderCard(d, DensityRail, 20))
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(l), 20)
	}
}

func TestSortForOverview_AttentionFirstStable(t *testing.T) {
	mk := func(title string, st session.Status) *session.Instance {
		return &session.Instance{Title: title, Status: st}
	}
	items := []*session.Instance{
		mk("d-paused", session.Paused),
		mk("b-running", session.Running),
		mk("a-prompting", session.Prompting),
		mk("c-ready", session.Ready),
		mk("e-prompting", session.Prompting),
	}
	order := SortForOverview(items)
	titles := make([]string, len(order))
	for i, idx := range order {
		titles[i] = items[idx].Title
	}
	assert.Equal(t, []string{"a-prompting", "e-prompting", "b-running", "c-ready", "d-paused"}, titles)
}

func TestSortForOverview_WorkspaceTerminalPinnedFirst(t *testing.T) {
	items := []*session.Instance{
		{Title: "wt", Status: session.Running, IsWorkspaceTerminal: true},
		{Title: "a", Status: session.Prompting},
	}
	order := SortForOverview(items)
	assert.Equal(t, 0, order[0], "workspace terminal stays pinned at display position 0")
}
