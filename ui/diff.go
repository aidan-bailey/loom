package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/session"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// AdditionStyle, DeletionStyle, and HunkStyle are the lipgloss styles
// applied line-by-line by colorizeDiff to render a colorized git diff.
// They are exported so tests and callers composing their own diff
// renderers can reuse the same palette.
var (
	AdditionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	DeletionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	HunkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#0ea5e9"))
)

// DiffPane renders the colorized git diff overlay shown when the user
// toggles it on with `d`. It caches the last rendered diff content so
// tick-driven redraws skip re-colorization when the underlying diff
// has not changed — cheap on every render, noticeable at large diffs.
type DiffPane struct {
	viewport        viewport.Model
	diff            string
	stats           string
	lastDiffContent string // cache key to avoid re-colorizing unchanged diffs
	width           int
	height          int
}

// NewDiffPane constructs a DiffPane with a zero-sized viewport; the
// caller must SetSize before the first render.
func NewDiffPane() *DiffPane {
	return &DiffPane{
		viewport: viewport.New(0, 0),
	}
}

// SetSize resizes the embedded viewport and invalidates the cached
// colorized diff so the next SetDiff re-renders even when the
// underlying content is unchanged.
func (d *DiffPane) SetSize(width, height int) {
	d.width = width
	d.height = height
	d.lastDiffContent = ""
	d.viewport.Width = width
	d.viewport.Height = height
	if d.diff != "" || d.stats != "" {
		d.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, d.stats, d.diff))
	}
}

// SetDiff replaces the rendered diff from instance's current diff
// stats. No-ops when the content has not changed since the last call,
// and shows fallback text for nil / unstarted / errored instances.
func (d *DiffPane) SetDiff(instance *session.Instance) {
	centeredFallbackMessage := lipgloss.Place(
		d.width,
		d.height,
		lipgloss.Center,
		lipgloss.Center,
		"No changes",
	)

	if instance == nil || !instance.Started() {
		d.viewport.SetContent(centeredFallbackMessage)
		return
	}

	stats := instance.GetDiffStats()
	if stats == nil {
		// Show loading message if worktree is not ready
		centeredMessage := lipgloss.Place(
			d.width,
			d.height,
			lipgloss.Center,
			lipgloss.Center,
			"Setting up worktree...",
		)
		d.viewport.SetContent(centeredMessage)
		return
	}

	if stats.Error != nil {
		// Show error message
		centeredMessage := lipgloss.Place(
			d.width,
			d.height,
			lipgloss.Center,
			lipgloss.Center,
			fmt.Sprintf("Error: %v", stats.Error),
		)
		d.viewport.SetContent(centeredMessage)
		return
	}

	if stats.IsEmpty() {
		d.stats = ""
		d.diff = ""
		d.viewport.SetContent(centeredFallbackMessage)
	} else {
		if stats.Content == d.lastDiffContent {
			return
		}
		d.lastDiffContent = stats.Content
		additions := AdditionStyle.Render(fmt.Sprintf("%d additions(+)", stats.Added))
		deletions := DeletionStyle.Render(fmt.Sprintf("%d deletions(-)", stats.Removed))
		d.stats = lipgloss.JoinHorizontal(lipgloss.Center, additions, " ", deletions)
		d.diff = colorizeDiff(stats.Content)
		d.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, d.stats, d.diff))
	}
}

func (d *DiffPane) String() string {
	return d.viewport.View()
}

// ScrollUp scrolls the viewport up
func (d *DiffPane) ScrollUp() {
	d.viewport.LineUp(1)
}

// ScrollDown scrolls the viewport down
func (d *DiffPane) ScrollDown() {
	d.viewport.LineDown(1)
}

// PageUp scrolls the viewport up by half a view.
func (d *DiffPane) PageUp() {
	d.viewport.HalfViewUp()
}

// PageDown scrolls the viewport down by half a view.
func (d *DiffPane) PageDown() {
	d.viewport.HalfViewDown()
}

// GotoTop jumps the viewport to the start.
func (d *DiffPane) GotoTop() {
	d.viewport.GotoTop()
}

// GotoBottom jumps the viewport to the end.
func (d *DiffPane) GotoBottom() {
	d.viewport.GotoBottom()
}

// ScrollPercent returns the viewport position as a fraction [0, 1].
func (d *DiffPane) ScrollPercent() float64 {
	return d.viewport.ScrollPercent()
}

func colorizeDiff(diff string) string {
	var coloredOutput strings.Builder
	coloredOutput.Grow(len(diff) * 2)

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		if len(line) > 0 {
			if strings.HasPrefix(line, "@@") {
				// Color hunk headers cyan
				coloredOutput.WriteString(HunkStyle.Render(line) + "\n")
			} else if line[0] == '+' && (len(line) == 1 || line[1] != '+') {
				// Color added lines green, excluding metadata like '+++'
				coloredOutput.WriteString(AdditionStyle.Render(line) + "\n")
			} else if line[0] == '-' && (len(line) == 1 || line[1] != '-') {
				// Color removed lines red, excluding metadata like '---'
				coloredOutput.WriteString(DeletionStyle.Render(line) + "\n")
			} else {
				// Print metadata and unchanged lines without color
				coloredOutput.WriteString(line + "\n")
			}
		} else {
			// Preserve empty lines
			coloredOutput.WriteString("\n")
		}
	}

	return coloredOutput.String()
}
