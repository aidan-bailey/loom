package overlay

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aidan-bailey/loom/ui"
)

// IssueRow is one selectable GitHub issue. Plain fields only so this
// package does not import session/github.
type IssueRow struct {
	Number int
	Title  string
	Labels []string
}

// IssuePicker lists open issues with a type-to-filter line. Filtering
// matches the number (prefix) or the title (case-insensitive
// substring). Enter picks the highlighted visible row; Esc cancels.
type IssuePicker struct {
	rows    []IssueRow
	visible []int // indices into rows after filtering
	filter  string
	cursor  int // index into visible
	status  string
	width   int
}

// NewIssuePicker creates a picker over rows (may be nil while loading).
func NewIssuePicker(rows []IssueRow) *IssuePicker {
	p := &IssuePicker{width: 72}
	p.SetRows(rows)
	return p
}

// SetRows replaces the row set (e.g. when a poll lands while the picker
// is open), re-applies the filter, and keeps the cursor on the same
// issue when it survives. Re-anchoring by number rather than position
// matters because a poll can add or close issues, which shifts every
// index below the change — without it a refresh arriving between the
// user's last keystroke and Enter could retarget the selection silently.
func (p *IssuePicker) SetRows(rows []IssueRow) {
	keep := 0
	if sel := p.Selected(); sel != nil {
		keep = sel.Number
	}
	p.rows = rows
	p.applyFilter()
	if keep == 0 {
		return
	}
	for vi, idx := range p.visible {
		if p.rows[idx].Number == keep {
			p.cursor = vi
			return
		}
	}
}

// SetStatus shows a line under the header ("loading…", "gh
// unavailable: …"); "" hides it.
func (p *IssuePicker) SetStatus(s string) { p.status = s }

func (p *IssuePicker) applyFilter() {
	p.visible = p.visible[:0]
	f := strings.ToLower(strings.TrimSpace(p.filter))
	for i, r := range p.rows {
		if f == "" || strings.HasPrefix(strconv.Itoa(r.Number), f) || strings.Contains(strings.ToLower(r.Title), f) {
			p.visible = append(p.visible, i)
		}
	}
	if p.cursor >= len(p.visible) {
		p.cursor = len(p.visible) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// VisibleNumbers lists the numbers of the rows the filter shows (tests).
func (p *IssuePicker) VisibleNumbers() []int {
	out := make([]int, 0, len(p.visible))
	for _, i := range p.visible {
		out = append(out, p.rows[i].Number)
	}
	return out
}

// HandleKeyPress returns (committed, canceled) like MergePicker.
func (p *IssuePicker) HandleKeyPress(msg tea.KeyPressMsg) (bool, bool) {
	switch msg.String() {
	case "up", "ctrl+p":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+n":
		if p.cursor < len(p.visible)-1 {
			p.cursor++
		}
	case "enter":
		return true, false
	case "esc", "ctrl+c":
		return true, true
	case "backspace":
		if p.filter != "" {
			r := []rune(p.filter)
			p.filter = string(r[:len(r)-1])
			p.applyFilter()
		}
	default:
		if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			p.filter += msg.Text
			p.cursor = 0
			p.applyFilter()
		}
	}
	return false, false
}

// Selected returns the highlighted visible row, or nil when the filter
// hides everything.
func (p *IssuePicker) Selected() *IssueRow {
	if p.cursor < 0 || p.cursor >= len(p.visible) {
		return nil
	}
	return &p.rows[p.visible[p.cursor]]
}

// HandleKey satisfies the Overlay interface.
func (p *IssuePicker) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := p.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (p *IssuePicker) View() string { return p.Render() }

// SetSize satisfies the Overlay interface; only width is used.
func (p *IssuePicker) SetSize(width, _ int) { p.width = width }

// maxIssueRows caps the visible list so a large repo does not push the
// hint line off screen. This only bounds rendered height because each
// row is truncated to exactly one line (see Render) — an untruncated
// title would wrap and defeat the cap.
const maxIssueRows = 15

// Render draws the picker.
func (p *IssuePicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.Text)
	dimStyle := lipgloss.NewStyle().Foreground(ui.Dim)
	hintStyle := lipgloss.NewStyle().Foreground(ui.Faint)

	var b strings.Builder
	b.WriteString(titleStyle.Render("New session from GitHub issue") + "\n")
	b.WriteString(dimStyle.Render("filter: ") + normalStyle.Render(p.filter+"▏") + "\n\n")
	if p.status != "" {
		b.WriteString(dimStyle.Render(p.status) + "\n")
	}
	if len(p.visible) == 0 && p.status == "" {
		b.WriteString(dimStyle.Render("no matching open issues") + "\n")
	}
	// Window the list around the cursor.
	start := 0
	if p.cursor >= maxIssueRows {
		start = p.cursor - maxIssueRows + 1
	}
	end := min(start+maxIssueRows, len(p.visible))
	for vi := start; vi < end; vi++ {
		r := p.rows[p.visible[vi]]
		labels := ""
		if len(r.Labels) > 0 {
			labels = "  [" + strings.Join(r.Labels, ", ") + "]"
		}
		marker := "  "
		if vi == p.cursor {
			marker = "> "
		}
		// One row, one line. The border style wraps rather than clips, so
		// an untruncated title turns a 15-row list into a ~39-line overlay,
		// which PlaceOverlay then clips from the TOP — taking the title and
		// the filter field with it. lipgloss Width is border-inclusive, so
		// subtract the border (2), the Padding(1,2) columns (4), and the
		// marker (2).
		//
		// Truncate by CELL width, not rune count: an emoji or CJK
		// character is one rune but two columns, and truncateRight (built
		// for file paths, which assume no wide runes) would under-cut,
		// letting lipgloss wrap the row and regrow the overlay past the
		// terminal. ansi.Truncate's returned string, tail included, never
		// exceeds the requested width (verified: it subtracts the tail's
		// width before collecting cells), so no further adjustment is
		// needed here.
		line := marker + ansi.Truncate(fmt.Sprintf("#%-5d %s%s", r.Number, r.Title, labels), p.width-8, "…")
		if vi == p.cursor {
			b.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			b.WriteString(normalStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + hintStyle.Render("type to filter • ↑↓ move • enter start session • esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(p.width).
		Render(b.String())
}
