package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// agentScrollTTL bounds how often the alt-screen state is probed (a tmux
// subprocess) on the scroll hot path. agentPageNotches is how many wheel notches
// a PageUp/Down forwards to a TUI agent.
const (
	agentScrollTTL   = 750 * time.Millisecond
	agentPageNotches = 5
)

// ansiRE strips SGR/CSI escapes so logged samples are human-readable.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// sampleLine returns the first non-blank line (ANSI-stripped, truncated) from a
// windowed slice, for diagnostic logging of what a scroll window is showing.
func sampleLine(lines []string) string {
	for _, l := range lines {
		clean := strings.TrimSpace(ansiRE.ReplaceAllString(l, ""))
		if clean != "" {
			if len(clean) > 60 {
				return clean[:60]
			}
			return clean
		}
	}
	return "(all-blank)"
}

// scrollToTopOffset is a sentinel passed to setOffset for "go to top"; the next
// UpdateContent clamps it to the real top of the captured buffer.
const scrollToTopOffset = 1 << 30

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

// windowLines returns `rows` lines from `lines` whose bottom sits `fromBottom`
// lines above the end of the slice, padding out-of-range positions with blanks.
// Shared by both panes to window a captured history buffer.
func windowLines(lines []string, fromBottom, rows int) []string {
	if rows < 1 {
		return nil
	}
	out := make([]string, rows)
	total := len(lines)
	bottom := total - fromBottom
	top := bottom - rows
	for i := 0; i < rows; i++ {
		idx := top + i
		if idx >= 0 && idx < total {
			out[i] = lines[idx]
		}
	}
	return out
}

// PreviewPane renders the agent tmux pane's content in the top half of the
// split view. It tails the emulator's live screen at scrollOffset 0, and when
// scrolled paints a window into tmux's authoritative history (capture-pane -S -)
// while live output keeps flowing. lastInstanceTitle resets the scroll position
// on selection change rather than persisting a stale offset.
type PreviewPane struct {
	width  int
	height int

	previewState      previewState
	lastInstanceTitle string // tracks the current instance to reset scroll on change

	// scrollOffset is lines-from-bottom into the captured history buffer; 0 =
	// live tail. setOffset only floors it at 0; UpdateContent clamps to the
	// real top once it has captured the buffer.
	scrollOffset int
	// scrollStarting marks the first UpdateContent after leaving the live tail,
	// so the new-lines baseline is set from the freshly captured buffer.
	scrollStarting bool
	// totalAtScrollStart is the buffer line count when scrolling began; the
	// "new lines below" count is total-now minus this.
	totalAtScrollStart int
	// lastTotal is the buffer line count from the previous scrolled tick, used
	// to anchor the view to content as new output appends below.
	lastTotal int
	// newLinesBelow is the live-output line count accrued since scrolling up.
	newLinesBelow int

	// altScreen caches whether the agent is a full-screen TUI (no tmux
	// scrollback); when true, scrolling is forwarded into the agent rather than
	// windowed. Refreshed at most once per agentScrollTTL on the scroll path.
	altScreen        bool
	altScreenChecked time.Time
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

// liveTail sets the pane content to the live (offset 0) emulator screen.
func (p *PreviewPane) liveTail(instance *session.Instance) error {
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

// UpdateContent refreshes the pane from the given instance. At scrollOffset 0 it
// tails the live emulator screen; when scrolled it windows tmux's authoritative
// history (capture-pane -S -) at the current offset, anchoring the view to its
// content as live output accrues below. Falls back to splash text for
// nil/loading/paused instances and resets the offset on instance change.
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
		p.lastTotal = 0
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
		return p.liveTail(instance)
	}

	// Scrolled: window into tmux's authoritative buffer (scrollback + visible).
	// The in-process emulator only mirrors the visible screen, so windowed
	// history must come from tmux, not emu.Scrollback().
	hist, ok := instance.CaptureHistory()
	if !ok {
		log.For("panescroll").Info("preview.capture_failed", "offset", p.scrollOffset, "histBytes", len(hist))
		p.scrollOffset = 0
		return p.liveTail(instance)
	}
	lines := strings.Split(strings.TrimRight(hist, "\n"), "\n")
	total := len(lines)
	rows := p.height - 1
	if rows < 1 {
		rows = 1
	}
	log.For("panescroll").Info("preview.window_calc",
		"offset", p.scrollOffset, "total", total, "rows", rows, "histBytes", len(hist))

	switch {
	case p.scrollStarting:
		// First tick of this scroll gesture: baseline the new-lines counter.
		p.totalAtScrollStart = total
		p.lastTotal = total
		p.scrollStarting = false
	case p.lastTotal > 0 && total > p.lastTotal:
		// New output appended below while scrolled: bump the offset by the same
		// amount so the content under the cursor stays put.
		p.scrollOffset += total - p.lastTotal
	}
	p.lastTotal = total

	maxOff := total - rows
	if maxOff < 0 {
		maxOff = 0
	}
	if p.scrollOffset > maxOff {
		p.scrollOffset = maxOff
	}
	if p.scrollOffset <= 0 {
		// Anchored back to the bottom -> live tail.
		p.scrollOffset = 0
		return p.liveTail(instance)
	}

	window := windowLines(lines, p.scrollOffset, rows)
	log.For("panescroll").Info("preview.update.scrolled",
		"offset", p.scrollOffset, "total", total, "rows", rows, "maxOff", maxOff,
		"histBytes", len(hist), "sample", sampleLine(window))
	p.previewState = previewState{fallback: false, text: strings.Join(window, "\n")}
	if newBelow := total - p.totalAtScrollStart; newBelow > 0 {
		p.newLinesBelow = newBelow
	} else {
		p.newLinesBelow = 0
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

// isAgentTUI reports whether the agent is a full-screen TUI (alt-screen, no tmux
// scrollback), caching the tmux probe for agentScrollTTL so rapid wheel events
// don't spawn a subprocess each.
func (p *PreviewPane) isAgentTUI(instance *session.Instance) bool {
	if instance == nil {
		return false
	}
	now := time.Now()
	if !p.altScreenChecked.IsZero() && now.Sub(p.altScreenChecked) < agentScrollTTL {
		return p.altScreen
	}
	p.altScreen = instance.IsAlternateScreen()
	p.altScreenChecked = now
	return p.altScreen
}

// forwardWheel forwards n wheel notches to a TUI agent so it scrolls its own
// view; Loom stays at the live tail and shows the redraw.
func (p *PreviewPane) forwardWheel(instance *session.Instance, up bool, n int) error {
	log.For("panescroll").Info("preview.forward_wheel", "up", up, "notches", n)
	return instance.ForwardWheel(up, n)
}

// ScrollUp scrolls one line up into history (or forwards a wheel-up to a TUI agent).
func (p *PreviewPane) ScrollUp(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, true, 1)
	}
	return p.scrollBy(instance, +1)
}

