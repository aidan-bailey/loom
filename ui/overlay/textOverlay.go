package overlay

import (
	"github.com/aidan-bailey/loom/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TextOverlay represents a text screen overlay
type TextOverlay struct {
	// Whether the overlay has been dismissed
	Dismissed bool
	// Callback function to be called when the overlay is dismissed.
	// May return a tea.Cmd for the host to dispatch after the overlay closes.
	OnDismiss func() tea.Cmd
	// Content to display in the overlay
	content string

	width int
}

// NewTextOverlay creates a new text screen overlay with the given title and content
func NewTextOverlay(content string) *TextOverlay {
	return &TextOverlay{
		Dismissed: false,
		content:   content,
	}
}

// HandleKeyPress processes a key press and updates the state.
// Returns (shouldClose, dismissCmd). dismissCmd is whatever the OnDismiss
// callback returned (possibly nil) and should be dispatched by the caller.
func (t *TextOverlay) HandleKeyPress(msg tea.KeyMsg) (bool, tea.Cmd) {
	// Close on any key
	t.Dismissed = true
	// Call the OnDismiss callback if it exists
	var cmd tea.Cmd
	if t.OnDismiss != nil {
		cmd = t.OnDismiss()
	}
	return true, cmd
}

// HandleKey satisfies the Overlay interface. Delegates to
// HandleKeyPress, which already returns (closed, cmd).
func (t *TextOverlay) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	return t.HandleKeyPress(msg)
}

// View satisfies the Overlay interface.
func (t *TextOverlay) View() string {
	return t.Render()
}

// SetSize satisfies the Overlay interface. TextOverlay uses only
// width; height is accepted but ignored.
func (t *TextOverlay) SetSize(width, _ int) {
	t.SetWidth(width)
}

// Render renders the text overlay
func (t *TextOverlay) Render(opts ...WhitespaceOption) string {
	// Create styles
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.OverlayBorder).
		Padding(1, 2).
		Width(t.width)

	// Apply the border style and return
	return style.Render(t.content)
}

func (t *TextOverlay) SetWidth(width int) {
	t.width = width
}
