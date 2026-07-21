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

// GroupState classifies how an overview group renders.
type GroupState int

const (
	GroupLoaded  GroupState = iota // reconciled; render cards
	GroupLoading                   // background activation in flight
	GroupError                     // background activation failed
	GroupEmpty                     // loaded but no instances
)

// OverviewGroup is one workspace's slice of the fleet overview.
type OverviewGroup struct {
	Name  string
	Items []*session.Instance
	Order []int // SortForOverview(Items); empty for non-loaded states
	State GroupState
	Err   string // populated when State == GroupError
}

// OverviewCursor is the render-space selection: a group index into
// Groups and an item position within that group's Order.
type OverviewCursor struct {
	Group int
	Item  int
}

// OverviewData is everything Render needs, assembled by the app on the
// Update goroutine each frame (same pattern as List reading instances).
type OverviewData struct {
	Groups  []OverviewGroup
	Cursor  OverviewCursor
	Spinner string
}

// Overview renders the fleet-triage card grid: every workspace group's
// instances as bordered cards under a collapsible group header, with
// loading/error/empty groups rendered inline. The render cursor spans
// groups (OverviewCursor{Group,Item}).
type Overview struct {
	width, height int
	collapsed     map[string]bool
	rowOffset     int // first visible card row (Task 7 combined window)
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

// Render draws the multi-group fleet overview. Height is hard-clamped;
// the combined vertical window (Task 7) keeps the cursor group/card
// visible.
func (o *Overview) Render(d OverviewData) string {
	if o.width == 0 || o.height == 0 {
		return ""
	}
	blocks := o.groupBlocks(d) // []string, one rendered block per group
	windowed := o.window(blocks, d)
	return clampHeight(lipgloss.Place(o.width, o.height, lipgloss.Left, lipgloss.Top, windowed), o.height)
}

// groupBlocks renders each group to a string block (header + body).
func (o *Overview) groupBlocks(d OverviewData) []string {
	blocks := make([]string, len(d.Groups))
	wsStyle := lipgloss.NewStyle().Foreground(Workspace)
	dim := lipgloss.NewStyle().Foreground(Dim)
	errStyle := lipgloss.NewStyle().Foreground(ErrorColor)
	for gi, g := range d.Groups {
		collapsed := o.IsCollapsed(g.Name)
		marker := "▾"
		if collapsed {
			marker = "▸"
		}
		header := wsStyle.Render(fmt.Sprintf("%s %s · %d", marker, strings.ToUpper(g.Name), len(g.Items)))
		var body string
		switch {
		case collapsed:
			body = ""
		case g.State == GroupLoading:
			body = dim.Render("  loading…")
		case g.State == GroupError:
			body = errStyle.Render("  failed to load — " + g.Err)
		case g.State == GroupEmpty || len(g.Order) == 0:
			body = dim.Render("  no sessions")
		default:
			body = o.renderGroupGrid(g, gi, d)
		}
		if body == "" {
			blocks[gi] = header
		} else {
			blocks[gi] = header + "\n" + body
		}
	}
	return blocks
}

// renderGroupGrid lays one group's cards in rows of overviewColumns.
// Highlighting keys on the render cursor matching this group + position.
func (o *Overview) renderGroupGrid(g OverviewGroup, gi int, d OverviewData) string {
	cols := overviewColumns(o.width)
	cardW := (o.width - (cols - 1)) / cols
	nRows := (len(g.Order) + cols - 1) / cols
	rows := make([]string, 0, nRows)
	for r := 0; r < nRows; r++ {
		start := r * cols
		end := start + cols
		if end > len(g.Order) {
			end = len(g.Order)
		}
		cards := make([]string, 0, end-start)
		for pos := start; pos < end; pos++ {
			idx := g.Order[pos]
			selected := d.Cursor.Group == gi && d.Cursor.Item == pos
			cd := BuildCardData(g.Items[idx], selected, d.Spinner, overviewCardTailLines)
			cd.Index = DisplayIndex(g.Items, idx)
			cards = append(cards, renderOverviewCard(cd, cardW))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(cards)...))
	}
	return strings.Join(rows, "\n")
}

// window vertically scrolls the joined group blocks so the cursor card's
// line range stays visible within o.height. Mutates o.rowOffset exactly
// once per render pass (same discipline as the old single-group grid).
func (o *Overview) window(blocks []string, d OverviewData) string {
	joined := strings.Join(blocks, "\n")
	lines := strings.Split(joined, "\n")
	budget := o.height
	if len(lines) <= budget {
		o.rowOffset = 0
		return joined
	}
	// Absolute line span of the cursor card.
	top, bottom := o.cursorLineSpan(blocks, d)
	if top < o.rowOffset {
		o.rowOffset = top
	}
	if bottom >= o.rowOffset+budget {
		o.rowOffset = bottom - budget + 1
	}
	if o.rowOffset > len(lines)-budget {
		o.rowOffset = len(lines) - budget
	}
	if o.rowOffset < 0 {
		o.rowOffset = 0
	}
	end := o.rowOffset + budget
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[o.rowOffset:end], "\n")
}

// cursorLineSpan returns the absolute [top,bottom] line indices (into the
// joined blocks) of the cursor's card. Each group block is 1 header line
// plus its body; a card row is overviewCardHeight lines. Groups are
// separated by one join newline (already accounted for because each
// block's own line count includes only its content and the join adds one
// line between blocks — see the running `line` counter).
func (o *Overview) cursorLineSpan(blocks []string, d OverviewData) (int, int) {
	line := 0
	cols := overviewColumns(o.width)
	for gi, b := range blocks {
		blockLines := strings.Count(b, "\n") + 1
		if gi == d.Cursor.Group {
			cardRow := d.Cursor.Item / cols
			top := line + 1 + cardRow*overviewCardHeight // +1 skips the header
			return top, top + overviewCardHeight - 1
		}
		line += blockLines
	}
	return 0, 0
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
