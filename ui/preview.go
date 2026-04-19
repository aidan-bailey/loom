package ui

import (
	"claude-squad/session"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var previewPaneStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

type PreviewPane struct {
	width  int
	height int

	previewState      previewState
	isScrolling       bool
	viewport          viewport.Model
	lastInstanceTitle string // tracks the current instance to reset scroll on change
}

type previewState struct {
	// fallback is true if the preview pane is displaying fallback text
	fallback bool
	// text is the text displayed in the preview pane
	text string
}

func NewPreviewPane() *PreviewPane {
	return &PreviewPane{
		viewport: viewport.New(0, 0),
	}
}

func (p *PreviewPane) SetSize(width, maxHeight int) {
	p.width = width
	p.height = maxHeight
	p.viewport.Width = width
	p.viewport.Height = maxHeight
}

// setFallbackState sets the preview state with fallback text and a message
func (p *PreviewPane) setFallbackState(message string) {
	p.previewState = previewState{
		fallback: true,
		text:     lipgloss.JoinVertical(lipgloss.Center, FallBackText, "", message),
	}
}

// Updates the preview pane content with the tmux pane content
func (p *PreviewPane) UpdateContent(instance *session.Instance) error {
	// Reset scroll mode when the selected instance changes.
	newTitle := ""
	if instance != nil {
		newTitle = instance.Title
	}
	if newTitle != p.lastInstanceTitle {
		p.lastInstanceTitle = newTitle
		if p.isScrolling {
			p.isScrolling = false
			p.viewport.SetContent("")
			p.viewport.GotoTop()
		}
	}

	// Auto-exit scroll mode when viewport is at the bottom (back to live output).
	if p.isScrolling && p.viewport.AtBottom() {
		p.isScrolling = false
		p.viewport.SetContent("")
		p.viewport.GotoTop()
	}

	switch {
	case instance == nil:
		p.setFallbackState("No agents running yet. Spin up a new instance with 'n' to get started!")
		return nil
	case instance.GetStatus() == session.Loading:
		p.setFallbackState("Setting up workspace...")
		return nil
	case instance.GetStatus() == session.Paused:
		p.setFallbackState(lipgloss.JoinVertical(lipgloss.Center,
			"Session is paused. Press 'r' to resume.",
			"",
			lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{
					Light: "#FFD700",
					Dark:  "#FFD700",
				}).
				Render(fmt.Sprintf(
					"The instance can be checked out at '%s' (copied to your clipboard)",
					instance.GetBranch(),
				)),
		))
		return nil
	}

	var content string
	var err error

	// If in scroll mode but haven't captured content yet, do it now
	if p.isScrolling && p.viewport.Height > 0 && len(p.viewport.View()) == 0 {
		// Capture full pane content including scrollback history using capture-pane -p -S -
		content, err = instance.PreviewFullHistory()
		if err != nil {
			return err
		}

		// Set content in the viewport
		footer := lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
			Render("ESC to exit scroll mode")

		p.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, content, footer))
	} else if !p.isScrolling {
		// In normal mode, use the usual preview
		content, err = instance.Preview()
		if err != nil {
			return err
		}

		// Always update the preview state with content, even if empty
		// This ensures that newly created instances will display their content immediately
		if len(content) == 0 && !instance.Started() {
			p.setFallbackState("Please enter a name for the instance.")
		} else {
			// Update the preview state with the current content
			p.previewState = previewState{
				fallback: false,
				text:     content,
			}
		}
	}

	return nil
}

