package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		r := []rune(s)
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
}

func rows() []IssueRow {
	return []IssueRow{
		{Number: 12, Title: "Fix flaky test", Labels: []string{"bug"}},
		{Number: 13, Title: "Add dark theme"},
		{Number: 20, Title: "Flaky CI on main"},
	}
}

func TestIssuePicker_NavigateAndPick(t *testing.T) {
	p := NewIssuePicker(rows())
	committed, canceled := p.HandleKeyPress(key("down"))
	assert.False(t, committed)
	committed, canceled = p.HandleKeyPress(key("enter"))
	assert.True(t, committed)
	assert.False(t, canceled)
	require.NotNil(t, p.Selected())
	assert.Equal(t, 13, p.Selected().Number)
}

func TestIssuePicker_EscCancels(t *testing.T) {
	p := NewIssuePicker(rows())
	committed, canceled := p.HandleKeyPress(key("esc"))
	assert.True(t, committed)
	assert.True(t, canceled)
}

func TestIssuePicker_FilterMatchesNumberAndTitle(t *testing.T) {
	p := NewIssuePicker(rows())
	for _, r := range "flaky" {
		p.HandleKeyPress(key(string(r)))
	}
	assert.Equal(t, []int{12, 20}, p.VisibleNumbers())
	p.HandleKeyPress(key("enter"))
	assert.Equal(t, 12, p.Selected().Number)

	p = NewIssuePicker(rows())
	p.HandleKeyPress(key("2"))
	p.HandleKeyPress(key("0"))
	assert.Equal(t, []int{20}, p.VisibleNumbers())
}

func TestIssuePicker_BackspaceEditsFilter(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("z"))
	assert.Empty(t, p.VisibleNumbers())
	p.HandleKeyPress(key("backspace"))
	assert.Len(t, p.VisibleNumbers(), 3)
}

func TestIssuePicker_SelectedNilWhenFilteredOut(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("z"))
	committed, canceled := p.HandleKeyPress(key("enter"))
	assert.True(t, committed)
	assert.False(t, canceled)
	assert.Nil(t, p.Selected())
}

func TestIssuePicker_RenderStatusAndRows(t *testing.T) {
	p := NewIssuePicker(nil)
	p.SetStatus("loading…")
	out := ansi.Strip(p.Render())
	assert.Contains(t, out, "loading…")

	p.SetRows(rows())
	p.SetStatus("")
	out = ansi.Strip(p.Render())
	assert.Contains(t, out, "#12")
	assert.Contains(t, out, "Fix flaky test")
	assert.Contains(t, out, "[bug]")
	assert.True(t, strings.Contains(out, "esc cancel"))
}

func TestIssuePicker_SetRowsClampsCursor(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("down"))
	p.HandleKeyPress(key("down"))
	p.SetRows(rows()[:1])
	p.HandleKeyPress(key("enter"))
	assert.Equal(t, 12, p.Selected().Number)
}

// maxIssueRows caps items, so it only bounds rendered height while each
// row stays one line. Untruncated titles wrap and the overlay grows past
// the terminal, where PlaceOverlay clips it from the top and the header
// and filter field vanish.
func TestIssuePicker_RenderHeightStaysBounded(t *testing.T) {
	// Titles are free-form GitHub text, so truncation must be by cell
	// width: an emoji or CJK rune is one rune but two columns, and a
	// rune-count cut lets lipgloss wrap the row and regrow the overlay
	// past the terminal — the exact failure this bound exists to catch.
	for _, tc := range []struct{ name, title string }{
		{"ascii", "Rail cards silently drop the GitHub token when the terminal is narrow"},
		{"emoji", "🐛🐛🐛🐛🐛🐛 Fix the flaky reconcile test on slow CI runners 🚀🚀🚀🚀🚀🚀"},
		{"cjk", "修正テストが遅いCIランナーで断続的に失敗する問題を調査して修正する必要があります"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []IssueRow
			for i := 0; i < 30; i++ {
				rows = append(rows, IssueRow{Number: 100 + i, Title: tc.title, Labels: []string{"bug", "p1"}})
			}
			p := NewIssuePicker(rows)
			for _, w := range []int{60, 72, 100} {
				p.SetSize(w, 0)
				out := ansi.Strip(p.Render())
				lines := strings.Split(out, "\n")
				assert.LessOrEqual(t, len(lines), maxIssueRows+10,
					"width %d: %d lines for %d capped rows — rows must not wrap", w, len(lines), maxIssueRows)
				for i, ln := range lines {
					assert.LessOrEqual(t, lipgloss.Width(ln), w, "width %d line %d overflows: %q", w, i, ln)
				}
			}
		})
	}
}

// A poll landing while the picker is open must not move the highlight to
// a different issue just because rows shifted position.
func TestIssuePicker_SetRowsKeepsCursorOnSameIssue(t *testing.T) {
	p := NewIssuePicker(rows())
	p.HandleKeyPress(key("down")) // issue 13
	require.Equal(t, 13, p.Selected().Number)

	// A newer issue arrives at the head, shifting every index down one.
	p.SetRows(append([]IssueRow{{Number: 99, Title: "Brand new"}}, rows()...))
	assert.Equal(t, 13, p.Selected().Number, "cursor follows the issue, not the index")
}
