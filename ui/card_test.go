package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/core"
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
	var nextID core.InstanceID
	mk := func(title string, st session.Status) core.InstanceView {
		nextID++
		return core.InstanceView{ID: nextID, Title: title, Status: st}
	}
	items := []core.InstanceView{
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

func TestSortForOverview_DeletingWithBellSortsLast(t *testing.T) {
	deleting := core.InstanceView{ID: 1, Title: "a-deleting", Status: session.Deleting}
	deleting.Bell = true // stale bell must not float a mid-kill card into the attention tier
	items := []core.InstanceView{
		deleting,
		{ID: 2, Title: "b-ready", Status: session.Ready},
	}
	order := SortForOverview(items)
	assert.Equal(t, "b-ready", items[order[0]].Title)
	assert.Equal(t, "a-deleting", items[order[1]].Title)

	d := CardData{Title: "a-deleting", Status: session.Deleting, BellPending: true}
	assert.False(t, d.NeedsAttention(), "Deleting card must never carry the attention accent")
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
	items := []core.InstanceView{
		{ID: 1, Title: "wt", Status: session.Running, IsWorkspaceTerminal: true},
		{ID: 2, Title: "a", Status: session.Prompting},
	}
	order := SortForOverview(items)
	assert.Equal(t, 0, order[0], "workspace terminal stays pinned at display position 0")
}

// TestRenderCard_PromptingShowsWaitReason: "awaiting input" only repeats
// what the gold accent already said. When Claude names the block, the card
// shows that instead — it is the part the user would otherwise have to
// attach to discover.
func TestRenderCard_PromptingShowsWaitReason(t *testing.T) {
	d := CardData{Title: "db-migration", Index: 1, Status: session.Prompting,
		WaitReason: "sandbox request", StatusAge: 4 * time.Minute}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.Contains(t, out, "sandbox request")
	assert.Contains(t, out, "4m", "the wait age stays alongside the reason")
	assert.NotContains(t, out, "awaiting input",
		"the reason replaces the generic phrase rather than joining it")
}

// TestRenderCard_PromptingWithoutReasonKeepsGenericLabel: non-Claude agents
// and a roster-less fallback have no reason to show, and must read exactly
// as they did before.
func TestRenderCard_PromptingWithoutReasonKeepsGenericLabel(t *testing.T) {
	d := CardData{Title: "db-migration", Index: 1, Status: session.Prompting,
		StatusAge: 4 * time.Minute}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.Contains(t, out, "awaiting input")
}

// TestRenderCard_WaitReasonIgnoredWhenNotPrompting: the reason is only
// meaningful for a live block; a stale one on a Running card must not paint.
func TestRenderCard_WaitReasonIgnoredWhenNotPrompting(t *testing.T) {
	d := CardData{Title: "db-migration", Index: 1, Status: session.Running,
		WaitReason: "sandbox request"}
	out := plain(RenderCard(d, DensityRail, 40))
	assert.NotContains(t, out, "sandbox request")
}

// TestRenderCard_LongWaitReasonStaysInBounds: reasons come verbatim from
// the CLI, so an unfamiliar long one must not overflow a narrow rail.
func TestRenderCard_LongWaitReasonStaysInBounds(t *testing.T) {
	d := CardData{Title: "db", Index: 1, Status: session.Prompting,
		WaitReason: strings.Repeat("reason ", 12), StatusAge: time.Minute}
	out := plain(RenderCard(d, DensityRail, 24))
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(l), 24)
	}
}

// withJobBadges publishes badges for the test and clears them at cleanup.
func withJobBadges(t *testing.T, b map[core.InstanceID]string) {
	t.Helper()
	SetJobBadges(b)
	t.Cleanup(func() { SetJobBadges(nil) })
}

// railFirstLine is the rail card's title line, styling stripped.
func railFirstLine(d CardData, width int) string {
	return ansi.Strip(strings.Split(RenderCard(d, DensityRail, width), "\n")[0])
}

func TestBuildCardData_CarriesTheJobBadge(t *testing.T) {
	withJobBadges(t, map[core.InstanceID]string{7: "⚙ job 14 +1"})

	with := BuildCardData(core.InstanceView{ID: 7, Title: "t", Status: session.Ready}, Pane{}, false, "", 0)
	assert.Equal(t, "⚙ job 14 +1", with.JobBadge)

	without := BuildCardData(core.InstanceView{ID: 8, Title: "t", Status: session.Ready}, Pane{}, false, "", 0)
	assert.Equal(t, "", without.JobBadge, "a session the map does not name has no badge")

	SetJobBadges(nil)
	assert.Equal(t, "", JobBadge(7), "clearing the map clears every badge")
}

func TestRenderCard_RailShowsTheJobBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: "⚙ job 14 +1"}
	out := RenderCard(d, DensityRail, 40)
	first := railFirstLine(d, 40)
	assert.Contains(t, first, "1. fix-login")
	assert.Contains(t, first, "⚙ job 14 +1")
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(line), 40)
	}

	d.JobBadge = ""
	assert.NotContains(t, railFirstLine(d, 40), "⚙", "no badge, no token")
}

