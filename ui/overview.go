package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
)

// overviewCardTailLines is the live-tail depth on overview cards.
const overviewCardTailLines = 2

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
			line := fmt.Sprintf("▸ %s · %d", strings.ToUpper(p.Name), total)
			if p.Attention > 0 {
				line += lipgloss.NewStyle().Foreground(Attention).Render(fmt.Sprintf("  ❯%d waiting", p.Attention))
			}
			b.WriteString(dim.Render(line) + "\n")
		}
	}
	return clampHeight(lipgloss.Place(o.width, o.height, lipgloss.Left, lipgloss.Top, b.String()), o.height)
}

// renderGrid lays cards out in rows of overviewColumns, windowed so the
// selected card's row is always visible. Like ScrollModel's
// AdvanceAndRender, rendering mutates the scroll anchor (o.rowOffset) —
// exactly once per render pass.
func (o *Overview) renderGrid(d OverviewData) string {
	cols := overviewColumns(o.width)
	cardW := (o.width - (cols - 1)) / cols

	cards := make([]string, 0, len(d.Order))
	selRow := 0
	for pos, idx := range d.Order {
		cd := BuildCardData(d.Items[idx], idx == d.SelectedIdx, d.Spinner, overviewCardTailLines)
		cd.Index = DisplayIndex(d.Items, idx)
		cards = append(cards, renderOverviewCard(cd, cardW))
		if idx == d.SelectedIdx {
			selRow = pos / cols
		}
	}

	rows := make([]string, 0, (len(cards)+cols-1)/cols)
	for i := 0; i < len(cards); i += cols {
		end := i + cols
		if end > len(cards) {
			end = len(cards)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(cards[i:end])...))
	}

	// Window rows so selRow stays visible within the height budget
	// (header + peers consumed elsewhere; approximate one card row's
	// height from its rendered line count).
	rowH := 1
	if len(rows) > 0 {
		rowH = len(strings.Split(rows[0], "\n"))
	}
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
	if o.rowOffset > len(rows)-visRows {
		o.rowOffset = len(rows) - visRows
	}
	if o.rowOffset < 0 {
		o.rowOffset = 0
	}
	end := o.rowOffset + visRows
	if end > len(rows) {
		end = len(rows)
	}
	return strings.Join(rows[o.rowOffset:end], "\n")
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
	// Left columns are truncated against the right side's actual width
	// (not a fixed reservation): the border style sets Width, so a line
	// wider than inner would wrap and make this card a line taller than
	// its row peers, breaking grid alignment.
	status := lipgloss.NewStyle().Foreground(statusFgFor(d)).Render(d.statusLabel())
	title := lipgloss.NewStyle().Foreground(titleFg).Bold(d.Selected).
		Render(truncate(d.Title, inner-lipgloss.Width(status)-1))
	top := spreadLine(title, status, inner)

	dim := lipgloss.NewStyle().Foreground(Dim)
	meta := ""
	if d.HasDiff {
		meta = lipgloss.NewStyle().Foreground(OK).Render(fmt.Sprintf("+%d", d.DiffAdded)) + " " +
			lipgloss.NewStyle().Foreground(ErrorColor).Render(fmt.Sprintf("−%d", d.DiffRemoved))
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
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
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
func spreadLine(l, r string, width int) string {
	gap := width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		gap = 1
	}
	return l + strings.Repeat(" ", gap) + r
}
