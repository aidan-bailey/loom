package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withShowAccounts(t *testing.T, on bool) {
	t.Helper()
	SetShowAccounts(on)
	t.Cleanup(func() { SetShowAccounts(false) })
}

func claudeInstance(t *testing.T, program, acct string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "badge", Path: t.TempDir(), Program: program})
	require.NoError(t, err)
	inst.SetAccount(acct)
	return inst
}

func TestAccountLabel(t *testing.T) {
	withShowAccounts(t, false)
	assert.Equal(t, "", accountLabel(claudeInstance(t, "claude", "max-2")), "badges off")

	withShowAccounts(t, true)
	assert.Equal(t, "max-2", accountLabel(claudeInstance(t, "claude", "max-2")))
	assert.Equal(t, "default", accountLabel(claudeInstance(t, "claude", "")))
	assert.Equal(t, "", accountLabel(claudeInstance(t, "aider", "max-2")), "only Claude sessions have an account")
	assert.Equal(t, "", accountLabel(nil))
}

func TestBuildCardData_CarriesTheAccount(t *testing.T) {
	withShowAccounts(t, true)
	d := BuildCardData(claudeInstance(t, "claude", "max-2"), false, "", 0)
	assert.Equal(t, "max-2", d.Account)
}

func TestRenderCard_RailShowsTheAccountBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := RenderCard(d, DensityRail, 40)
	first := ansi.Strip(strings.Split(out, "\n")[0])
	assert.Contains(t, first, "1. fix-login")
	assert.Contains(t, first, "@max-2")
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 40)
	}
}

func TestRenderCard_NarrowRailDropsTheBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := ansi.Strip(RenderCard(d, DensityRail, 12))
	assert.NotContains(t, out, "@max-2")
}

func TestRenderOverviewCard_ShowsTheAccount(t *testing.T) {
	d := CardData{Title: "fix-login", Branch: "aidanb/fix", Status: session.Running, Account: "max-2"}
	assert.Contains(t, ansi.Strip(renderOverviewCard(d, 50)), "@max-2")
}

// TestRenderOverviewCard_AccountDropsBeforeOtherMeta pins the meta-line
// ordering: the account token sorts last, so ansi.Truncate — which cuts
// from the right — drops it before it touches parity or diff stats.
func TestRenderOverviewCard_AccountDropsBeforeOtherMeta(t *testing.T) {
	d := CardData{
		Title: "fix-login", Branch: "aidanb/fix-login-flow", Status: session.Running,
		Account:   "max-2",
		HasParity: true, Ahead: 2, Behind: 1,
		HasDiff: true, DiffAdded: 3, DiffRemoved: 1,
	}
	wide := ansi.Strip(renderOverviewCard(d, 20))
	assert.Contains(t, wide, "@max-2")
	assert.Contains(t, wide, "↑2")
	assert.Contains(t, wide, "+3")

	narrow := ansi.Strip(renderOverviewCard(d, 15))
	assert.NotContains(t, narrow, "@max-2", "too narrow for everything: the account token is dropped first")
	assert.Contains(t, narrow, "↑2 ↓1 +3 −1", "parity and diff stats survive intact at the same width")
}

// TestRenderCard_AccountBadgeDecisionIsWidthOnly reproduces the bug where
// the rail badge's show/hide threshold depended on the specific account
// name's length: at a given card width, every account — short or long —
// must make the same call, since the threshold reserves the badge's cap
// (railAccountBadgeMaxWidth), never its actual width.
func TestRenderCard_AccountBadgeDecisionIsWidthOnly(t *testing.T) {
	names := []string{"x", "default", "very-long-account-name"}
	wantShown := map[int]bool{16: false, 20: false, 24: false, 30: true, 40: true}
	for _, w := range []int{16, 20, 24, 30, 40} {
		var shown []bool
		for _, name := range names {
			d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: name}
			first := ansi.Strip(strings.Split(RenderCard(d, DensityRail, w), "\n")[0])
			shown = append(shown, strings.Contains(first, "@"))
		}
		for i := range shown {
			assert.Equal(t, shown[0], shown[i],
				"width %d: badge presence for %q must match %q's", w, names[i], names[0])
		}
		assert.Equal(t, wantShown[w], shown[0], "width %d", w)
	}
}

// TestRenderCard_LongAccountNameBadgeIsCapped pins the badge's own width
// cap: a long account name is truncated with an ellipsis rather than
// growing the badge (which would also invalidate the width-only decision
// above).
func TestRenderCard_LongAccountNameBadgeIsCapped(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "very-long-account-name"}
	first := ansi.Strip(strings.Split(RenderCard(d, DensityRail, 60), "\n")[0])
	i := strings.Index(first, "@")
	require.GreaterOrEqual(t, i, 0, "wide enough for the badge to show")
	assert.LessOrEqual(t, lipgloss.Width(first[i:]), railAccountBadgeMaxWidth)
}

// TestRenderCard_SelectedRailAccountBadgeGapCarriesBackground pins the
// striping fix: on a selected rail card (solid Panel background), the
// gap spreadLine inserts between the title and the badge must itself
// carry that background, or it leaves an unpainted seam.
func TestRenderCard_SelectedRailAccountBadgeGapCarriesBackground(t *testing.T) {
	d := CardData{Title: "fix", Index: 1, Status: session.Running, Account: "max-2", Selected: true}
	out := RenderCard(d, DensityRail, 40)

	title := lipgloss.NewStyle().Foreground(Text).Bold(true).Background(Panel).Render("1. fix")
	badge := lipgloss.NewStyle().Foreground(Dim).Background(Panel).Render("@max-2")
	inner := 40 - 2
	gap := inner - lipgloss.Width(title) - lipgloss.Width(badge)
	require.Positive(t, gap)
	wantGap := lipgloss.NewStyle().Background(Panel).Render(strings.Repeat(" ", gap))
	assert.Contains(t, out, wantGap)
}

// TestSpreadLineBg_FillsGapWithPanelBackgroundOnlyWhenFilled pins
// spreadLineBg itself: filled=true must wrap the gap in Panel's
// background; filled=false must behave exactly like plain spreadLine.
func TestSpreadLineBg_FillsGapWithPanelBackgroundOnlyWhenFilled(t *testing.T) {
	width := len("L") + 5 + len("R")
	want := lipgloss.NewStyle().Background(Panel).Render(strings.Repeat(" ", 5))
	assert.Contains(t, spreadLineBg("L", "R", width, true), want)

	unfilled := spreadLineBg("L", "R", width, false)
	assert.NotContains(t, unfilled, want)
	assert.Equal(t, spreadLine("L", "R", width), unfilled)
}
