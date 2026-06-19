package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/session"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

var previewPaneStyle = lipgloss.NewStyle().
	Foreground(compat.AdaptiveColor{Light: lipgloss.Color("#1a1a1a"), Dark: lipgloss.Color("#dddddd")})

var previewScrollFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))

// scrollFooter renders the jump-to-bottom affordance shown while a pane is
// scrolled away from the live tail. newLines is the count of live-output lines
// accrued below the window since scrolling started. Shared by both panes.
func scrollFooter(newLines int) string {
	if newLines > 0 {
		return fmt.Sprintf("▼ %d new line(s) — Esc/End to jump to bottom", newLines)
	}
	return "▲ scrolled — Esc/End to jump to bottom"
}

// PreviewPane renders the agent tmux pane's content in the top half of the
// split view. It tails the emulator's live screen at scrollOffset 0 and paints
// a window into the emulator scrollback when scrolled up, while live output
// keeps flowing. lastInstanceTitle resets the scroll position on selection
// change rather than persisting a stale offset.
type PreviewPane struct {
	width  int
	height int

	previewState      previewState
	lastInstanceTitle string // tracks the current instance to reset scroll on change

	// scrollOffset is lines-from-bottom; 0 = live tail. Increasing scrolls up
	// into the emulator scrollback. Clamped to [0, ScrollbackLen()].
	scrollOffset int
	// scrollbackAtScrollStart is ScrollbackLen() captured when the offset left
	// 0, used to count "new lines below" without scanning the buffer.
	scrollbackAtScrollStart int
	// lastScrollbackLen caches ScrollbackLen() from the last UpdateContent so
	// ScrollPercent is lock-free and consistent with the last render.
	lastScrollbackLen int
	// newLinesBelow is the live-output line count accrued since scrolling up.
	newLinesBelow int
}

type previewState struct {
	// fallback is true if the preview pane is displaying fallback text
	fallback bool
	// text is the text displayed in the preview pane
	text string
}

// NewPreviewPane constructs a PreviewPane at live tail; the caller must SetSize
// before the first render.
func NewPreviewPane() *PreviewPane {
	return &PreviewPane{}
}

// SetSize records the pane dimensions. maxHeight caps the visible height —
// content exceeding it is truncated with an ellipsis at live tail or windowed
// when scrolled.
func (p *PreviewPane) SetSize(width, maxHeight int) {
	p.width = width
	p.height = maxHeight
}

// setFallbackState sets the preview state with fallback text and a message
func (p *PreviewPane) setFallbackState(message string) {
	p.previewState = previewState{
		fallback: true,
		text:     lipgloss.JoinVertical(lipgloss.Center, FallBackText, "", message),
	}
}

// UpdateContent refreshes the pane from the given instance. At scrollOffset 0 it
// tails the live emulator screen; when scrolled it paints a window of the
// emulator scrollback at the current offset (live output keeps accruing below).
// Falls back to splash text for nil / loading / paused instances and resets the
// offset when the selected instance changes.
func (p *PreviewPane) UpdateContent(instance *session.Instance) error {
	// Reset to live tail when the selected instance changes.
	newTitle := ""
	if instance != nil {
		newTitle = instance.Title
	}
	if newTitle != p.lastInstanceTitle {
		p.lastInstanceTitle = newTitle
		p.scrollOffset = 0
		p.newLinesBelow = 0
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
				Foreground(lipgloss.Color("#FFD700")).
				Render(fmt.Sprintf(
					"The instance can be checked out at '%s' (copied to your clipboard)",
					instance.GetBranch(),
				)),
		))
		return nil
	}

	if p.scrollOffset == 0 {
		// Live tail: emulator visible screen (or capture-pane fallback).
		content, err := instance.Preview()
		if err != nil {
			return err
		}
		if len(content) == 0 && !instance.Started() {
			p.setFallbackState("Please enter a name for the instance.")
		} else {
			p.previewState = previewState{fallback: false, text: content}
		}
		p.newLinesBelow = 0
		return nil
	}

	// Scrolled: render a height-1 window at the offset (last row is reserved
	// for the jump-to-bottom footer).
	total, ok := instance.ScrollbackLen()
	if !ok {
		// No emulator (snapshot/Windows): scrolling unsupported -> live tail.
		p.scrollOffset = 0
		content, _ := instance.Preview()
		p.previewState = previewState{fallback: false, text: content}
		return nil
	}
	p.lastScrollbackLen = total
	if p.scrollOffset > total {
		p.scrollOffset = total
	}
	rows := p.height - 1
	if rows < 1 {
		rows = 1
	}
	window, _ := instance.RenderWindow(p.scrollOffset, rows)
	p.previewState = previewState{fallback: false, text: window}
	newBelow := total - p.scrollbackAtScrollStart
	if newBelow < 0 {
		newBelow = 0
	}
	p.newLinesBelow = newBelow
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

	// Scrolled: render the windowed history with a jump-to-bottom footer.
	if p.scrollOffset > 0 {
		footer := previewScrollFooterStyle.Render(scrollFooter(p.newLinesBelow))
		body := lipgloss.JoinVertical(lipgloss.Left, p.previewState.text, footer)
		return previewPaneStyle.Width(p.width).Render(body)
	}

	// Live-tail display
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

