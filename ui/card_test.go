package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
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

// assertSolidBg walks a raw (un-stripped) rendered line and fails if
// any visible rune is emitted while the given background SGR sub-
// sequence is not active. SGR state machine: a sequence containing
// bgSeq turns the background on; a bare reset (\x1b[m / \x1b[0m) turns
// it off; other sequences (fg, bold) leave it unchanged.
func assertSolidBg(t *testing.T, line, bgSeq string) {
	t.Helper()
	bg := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				t.Fatalf("unterminated SGR sequence in %q", line)
			}
			params := line[i+2 : i+end]
			switch {
			case params == "" || params == "0":
				bg = false
			case strings.Contains(params, bgSeq):
				bg = true
			}
			i += end + 1
			continue
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		if !bg {
			t.Fatalf("visible rune %q at byte %d has no active background in %q", r, i, line)
		}
		i += sz
	}
}

func TestRenderCard_SelectedRailBackgroundIsSolid(t *testing.T) {
	ApplyTheme(DefaultThemeName) // pin a known Panel color
	r, g, b, _ := Panel.RGBA()
	bgSeq := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
	d := CardData{Title: "sel", Index: 1, Status: session.Ready, Selected: true}
	out := RenderCard(d, DensityRail, 20) // raw: do NOT strip ANSI
	for _, line := range strings.Split(out, "\n") {
		assertSolidBg(t, line, bgSeq)
	}
}

func TestRenderCard_AttentionBeatsTail(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Prompting,
		TailLines: []string{"some tail"}}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.Contains(t, out, "awaiting input")
	assert.NotContains(t, out, "some tail")
}

func TestTailLines_SanitizesControlChars(t *testing.T) {
	assert.Equal(t, []string{"col1 col2"}, TailLines("col1\tcol2\r\n", 3))
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{-time.Second, ""},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{time.Hour, "1h"},
		{time.Hour + 12*time.Minute, "1h12m"},
		{72 * time.Hour, "3d"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, formatAge(c.d), "formatAge(%v)", c.d)
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 3), "exact fit is untouched")
	assert.Equal(t, "ab…", truncate("abcd", 3), "one over: ellipsis fits within budget")
	assert.Equal(t, "日…", truncate("日本語", 4), "wide runes")
	assert.Equal(t, "a", truncate("abcd", 1), "tiny width hard-cuts without ellipsis")
	assert.Equal(t, "", truncate("abcd", 0))
	for _, w := range []int{1, 2, 3, 4, 5} {
		assert.LessOrEqual(t, runewidth.StringWidth(truncate("日本語です", w)), w)
	}
}

func TestSortForOverview_WorkspaceTerminalPinnedFirst(t *testing.T) {
	items := []*session.Instance{
		{Title: "wt", Status: session.Running, IsWorkspaceTerminal: true},
		{Title: "a", Status: session.Prompting},
	}
	order := SortForOverview(items)
	assert.Equal(t, 0, order[0], "workspace terminal stays pinned at display position 0")
}