// TestRenderCard_JobBadgeDecisionIsWidthOnly pins that, at a given card
// width, every badge text within the cap makes the same call: the decision
// reserves railJobBadgeMaxWidth, never the badge's own width.
func TestRenderCard_JobBadgeDecisionIsWidthOnly(t *testing.T) {
	badges := []string{"⚙ queued", "⚙ job 14", "⚙ job 14 +12"}
	wantShown := map[int]bool{20: false, 32: false, 33: true, 40: true}
	for w, want := range wantShown {
		for _, b := range badges {
			d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: b}
			first := railFirstLine(d, w)
			assert.Equal(t, want, strings.Contains(first, "⚙"), "width %d, badge %q", w, b)
		}
	}
}

// TestRenderCard_LongJobBadgeIsDroppedWhole pins the cap: a badge wider than
// railJobBadgeMaxWidth is dropped, never cut (a cut "+9" could read as "+").
func TestRenderCard_LongJobBadgeIsDroppedWhole(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: "⚙ job 123456789012 +9"}
	first := railFirstLine(d, 60)
	assert.Contains(t, first, "1. fix-login")
	assert.NotContains(t, first, "⚙")
}

// TestRenderCard_JobBadgeTakesRoomFromTheAccountBadge pins the order of
// precedence when both want the title line's right edge: the job badge
// stays and the account badge goes first, and the account still shows on
// the same card at the same width once the job ends.
func TestRenderCard_JobBadgeTakesRoomFromTheAccountBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2", JobBadge: "⚙ job 14"}

	tight := railFirstLine(d, 40)
	assert.Contains(t, tight, "⚙ job 14")
	assert.NotContains(t, tight, "@max-2", "no room for both at 40")

	roomy := railFirstLine(d, 44)
	assert.Contains(t, roomy, "⚙ job 14")
	assert.Contains(t, roomy, "@max-2")
	assert.Less(t, strings.Index(roomy, "⚙"), strings.Index(roomy, "@"), "the job badge sits left of the account's")

	d.JobBadge = ""
	assert.Contains(t, railFirstLine(d, 40), "@max-2", "without a job the account shows at 40")
}

func TestRenderCard_SelectedRailJobBadgeStaysInsideTheCard(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Selected: true,
		Account: "max-2", JobBadge: "⚙ job 14 +2"}
	for _, w := range []int{33, 44, 60} {
		for _, line := range strings.Split(RenderCard(d, DensityRail, w), "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(line), w, "width %d", w)
		}
	}
}

func TestRenderOverviewCard_ShowsTheJobBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Branch: "aidanb/fix", Status: session.Running, JobBadge: "⚙ queued"}
	assert.Contains(t, ansi.Strip(renderOverviewCard(d, 50)), "⚙ queued")

	d.JobBadge = ""
	assert.NotContains(t, ansi.Strip(renderOverviewCard(d, 50)), "⚙")
}

// TestRenderOverviewCard_JobBadgeOutlastsTheAccountOnTheMetaLine pins the
// ordering: the job badge is the meta line's first token, so the right-hand
// truncation drops the account (the last) before it.
func TestRenderOverviewCard_JobBadgeOutlastsTheAccountOnTheMetaLine(t *testing.T) {
	d := CardData{
		Title: "fix-login", Branch: "aidanb/fix-login-flow", Status: session.Running,
		Account: "max-2", JobBadge: "⚙ job 14",
		HasParity: true, Ahead: 2, Behind: 1,
		HasDiff: true, DiffAdded: 3, DiffRemoved: 1,
	}
	wide := ansi.Strip(renderOverviewCard(d, 40))
	assert.Contains(t, wide, "⚙ job 14")
	assert.Contains(t, wide, "@max-2")

	narrow := ansi.Strip(renderOverviewCard(d, 20))
	assert.Contains(t, narrow, "⚙ job 14", "the badge survives the cut")
	assert.NotContains(t, narrow, "@max-2", "the account is cut first")
}

func TestOverview_JobBadgeKeepsCardHeight(t *testing.T) {
	for _, width := range []int{18, 40, 60} {
		for _, badge := range []string{"⚙ queued", "⚙ job 14 +2", "⚙ job 123456789012 +99"} {
			d := CardData{Title: strings.Repeat("long-title-", 6), Status: session.Running,
				Branch: "aidanb/branch", Account: "max-2", JobBadge: badge,
				HasParity: true, Ahead: 100, Behind: 2000,
				HasDiff: true, DiffAdded: 1234, DiffRemoved: 5678}
			card := renderOverviewCard(d, width)
			assert.Equal(t, overviewCardHeight, len(strings.Split(card, "\n")),
				"width %d, badge %q", width, badge)
		}
	}
}
