package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// testOverviewData builds a single loaded group ("loom") with two
// instances, cursor on the first.
func testOverviewData() OverviewData {
	items := []*session.Instance{
		{Title: "auth-refactor", Status: session.Running},
		{Title: "db-migration", Status: session.Prompting},
	}
	return OverviewData{
		Groups: []OverviewGroup{
			{Name: "loom", Items: items, Order: SortForOverview(items), State: GroupLoaded},
		},
		Cursor: OverviewCursor{Group: 0, Item: 0},
	}
}

func TestOverview_RendersGroupHeaderAndCards(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	out := ansi.Strip(o.Render(testOverviewData()))
	assert.Contains(t, out, "LOOM · 2")
	assert.Contains(t, out, "db-migration")
	assert.Contains(t, out, "auth-refactor")
	assert.Contains(t, out, "awaiting input")
}

func TestOverview_ColumnsScaleWithWidth(t *testing.T) {
	assert.Equal(t, 1, overviewColumns(59))
	assert.Equal(t, 2, overviewColumns(120))
	assert.Equal(t, 3, overviewColumns(200))
}

func TestOverview_CollapseHidesCards(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	o.ToggleCollapse("loom")
	out := ansi.Strip(o.Render(testOverviewData()))
	assert.Contains(t, out, "▸ LOOM · 2")
	assert.NotContains(t, out, "db-migration")
}

// TestOverview_RendersMultipleGroups pins the multi-group fleet view:
// loaded, loading, error, and empty groups each render their header and
// state-appropriate body.
func TestOverview_RendersMultipleGroups(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 40)
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "alpha", State: GroupLoaded,
				Items: []*session.Instance{{Title: "a1", Status: session.Ready}},
				Order: []int{0}},
			{Name: "beta", State: GroupLoading},
			{Name: "gamma", State: GroupError, Err: "reconcile: boom"},
			{Name: "delta", State: GroupEmpty},
		},
		Cursor: OverviewCursor{Group: 0, Item: 0},
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "ALPHA")
	assert.Contains(t, out, "a1")
	assert.Contains(t, out, "BETA")
	assert.Contains(t, out, "loading")
	assert.Contains(t, out, "GAMMA")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "DELTA")
	assert.Contains(t, out, "no sessions")
}

func TestOverview_NeverExceedsHeight(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 12)
	out := o.Render(testOverviewData())
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), 12)
}

// TestOverview_UniformCardHeight pins the fixed-card-height invariant
// that the grid math depends on: every card renders at exactly
// overviewCardHeight lines regardless of status, tail depth, or
// title/branch length — including degenerate card widths where the
// status label alone exceeds the inner width (which must truncate, not
// wrap inside the border).
func TestOverview_UniformCardHeight(t *testing.T) {
	variants := []CardData{
		{Title: "prompting-card", Status: session.Prompting, StatusAge: 4 * time.Minute},
		{Title: "running-card", Status: session.Running, Spinner: "✻",
			TailLines: []string{"line one", "line two"}},
		{Title: "ready-card", Status: session.Ready, TailLines: []string{"only tail"}},
		{Title: "selected-card", Status: session.Ready, Selected: true}, // thick border, same height
		{Title: "paused-card", Status: session.Paused},
		{Title: strings.Repeat("very-long-title-", 8), Status: session.Running,
			Branch:  strings.Repeat("user/branch-", 6),
			HasDiff: true, DiffAdded: 1234, DiffRemoved: 5678},
	}
	for _, width := range []int{18, 40, 60} { // 18: status wider than inner
		row := make([]string, 0, len(variants))
		for _, d := range variants {
			card := renderOverviewCard(d, width)
			assert.Equal(t, overviewCardHeight, len(strings.Split(card, "\n")),
				"card %q at width %d", d.Title, width)
			row = append(row, card)
		}
		joined := lipgloss.JoinHorizontal(lipgloss.Top, row...)
		assert.Equal(t, overviewCardHeight, len(strings.Split(joined, "\n")),
			"joined row at width %d", width)
	}
}

// TestOverview_NeverExceedsHeightDegenerate sweeps degenerate sizes:
// every combination of tiny heights, group counts, and item counts must
// stay within the height budget (clampHeight bounds the final render).
func TestOverview_NeverExceedsHeightDegenerate(t *testing.T) {
	extraGroupSets := map[int][]OverviewGroup{
		0: nil,
		3: {
			{Name: "alpha", State: GroupLoading},
			{Name: "beta", State: GroupError, Err: "boom"},
			{Name: "gamma", State: GroupEmpty},
		},
	}
	itemSets := map[int][]*session.Instance{
		0: nil,
		2: {
			{Title: "auth-refactor", Status: session.Running},
			{Title: "db-migration", Status: session.Prompting},
		},
	}
	for height := 1; height <= 8; height++ {
		for nExtra, extras := range extraGroupSets {
			for nItems, items := range itemSets {
				name := fmt.Sprintf("h=%d extra=%d items=%d", height, nExtra, nItems)
				o := NewOverview()
				o.SetSize(80, height)
				groups := []OverviewGroup{{Name: "loom", Items: items, State: GroupLoaded}}
				if len(items) == 0 {
					groups[0].State = GroupEmpty
				} else {
					groups[0].Order = SortForOverview(items)
				}
				groups = append(groups, extras...)
				d := OverviewData{Groups: groups, Cursor: OverviewCursor{Group: 0, Item: 0}}
				out := o.Render(d)
				assert.LessOrEqual(t, len(strings.Split(out, "\n")), height, name)
			}
		}
	}
}
