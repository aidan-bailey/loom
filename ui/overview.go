package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
)

// overviewCardTailLines is the live-tail depth on overview cards.
const overviewCardTailLines = 2

// overviewCardHeight is the total line count of one rendered overview
// card: 3 content lines (title/status, branch/meta, rule) +
// overviewCardTailLines + 2 border lines. renderOverviewCard
// guarantees this height — every composed line is truncated to the
// inner width so lipgloss never wraps, and the tail is padded to
// exactly overviewCardTailLines (pinned by
// TestOverview_UniformCardHeight).
const overviewCardHeight = overviewCardTailLines + 5

// OverviewData is everything Render needs, assembled by the app on the
// Update goroutine each frame (same pattern as List reading instances).
type OverviewData struct {
	ActiveName  string
	Items       []*session.Instance
	Order       []int // display order (indices into Items), from SortForOverview
	SelectedIdx int   // list index of the selected instance
	Peers       []PeerSection
	Spinner     string
}

// Overview renders the fleet-triage card grid: the active workspace's
// instances as bordered cards under a collapsible group header, peer
// workspaces as dimmed count headers (live selection stays scoped to
// the active workspace until cross-workspace lands).
type Overview struct {
	width, height int
	collapsed     map[string]bool
	rowOffset     int // first visible card row (scroll window)
}

// NewOverview constructs an empty overview component.
func NewOverview() *Overview {
	return &Overview{collapsed: make(map[string]bool)}
}

// SetSize sets the render bounds.
func (o *Overview) SetSize(w, h int) { o.width, o.height = w, h }

// ToggleCollapse flips a group's collapsed state (case-insensitive name).
func (o *Overview) ToggleCollapse(name string) {
	key := strings.ToLower(name)
	o.collapsed[key] = !o.collapsed[key]
}

// IsCollapsed reports a group's collapsed state.
func (o *Overview) IsCollapsed(name string) bool {
	return o.collapsed[strings.ToLower(name)]
}

// overviewColumns maps width to a 1–3 column grid.
func overviewColumns(width int) int {
	cols := width / 60
	if cols < 1 {
		cols = 1
	}
	if cols > 3 {
		cols = 3
	}
	return cols
}

// Render draws the overview. Height is hard-clamped.
func (o *Overview) Render(d OverviewData) string {
	if o.width == 0 || o.height == 0 {
		return ""
	}
	var b strings.Builder

	marker := "▾"
	collapsed := o.IsCollapsed(d.ActiveName)
	if collapsed {
		marker = "▸"
	}
	header := fmt.Sprintf("%s %s · %d", marker, strings.ToUpper(d.ActiveName), len(d.Items))
	wsStyle := lipgloss.NewStyle().Foreground(Workspace)
	b.WriteString(wsStyle.Render(header) + "\n")

	if !collapsed && len(d.Items) > 0 {
		b.WriteString(o.renderGrid(d))
	}

	if len(d.Peers) > 0 {
		dim := lipgloss.NewStyle().Foreground(Dim)
		b.WriteString("\n")
		for _, p := range d.Peers {
			total := p.Attention + p.Running + p.Idle
			// Separately-styled concatenated segments — never nest a
			// styled run inside another Render (see RenderCard's solidBg
			// note: an outer style does not survive embedded SGR resets).
			line := dim.Render(fmt.Sprintf("▸ %s · %d", strings.ToUpper(p.Name), total))
			if p.Attention > 0 {
				line += lipgloss.NewStyle().Foreground(Attention).Render(fmt.Sprintf("  ❯%d waiting", p.Attention))
			}
			b.WriteString(line + "\n")
		}
	}
	return clampHeight(lipgloss.Place(o.width, o.height, lipgloss.Left, lipgloss.Top, b.String()), o.height)
}

// renderGrid lays cards out in rows of overviewColumns, windowed so the
// selected card's row is always visible. Like ScrollModel's
// AdvanceAndRender, rendering mutates the scroll anchor (o.rowOffset) —
// exactly once per render pass. The window is computed BEFORE any card
// is rendered (selRow needs only Order positions and every card row is
// exactly overviewCardHeight lines), so only visible rows' cards are
// ever built — off-window instances cost nothing per frame.
func (o *Overview) renderGrid(d OverviewData) string {
	cols := overviewColumns(o.width)
	cardW := (o.width - (cols - 1)) / cols

	selRow := 0
	for pos, idx := range d.Order {
		if idx == d.SelectedIdx {
			selRow = pos / cols
			break
		}
	}
	nRows := (len(d.Order) + cols - 1) / cols

	// Window rows so selRow stays visible within the height budget
	// (header + peers consumed elsewhere).
	rowH := overviewCardHeight
	budget := o.height - 1 - peerBudget(d.Peers)
	visRows := budget / rowH
	if visRows < 1 {
		visRows = 1
	}
	if selRow < o.rowOffset {
		o.rowOffset = selRow
	}
	if selRow >= o.rowOffset+visRows {
		o.rowOffset = selRow - visRows + 1
	}
	if o.rowOffset > nRows-visRows {
		o.rowOffset = nRows - visRows
	}
	if o.rowOffset < 0 {
		o.rowOffset = 0
	}
	endRow := o.rowOffset + visRows
	if endRow > nRows {
		endRow = nRows
	}

	rows := make([]string, 0, endRow-o.rowOffset)
	for r := o.rowOffset; r < endRow; r++ {
		start := r * cols
		end := start + cols
		if end > len(d.Order) {
			end = len(d.Order)
		}
		cards := make([]string, 0, end-start)
		for _, idx := range d.Order[start:end] {
			cd := BuildCardData(d.Items[idx], idx == d.SelectedIdx, d.Spinner, overviewCardTailLines)
			cd.Index = DisplayIndex(d.Items, idx)
			cards = append(cards, renderOverviewCard(cd, cardW))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(cards)...))
	}
	return strings.Join(rows, "\n")
}