// ScrollUp scrolls one line up into scrollback.
func (p *PreviewPane) ScrollUp(instance *session.Instance) error { return p.scrollBy(instance, +1) }

// ScrollDown scrolls one line down toward the live tail.
func (p *PreviewPane) ScrollDown(instance *session.Instance) error { return p.scrollBy(instance, -1) }

// PageUp scrolls up by half a pane height.
func (p *PreviewPane) PageUp(instance *session.Instance) error {
	return p.scrollBy(instance, +(p.height / 2))
}

// PageDown scrolls down by half a pane height.
func (p *PreviewPane) PageDown(instance *session.Instance) error {
	return p.scrollBy(instance, -(p.height / 2))
}

// GotoTop jumps to the oldest scrollback line.
func (p *PreviewPane) GotoTop(instance *session.Instance) error {
	maxOff := 0
	if instance != nil {
		if m, ok := instance.ScrollbackLen(); ok {
			maxOff = m
		}
	}
	return p.setOffset(instance, maxOff)
}

// GotoBottom returns to the live tail.
func (p *PreviewPane) GotoBottom(instance *session.Instance) error {
	return p.setOffset(instance, 0)
}

// ScrollPercent returns the scroll position as a fraction [0, 1]; 1.0 == live
// tail (bottom).
func (p *PreviewPane) ScrollPercent() float64 {
	if p.scrollOffset <= 0 || p.lastScrollbackLen <= 0 {
		return 1.0
	}
	return 1.0 - float64(p.scrollOffset)/float64(p.lastScrollbackLen)
}

// IsScrolling reports whether the pane is scrolled away from the live tail.
func (p *PreviewPane) IsScrolling() bool {
	return p.scrollOffset > 0
}

// ResetToNormalMode returns the pane to the live tail.
func (p *PreviewPane) ResetToNormalMode(instance *session.Instance) error {
	return p.setOffset(instance, 0)
}

func (p *PreviewPane) scrollBy(instance *session.Instance, delta int) error {
	return p.setOffset(instance, p.scrollOffset+delta)
}

// setOffset clamps and applies a new lines-from-bottom offset. Marks the
// scrollback length on the transition away from the live tail so the
// "new lines below" counter has a baseline.
func (p *PreviewPane) setOffset(instance *session.Instance, off int) error {
	if instance != nil && instance.GetStatus() == session.Paused {
		return nil
	}
	maxOff := 0
	if instance != nil {
		if m, ok := instance.ScrollbackLen(); ok {
			maxOff = m
		}
	}
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	wasBottom := p.scrollOffset == 0
	p.scrollOffset = off
	if wasBottom && off > 0 {
		p.scrollbackAtScrollStart = maxOff // baseline for the new-lines counter
	}
	if off == 0 {
		p.newLinesBelow = 0
	}
	return nil
}
