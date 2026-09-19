package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
)

func TestGitHubBadge(t *testing.T) {
	cases := []struct {
		name string
		gh   github.State
		want string
	}{
		{"unknown renders nothing", github.State{}, ""},
		{"known no PR", github.State{Known: true}, ""},
		{"draft", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PRDraft}, "PR#45 draft"},
		{"open", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen}, "PR#45 open"},
		{"approved", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen, Review: github.ReviewApproved}, "PR#45 ✓approved"},
		{"changes", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PROpen, Review: github.ReviewChangesRequested}, "PR#45 ✗changes"},
		{"merged", github.State{Known: true, HasPR: true, PRNumber: 45, PRState: github.PRMerged}, "PR#45 merged"},
		{"checks pending", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksPending}, "PR#1 open ●"},
		{"checks passing", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksPassing}, "PR#1 open ✓"},
		{"checks failing", github.State{Known: true, HasPR: true, PRNumber: 1, PRState: github.PROpen, Checks: github.ChecksFailing}, "PR#1 open ✗"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, plain(githubBadge(c.gh)))
		})
	}
}

func TestParityToken(t *testing.T) {
	assert.Equal(t, "", plain(parityToken(CardData{})))
	assert.Equal(t, "✓ even", plain(parityToken(CardData{HasParity: true})))
	assert.Equal(t, "↑3 ↓12", plain(parityToken(CardData{HasParity: true, Ahead: 3, Behind: 12})))
}

func TestRailToken(t *testing.T) {
	assert.Equal(t, "", plain(railGitHubToken(CardData{})))
	d := CardData{GitHub: github.State{Known: true, IssueNumber: 12}}
	assert.Equal(t, "#12", plain(railGitHubToken(d)))
	d.GitHub.HasPR, d.GitHub.PRState, d.GitHub.Checks = true, github.PRMerged, github.ChecksPassing
	assert.Equal(t, "#12 · PR ⇄✓", plain(railGitHubToken(d)))
	d.HasParity, d.Behind = true, 4
	assert.Equal(t, "#12 · PR ⇄✓ ↓4", plain(railGitHubToken(d)))
	only := CardData{HasParity: true, Behind: 2, GitHub: github.State{Known: true}}
	assert.Equal(t, "↓2", plain(railGitHubToken(only)))
	even := CardData{HasParity: true, GitHub: github.State{Known: true}}
	assert.Equal(t, "", plain(railGitHubToken(even)), "behind=0 is not worth rail space")
}

func TestRenderCard_RailShowsGitHubTokenOnStatusLine(t *testing.T) {
	d := CardData{Title: "t", Index: 1, Status: session.Ready,
		GitHub: github.State{Known: true, IssueNumber: 12, HasPR: true, PRState: github.PROpen}}
	out := plain(RenderCard(d, DensityRail, 40))
	lines := strings.Split(out, "\n")
	assert.Len(t, lines, 2, "rail cards stay two lines")
	assert.Contains(t, lines[1], "#12 · PR ●")
	assert.Contains(t, lines[1], "idle")
}

func TestRenderOverviewCard_ShowsIssueLineAndBadge(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, Branch: "u/b",
		HasParity: true, Ahead: 1, Behind: 2,
		GitHub: github.State{Known: true, IssueNumber: 12, IssueTitle: "Fix flaky test", HasPR: true, PRNumber: 45, PRState: github.PROpen, Checks: github.ChecksFailing}}
	out := plain(renderOverviewCard(d, 60))
	assert.Contains(t, out, "#12 Fix flaky test")
	assert.Contains(t, out, "↑1 ↓2")
	assert.Contains(t, out, "PR#45 open ✗")
	assert.Equal(t, overviewCardHeight, len(strings.Split(out, "\n")))
}

func TestRenderOverviewCard_ClosedIssueMarked(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, GitHub: github.State{Known: true, IssueNumber: 12, IssueTitle: "Done", IssueClosed: true}}
	out := plain(renderOverviewCard(d, 60))
	assert.Contains(t, out, "✓ #12 Done")
}

func TestRenderOverviewCard_UnlinkedHasNoIssueLine(t *testing.T) {
	d := CardData{Title: "t", Status: session.Ready, TailLines: []string{"a", "b", "c"}}
	out := plain(renderOverviewCard(d, 60))
	assert.NotContains(t, out, "#")
	// Unlinked cards get one extra tail line (3 shown) so height matches.
	assert.Contains(t, out, "a")
	assert.Equal(t, overviewCardHeight, len(strings.Split(out, "\n")))
}

func TestBuildCardData_CopiesGitHubAndParity(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "t", Path: t.TempDir(), Program: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	inst.SetGitHubState(github.State{Known: true, IssueNumber: 9})
	d := BuildCardData(inst, false, "", 0)
	assert.Equal(t, 9, d.GitHub.IssueNumber)
	assert.False(t, d.HasParity)

	// Linked but not yet polled: the number still shows.
	fresh, err := session.NewInstance(session.InstanceOptions{Title: "f", Path: t.TempDir(), Program: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	fresh.SetIssue(7)
	d = BuildCardData(fresh, false, "", 0)
	assert.False(t, d.GitHub.Known)
	assert.Equal(t, 7, d.GitHub.IssueNumber)
}

// The rail composes a status phrase and a GitHub token with spreadLine,
// which zeroes its gap but does not shorten an over-wide right side —
// so the token must be clamped or the card bleeds into the pane beside
// it. The rail is 20% of terminal width, so narrow cards are ordinary,
// not exotic.
func TestRenderCard_RailNeverExceedsWidth(t *testing.T) {
	d := CardData{Title: "add-rate-limit", Index: 0, Status: session.Ready,
		HasParity: true, Behind: 5,
		GitHub: github.State{Known: true, IssueNumber: 1234, HasPR: true,
			PRState: github.PROpen, Review: github.ReviewChangesRequested,
			Checks: github.ChecksFailing}}
	for _, w := range []int{40, 24, 20, 16, 14, 12, 10} {
		for i, line := range strings.Split(RenderCard(d, DensityRail, w), "\n") {
			assert.LessOrEqual(t, lipgloss.Width(line), w,
				"width %d line %d overflows: %q", w, i, plain(line))
		}
	}
}
