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

// TestOverview_RendersMultipleGroups pins the multi-group view: loaded
// and empty groups each render their header and state-appropriate body.
func TestOverview_RendersMultipleGroups(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 40)
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "alpha", State: GroupLoaded,
				Items: []*session.Instance{{Title: "a1", Status: session.Ready}},
				Order: []int{0}},
			{Name: "delta", State: GroupEmpty},
		},
		Cursor: OverviewCursor{Group: 0, Item: 0},
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "ALPHA")
	assert.Contains(t, out, "a1")
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

func TestOverview_WindowKeepsCursorGroupVisible(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, overviewCardHeight+4) // room for ~1 card row + a header
	// Two loaded groups, each with 3 cards; cursor deep in the SECOND
	// group must be visible (the first group scrolls off the top).
	mkItems := func(p string) ([]*session.Instance, []int) {
		its := []*session.Instance{
			{Title: p + "1", Status: session.Ready},
			{Title: p + "2", Status: session.Ready},
			{Title: p + "3", Status: session.Ready},
		}
		return its, []int{0, 1, 2}
	}
	i1, o1 := mkItems("g")
	i2, o2 := mkItems("h")
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "one", State: GroupLoaded, Items: i1, Order: o1},
			{Name: "two", State: GroupLoaded, Items: i2, Order: o2},
		},
		Cursor: OverviewCursor{Group: 1, Item: 2}, // last card of second group
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "h3", "cursor card is within the visible window")
	// Height budget respected.
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), overviewCardHeight+4)
}

// TestOverview_WindowUpScrollsToFirstGroup exercises the up-scroll
// branch (top < o.rowOffset) and the rowOffset < 0 guard: after a first
// render pushes rowOffset down (cursor deep in the second group), a
// second render with the cursor on the first card of the first group
// must scroll back up so that card is visible.
func TestOverview_WindowUpScrollsToFirstGroup(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, overviewCardHeight+4) // room for ~1 card row + a header
	mkItems := func(p string) ([]*session.Instance, []int) {
		its := []*session.Instance{
			{Title: p + "1", Status: session.Ready},
			{Title: p + "2", Status: session.Ready},
			{Title: p + "3", Status: session.Ready},
		}
		return its, []int{0, 1, 2}
	}
	i1, ord1 := mkItems("g")
	i2, ord2 := mkItems("h")
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "one", State: GroupLoaded, Items: i1, Order: ord1},
			{Name: "two", State: GroupLoaded, Items: i2, Order: ord2},
		},
	}
	// First render: cursor deep in the SECOND group pushes rowOffset down.
	d.Cursor = OverviewCursor{Group: 1, Item: 2}
	_ = o.Render(d)
	// Second render: cursor jumps to the first card of the first group;
	// the window must scroll back up so g1 is visible.
	d.Cursor = OverviewCursor{Group: 0, Item: 0}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "g1", "up-scroll brings the first group's first card back into view")
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), overviewCardHeight+4)
}

// TestOverview_WindowAccountsForShortPrecedingGroups pins the
// cursorLineSpan variable-block-height accounting: a SHORT preceding
// block (a collapsed, header-only group) must be counted as its actual
// line count (strings.Count+1), not assumed to be a full card row. If the
// per-block accounting were off, the cursor card in the following loaded
// group would be windowed out.
func TestOverview_WindowAccountsForShortPrecedingGroups(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, overviewCardHeight+4)
	o.ToggleCollapse("groupzero") // block 0 becomes header-only (1 line)
	loaded := []*session.Instance{
		{Title: "loom0", Status: session.Ready},
		{Title: "loom1", Status: session.Ready},
		{Title: "loom2", Status: session.Ready},
		{Title: "loom3", Status: session.Ready},
	}
	tail := []*session.Instance{{Title: "z1", Status: session.Ready}}
	d := OverviewData{
		Groups: []OverviewGroup{
			{Name: "groupzero", State: GroupLoaded,
				Items: []*session.Instance{{Title: "hidden", Status: session.Ready}},
				Order: []int{0}},
			{Name: "loom", State: GroupLoaded, Items: loaded, Order: []int{0, 1, 2, 3}},
			{Name: "zeta", State: GroupLoaded, Items: tail, Order: []int{0}},
		},
		// Cursor on the last card of the loaded middle group requires
		// scrolling past the short (collapsed) preceding block.
		Cursor: OverviewCursor{Group: 1, Item: 3},
	}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "loom3", "short preceding block is accounted correctly so the cursor card is visible")
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), overviewCardHeight+4)
}

// TestOverview_NeverExceedsHeightDegenerate sweeps degenerate sizes:
// every combination of tiny heights, group counts, and item counts must
// stay within the height budget (clampHeight bounds the final render).
func TestOverview_NeverExceedsHeightDegenerate(t *testing.T) {
	extraGroupSets := map[int][]OverviewGroup{
		0: nil,
		2: {
			{Name: "alpha", State: GroupEmpty},
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