// ScrollDown scrolls one line down toward the live tail (or forwards a wheel-down).
func (p *PreviewPane) ScrollDown(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, false, 1)
	}
	return p.scrollBy(instance, -1)
}

// PageUp scrolls up by half a pane height (or forwards a burst of wheel-ups).
func (p *PreviewPane) PageUp(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, true, agentPageNotches)
	}
	return p.scrollBy(instance, +(p.height / 2))
}

// PageDown scrolls down by half a pane height (or forwards a burst of wheel-downs).
func (p *PreviewPane) PageDown(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, false, agentPageNotches)
	}
	return p.scrollBy(instance, -(p.height / 2))
}

// GotoTop jumps to the oldest line of captured history (TUI: a large wheel-up burst).
func (p *PreviewPane) GotoTop(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, true, 30)
	}
	return p.setOffset(instance, scrollToTopOffset)
}

// GotoBottom returns to the live tail (TUI: a large wheel-down burst).
func (p *PreviewPane) GotoBottom(instance *session.Instance) error {
	if p.isAgentTUI(instance) {
		return p.forwardWheel(instance, false, 30)
	}
	return p.setOffset(instance, 0)
}

// ScrollPercent returns the scroll position as a fraction [0, 1]; 1.0 == live
// tail (bottom).
func (p *PreviewPane) ScrollPercent() float64 {
	if p.scrollOffset <= 0 || p.lastTotal <= 0 {
		return 1.0
	}
	return 1.0 - float64(p.scrollOffset)/float64(p.lastTotal)
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

// setOffset floors a new lines-from-bottom offset at 0 and marks the start of a
// scroll gesture. The real top-of-buffer clamp happens in UpdateContent, which
// has the captured line count.
func (p *PreviewPane) setOffset(instance *session.Instance, off int) error {
	if instance != nil && instance.GetStatus() == session.Paused {
		return nil
	}
	if off < 0 {
		off = 0
	}
	prev := p.scrollOffset
	wasBottom := p.scrollOffset == 0
	p.scrollOffset = off
	if prev != off {
		log.For("panescroll").Info("preview.setOffset", "from", prev, "to", off, "height", p.height)
	}
	if wasBottom && off > 0 {
		p.scrollStarting = true
	}
	if off == 0 {
		p.newLinesBelow = 0
		p.lastTotal = 0
	}
	return nil
}
