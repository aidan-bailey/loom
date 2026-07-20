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

func testOverviewData() OverviewData {
	items := []*session.Instance{
		{Title: "auth-refactor", Status: session.Running},
		{Title: "db-migration", Status: session.Prompting},
	}
	return OverviewData{
		ActiveName:  "loom",
		Items:       items,
		Order:       SortForOverview(items),
		SelectedIdx: 0,
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

func TestOverview_RendersPeersAsDimHeaders(t *testing.T) {
	o := NewOverview()
	o.SetSize(120, 40)
	d := testOverviewData()
	d.Peers = []PeerSection{{Name: "summa", Attention: 1, Running: 2, Idle: 0}}
	out := ansi.Strip(o.Render(d))
	assert.Contains(t, out, "SUMMA · 3")
}

func TestOverview_NeverExceedsHeight(t *testing.T) {
	o := NewOverview()
	o.SetSize(80, 12)
	out := o.Render(testOverviewData())
	assert.LessOrEqual(t, len(strings.Split(out, "\n")), 12)
}

// TestOverview_SelectionFollowWindowing pins the selection-follow
// invariant: whatever the selected index, its card's row is scrolled
// into view. 80x16 with 12 one-column cards gives a 2-row window over
// 12 rows; sweeping the selection down then back up exercises both
// scroll directions against the persistent rowOffset.
func TestOverview_SelectionFollowWindowing(t *testing.T) {
	items := make([]*session.Instance, 12)
	for i := range items {
		items[i] = &session.Instance{Title: fmt.Sprintf("task-%02d", i), Status: session.Ready}
	}
	o := NewOverview()
	o.SetSize(80, 16)
	seq := make([]int, 0, 23)
	for i := 0; i < 12; i++ { // top → bottom
		seq = append(seq, i)
	}
	for i := 10; i >= 0; i-- { // bottom → top
		seq = append(seq, i)
	}
	for _, sel := range seq {
		d := OverviewData{
			ActiveName:  "loom",
			Items:       items,
			Order:       SortForOverview(items),
			SelectedIdx: sel,
		}
		out := ansi.Strip(o.Render(d))
		assert.Contains(t, out, items[sel].Title, "selected card missing at sel=%d", sel)
	}
}

// TestOverview_UniformCardHeight pins the fixed-card-height invariant
// that renderGrid's windowing math depends on: every card renders at
// exactly overviewCardHeight lines regardless of status, tail depth,
// or title/branch length — including degenerate card widths where the
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
// every combination of tiny heights, peer counts, and item counts must
// stay within the height budget (mirrors the List height-clamp sweep).
func TestOverview_NeverExceedsHeightDegenerate(t *testing.T) {
	peerSets := map[int][]PeerSection{
		0: nil,
		3: {
			{Name: "alpha", Attention: 1, Running: 1, Idle: 1},
			{Name: "beta", Running: 2},
			{Name: "gamma", Idle: 4},
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
		for nPeers, peers := range peerSets {
			for nItems, items := range itemSets {
				name := fmt.Sprintf("h=%d peers=%d items=%d", height, nPeers, nItems)
				o := NewOverview()
				o.SetSize(80, height)
				d := OverviewData{
					ActiveName:  "loom",
					Items:       items,
					Order:       SortForOverview(items),
					SelectedIdx: 0,
					Peers:       peers,
				}
				out := o.Render(d)
				assert.LessOrEqual(t, len(strings.Split(out, "\n")), height, name)
			}
		}
	}
}
