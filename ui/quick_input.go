package ui

import (
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

// QuickInputTarget specifies where submitted text should be routed.
type QuickInputTarget int

const (
	QuickInputTargetAgent    QuickInputTarget = iota // always send to agent
	QuickInputTargetTerminal                         // always send to terminal
)

var (
	quickInputHintStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"})
)

// QuickInputBar is a single-line input bar for quick interactions with tmux sessions.
type QuickInputBar struct {
	textInput textinput.Model
	Target    QuickInputTarget
	width     int
}

// NewQuickInputBar creates a focused input bar ready for typing.
func NewQuickInputBar(target QuickInputTarget) *QuickInputBar {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Focus()
	ti.CharLimit = 256
	return &QuickInputBar{
		textInput: ti,
		Target:    target,
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
	return 2 // input + hint
}

// View renders the input bar.
func (q *QuickInputBar) View() string {
	input := q.textInput.View()
	var hintText string
	switch q.Target {
	case QuickInputTargetAgent:
		hintText = "Enter to send to agent · Esc to cancel"
	case QuickInputTargetTerminal:
		hintText = "Enter to send to terminal · Esc to cancel"
	}
	hint := quickInputHintStyle.Render(hintText)
	return lipgloss.JoinVertical(lipgloss.Left, input, hint)
}