// Returns the preview pane content as a string.
func (p *PreviewPane) String() string {
	if p.width == 0 || p.height == 0 {
		return strings.Repeat("\n", p.height)
	}

	if p.previewState.fallback {
		// Calculate available height for fallback text
		availableHeight := p.height - 3 - 4 // 2 for borders, 1 for margin, 1 for padding

		// Count the number of lines in the fallback text
		fallbackLines := len(strings.Split(p.previewState.text, "\n"))

		// Calculate padding needed above and below to center the content
		totalPadding := availableHeight - fallbackLines
		topPadding := 0
		bottomPadding := 0
		if totalPadding > 0 {
			topPadding = totalPadding / 2
			bottomPadding = totalPadding - topPadding // accounts for odd numbers
		}

		// Build the centered content
		var lines []string
		if topPadding > 0 {
			lines = append(lines, strings.Repeat("\n", topPadding))
		}
		lines = append(lines, p.previewState.text)
		if bottomPadding > 0 {
			lines = append(lines, strings.Repeat("\n", bottomPadding))
		}

		// Center both vertically and horizontally
		return previewPaneStyle.
			Width(p.width).
			Align(lipgloss.Center).
			Render(strings.Join(lines, ""))
	}

	// If in copy mode, use the viewport to display scrollable content
	if p.isScrolling {
		return p.viewport.View()
	}

	// Normal mode display
	// Calculate available height accounting for border and margin
	availableHeight := p.height - 1 //  1 for ellipsis

	lines := strings.Split(p.previewState.text, "\n")

	// Truncate if we have more lines than available height
	if availableHeight > 0 {
		if len(lines) > availableHeight {
			lines = lines[:availableHeight]
			lines = append(lines, "...")
		} else {
			// Pad with empty lines to fill available height
			padding := availableHeight - len(lines)
			lines = append(lines, make([]string, padding)...)
		}
	}

	content := strings.Join(lines, "\n")
	rendered := previewPaneStyle.Width(p.width).Render(content)
	return rendered
}

// enterScrollMode captures the full pane history and seeds the viewport.
// Callers must apply a motion (LineUp, HalfViewUp, etc.) after to keep
// AtBottom() false — otherwise the next UpdateContent auto-exits.
func (p *PreviewPane) enterScrollMode(instance *session.Instance) error {
	content, err := instance.PreviewFullHistory()
	if err != nil {
		return err
	}

	footer := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
		Render("ESC to exit scroll mode")

	p.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, content, footer))
	p.viewport.GotoBottom()
	p.isScrolling = true
	return nil
}

// ScrollUp scrolls up in the viewport
func (p *PreviewPane) ScrollUp(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}
	if !p.isScrolling {
		if err := p.enterScrollMode(instance); err != nil {
			return err
		}
	}
	p.viewport.LineUp(1)
	return nil
}

// ScrollDown scrolls down in the viewport
func (p *PreviewPane) ScrollDown(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}
	if !p.isScrolling {
		return nil
	}
	p.viewport.LineDown(1)
	return nil
}

// PageUp scrolls up by half a viewport height.
func (p *PreviewPane) PageUp(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}
	if !p.isScrolling {
		if err := p.enterScrollMode(instance); err != nil {
			return err
		}
	}
	p.viewport.HalfViewUp()
	return nil
}

// PageDown scrolls down by half a viewport height.
func (p *PreviewPane) PageDown(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}
	if !p.isScrolling {
		return nil
	}
	p.viewport.HalfViewDown()
	return nil
}

// GotoTop jumps the viewport to the start of captured history.
func (p *PreviewPane) GotoTop(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}
	if !p.isScrolling {
		if err := p.enterScrollMode(instance); err != nil {
			return err
		}
	}
	p.viewport.GotoTop()
	return nil
}

// GotoBottom exits scroll mode and returns to live tail.
func (p *PreviewPane) GotoBottom(instance *session.Instance) error {
	return p.ResetToNormalMode(instance)
}

// ScrollPercent returns the viewport position as a fraction [0, 1].
// Returns 1.0 when not in scroll mode (live tail is "at the bottom").
func (p *PreviewPane) ScrollPercent() float64 {
	if !p.isScrolling {
		return 1.0
	}
	return p.viewport.ScrollPercent()
}

// IsScrolling returns whether the preview pane is in scroll mode.
func (p *PreviewPane) IsScrolling() bool {
	return p.isScrolling
}

// ResetToNormalMode exits scroll mode and returns to normal mode
func (p *PreviewPane) ResetToNormalMode(instance *session.Instance) error {
	if instance == nil || instance.GetStatus() == session.Paused {
		return nil
	}

	if p.isScrolling {
		p.isScrolling = false
		// Reset viewport
		p.viewport.SetContent("")
		p.viewport.GotoTop()

		// Immediately update content instead of waiting for next UpdateContent call
		content, err := instance.Preview()
		if err != nil {
			return err
		}
		p.previewState.text = content
	}

	return nil
}