// peerBudget is the line budget consumed by the peer footer (one line
// per peer plus the separating blank line).
func peerBudget(peers []PeerSection) int {
	if len(peers) == 0 {
		return 0
	}
	return len(peers) + 1
}

// joinWithGap interleaves a one-column gap between cards for
// JoinHorizontal.
func joinWithGap(cards []string) []string {
	out := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			out = append(out, " ")
		}
		out = append(out, c)
	}
	return out
}

// renderOverviewCard renders one DensityCard box: border colored by
// attention/selection, title + status corner, branch + diff meta, rule,
// live tail. Styled substrings are composed into plain lines and only
// wrapped in a border style (no background), so embedded SGR resets
// cannot strip an outer style (see RenderCard's solidBg note).
func renderOverviewCard(d CardData, width int) string {
	inner := width - 2 // border columns
	if inner < 10 {
		inner = 10
	}
	borderColor := d.accentColor()

	titleFg := Text
	if d.NeedsAttention() {
		titleFg = Attention
	}
	// Both columns are truncated against inner (not a fixed
	// reservation): right column first, then left against the
	// remainder. The border style sets Width, so a line wider than
	// inner would wrap and make this card taller than its row peers,
	// breaking grid alignment and the overviewCardHeight invariant.
	status := lipgloss.NewStyle().Foreground(statusFgFor(d)).Render(truncate(d.statusLabel(), inner))
	title := lipgloss.NewStyle().Foreground(titleFg).Bold(d.Selected).
		Render(truncate(d.Title, inner-lipgloss.Width(status)-1))
	top := spreadLine(title, status, inner)

	dim := lipgloss.NewStyle().Foreground(Dim)
	meta := ""
	if d.HasDiff {
		meta = lipgloss.NewStyle().Foreground(OK).Render(fmt.Sprintf("+%d", d.DiffAdded)) + " " +
			lipgloss.NewStyle().Foreground(ErrorColor).Render(fmt.Sprintf("−%d", d.DiffRemoved))
		if lipgloss.Width(meta) > inner {
			// Styled composition, so ANSI-aware truncation.
			meta = ansi.Truncate(meta, inner, "…")
		}
	}
	mid := spreadLine(dim.Render(truncate(d.Branch, inner-lipgloss.Width(meta)-1)), meta, inner)

	rule := lipgloss.NewStyle().Foreground(Rule).Render(strings.Repeat("─", inner))

	tails := make([]string, 0, overviewCardTailLines)
	for _, l := range d.TailLines {
		tails = append(tails, dim.Render(truncate(l, inner)))
	}
	for len(tails) < overviewCardTailLines {
		tails = append(tails, "")
	}

	content := strings.Join(append([]string{top, mid, rule}, tails...), "\n")
	// The selected card gets a thick border so selection is unmistakable
	// regardless of which accent color ranks (attention stays gold, but
	// the border weight marks the cursor). Border height is identical, so
	// the overviewCardHeight invariant is unaffected.
	border := lipgloss.RoundedBorder()
	if d.Selected {
		border = lipgloss.ThickBorder()
	}
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(borderColor).
		Width(width).
		Render(content)
}

// statusFgFor picks the status-corner foreground for a card.
func statusFgFor(d CardData) color.Color {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Status == session.Running || d.Status == session.Loading:
		return Accent
	default:
		return Dim
	}
}

// spreadLine left-aligns l and right-aligns r within width cells.
// Callers truncate l against r's actual width, so gap ≥ 1 whenever
// both are non-empty; clamping at 0 (not 1) means an r that alone
// fills the width still composes to exactly width cells instead of
// overflowing by one and wrapping.
func spreadLine(l, r string, width int) string {
	gap := width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 0 {
		gap = 0
	}
	return l + strings.Repeat(" ", gap) + r
}
