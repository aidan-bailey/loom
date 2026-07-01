package overlay

import (
	"fmt"
	"strconv"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// MergePickerRow is one selectable source session in the merge picker.
// Index is the session's original 1-based number from the main
// session list (ui.DisplayIndex) — NOT this row's position in the
// picker's own slice. Rows can have gaps (an ineligible session was
// filtered out upstream), which is deliberate: typing the digit the
// user already saw in the main list must land on the same session.
type MergePickerRow struct {
	Index  int
	Title  string
	Branch string
	Status string
}

// MergePicker lets the user choose which session's branch to merge
// into the currently-focused ("target") session. Deliberately decoupled
// from session.Instance (plain string/int fields only) so this package
// doesn't need to import session — the caller (app.runMergeSelected)
// re-resolves the chosen row back to an *session.Instance by its Index.
type MergePicker struct {
	targetTitle string
	rows        []MergePickerRow
	cursor      int
	width       int
	digitBuf    string
}

// NewMergePicker creates a merge picker for targetTitle (shown in the
// header) offering rows as the selectable sources.
func NewMergePicker(targetTitle string, rows []MergePickerRow) *MergePicker {
	return &MergePicker{targetTitle: targetTitle, rows: rows, width: 56}
}

// HandleKeyPress processes navigation, digit-jump, and selection keys.
// Returns (committed, canceled). committed=true means the overlay
// should close; when committed, canceled=true means the user backed
// out via Esc/q rather than picking a row (SelectedRow may still be
// non-nil in that case — callers must check canceled first).
func (p *MergePicker) HandleKeyPress(msg tea.KeyPressMsg) (bool, bool) {
	switch msg.String() {
	case "up", "k":
		p.digitBuf = ""
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		p.digitBuf = ""
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "enter":
		return true, false
	case "esc", "q":
		return true, true
	default:
		s := msg.String()
		if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
			p.digitBuf += s
			p.applyDigitBuf()
		}
	}
	return false, false
}

// applyDigitBuf jumps the cursor to the row whose Index matches the
// buffered digits. If the buffered value already exceeds every row's
// Index, no row can ever match by appending more digits, so the buffer
// resets to just the latest keystroke — this keeps a stray digit from
// locking out further typing.
func (p *MergePicker) applyDigitBuf() {
	n, err := strconv.Atoi(p.digitBuf)
	if err != nil {
		p.digitBuf = ""
		return
	}
	maxIndex := 0
	for i, r := range p.rows {
		if r.Index == n {
			p.cursor = i
			p.digitBuf = ""
			return
		}
		if r.Index > maxIndex {
			maxIndex = r.Index
		}
	}
	if n > maxIndex {
		if len(p.digitBuf) <= 1 {
			p.digitBuf = ""
			return
		}
		p.digitBuf = p.digitBuf[len(p.digitBuf)-1:]
		p.applyDigitBuf()
	}
}

// SelectedRow returns the row currently highlighted, or nil if there
// are no rows.
func (p *MergePicker) SelectedRow() *MergePickerRow {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return nil
	}
	return &p.rows[p.cursor]
}

// HandleKey satisfies the Overlay interface.
func (p *MergePicker) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := p.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (p *MergePicker) View() string {
	return p.Render()
}

// SetSize satisfies the Overlay interface. Only width is used; height
// is accepted but ignored, matching WorkspacePicker.
func (p *MergePicker) SetSize(width, _ int) {
	p.width = width
}

// Render renders the merge picker overlay.
func (p *MergePicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.TextPrimary)
	hintStyle := lipgloss.NewStyle().Foreground(ui.TextHint)

	content := titleStyle.Render(fmt.Sprintf("Merge into '%s'", p.targetTitle)) + "\n\n"

	if len(p.rows) == 0 {
		content += normalStyle.Render("No other sessions available to merge") + "\n"
	}
	for i, r := range p.rows {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%d. %s (%s) [%s]", cursor, r.Index, r.Title, r.Branch, r.Status)
		if i == p.cursor {
			content += selectedStyle.Render(line) + "\n"
		} else {
			content += normalStyle.Render(line) + "\n"
		}
	}

	content += "\n" + hintStyle.Render("type # to jump • enter merge • esc cancel")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(p.width)

	return border.Render(content)
}
