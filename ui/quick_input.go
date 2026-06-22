package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// QuickInputAction represents the result of handling a key press in the input bar.
type QuickInputAction int

// QuickInputContinue, QuickInputSubmit, and QuickInputCancel are the
// terminal states HandleKey returns so the parent model knows whether
// to keep the bar open, dispatch the contents, or dismiss the bar.
const (
	QuickInputContinue QuickInputAction = iota
	QuickInputSubmit
	QuickInputCancel
)

// QuickInputTarget specifies where submitted text should be routed.
type QuickInputTarget int

// QuickInputTargetAgent sends submitted text to the agent pane; the
// terminal variant routes to the bottom pane. The target is chosen at
// construction time and is immutable for the life of the bar.
const (
	QuickInputTargetAgent QuickInputTarget = iota
	QuickInputTargetTerminal
)

var (
	quickInputHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
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
func (q *QuickInputBar) HandleKeyPress(msg tea.KeyPressMsg) QuickInputAction {
	switch msg.Code {
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
	q.textInput.SetWidth(w - 4) // account for prompt and padding
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
