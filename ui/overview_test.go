package ui

import (
	"fmt"
	"strings"
	"testing"

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
