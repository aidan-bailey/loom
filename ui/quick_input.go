package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// QuickInputAction represents the result of handling a key press in the input bar.
type QuickInputAction int

const (
	QuickInputContinue QuickInputAction = iota
	QuickInputSubmit
	QuickInputCancel
)

var (
	quickInputHintStyle      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"})
	quickInputSeparatorStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#DDDADA", Dark: "#3C3C3C"})
)

// QuickInputBar is a single-line input bar for quick interactions with tmux sessions.
type QuickInputBar struct {
	textInput textinput.Model
	width     int
}

// NewQuickInputBar creates a focused input bar ready for typing.
func NewQuickInputBar() *QuickInputBar {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Focus()
	ti.CharLimit = 256
	return &QuickInputBar{
		textInput: ti,
	}
}

// HandleKeyPress processes a key event and returns the resulting action.
func (q *QuickInputBar) HandleKeyPress(msg tea.KeyMsg) QuickInputAction {
	switch msg.Type {
	case tea.KeyEnter:
		return QuickInputSubmit
	case tea.KeyEsc:
		return QuickInputCancel
	default:
		q.textInput, _ = q.textInput.Update(msg)
		return QuickInputContinue
	}
}

// Value returns the current text in the input bar.
func (q *QuickInputBar) Value() string {
	return q.textInput.Value()
}

// SetWidth sets the rendering width of the input bar.
func (q *QuickInputBar) SetWidth(w int) {
	q.width = w
	q.textInput.Width = w - 4 // account for prompt and padding
}

// Height returns the number of lines the input bar occupies.
func (q *QuickInputBar) Height() int {
	return 3 // separator + input + hint
}

// View renders the input bar.
func (q *QuickInputBar) View() string {
	w := q.width
	if w <= 0 {
		w = 40
	}
	separator := quickInputSeparatorStyle.Render(strings.Repeat("─", w))
	input := q.textInput.View()
	hint := quickInputHintStyle.Render("Enter to send · Esc to cancel")
	return lipgloss.JoinVertical(lipgloss.Left, separator, input, hint)
}
