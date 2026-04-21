package overlay

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// feHeaderRows reserves rows for the title, the search prompt, the
// blank spacer below it, the blank spacer above the hint, and the
// hint itself. Kept as a constant so SetSize and View agree on how
// much vertical space the chrome consumes.
const feHeaderRows = 5

var (
	feBorder = lipgloss.NewStyle().
			BorderForeground(lipgloss.Color("#7c7cff")).
			Border(lipgloss.RoundedBorder(), false, true, true, true)

	feTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7c7cff")).
			Bold(true)

	feHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"})

	feSelectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#3a3a5a")).
			Bold(true)
)

// FileExplorerOverlay is a full-right-pane modal that lists files in
// a root directory and invokes a caller-provided callback when the
// user picks one. It implements Overlay so the app-layer overlay
// routing machinery works unchanged.
//
// The list is populated exactly once (at construction). Re-opening
// picks up anything the agent wrote meanwhile.
type FileExplorerOverlay struct {
	input    textinput.Model
	viewport viewport.Model

	root    string
	files   []string
	results []FileMatch
	cursor  int

	width  int
	height int

	// openCallback is invoked on Enter with the absolute path of the
	// selected file. It returns a tea.Cmd that the host must dispatch
	// (typically tea.ExecProcess to launch $EDITOR). Supplying this as
	// a closure keeps the overlay package free of bubbletea
	// subprocess plumbing.
	openCallback func(absPath string) tea.Cmd
}

// NewFileExplorerOverlay constructs a focused overlay over files
// (paths relative to root). The openCallback receives the joined
// absolute path when the user confirms a selection.
func NewFileExplorerOverlay(root string, files []string, open func(absPath string) tea.Cmd) *FileExplorerOverlay {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Focus()
	ti.CharLimit = 256

	f := &FileExplorerOverlay{
		input:        ti,
		viewport:     viewport.New(0, 0),
		root:         root,
		files:        files,
		openCallback: open,
	}
	f.filter()
	return f
}

// SetSize sizes the viewport to fit within the overlay chrome.
// feHeaderRows accounts for the title row, input row, spacer rows,
// and hint row; the remainder belongs to the scrollable file list.
func (f *FileExplorerOverlay) SetSize(width, height int) {
	f.width = width
	f.height = height

	borderH := feBorder.GetHorizontalFrameSize()
	borderV := feBorder.GetVerticalFrameSize()
	innerWidth := width - borderH
	if innerWidth < 0 {
		innerWidth = 0
	}
	viewportHeight := height - borderV - feHeaderRows
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	f.input.Width = innerWidth - 2
	f.viewport.Width = innerWidth
	f.viewport.Height = viewportHeight

	f.refreshViewport()
}

// HandleKey routes key events. Esc and Enter close the overlay
// (Enter also returns the open command). Arrow-style keys move the
// cursor within the filtered results; anything else is forwarded to
// the search input.
func (f *FileExplorerOverlay) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return true, nil
	case tea.KeyEnter:
		if len(f.results) == 0 {
			return false, nil
		}
		sel := f.results[f.cursor]
		abs := filepath.Join(f.root, sel.Path)
		var cmd tea.Cmd
		if f.openCallback != nil {
			cmd = f.openCallback(abs)
		}
		return true, cmd
	case tea.KeyUp, tea.KeyCtrlP:
		f.moveCursor(-1)
		return false, nil
	case tea.KeyDown, tea.KeyCtrlN:
		f.moveCursor(1)
		return false, nil
	case tea.KeyPgUp:
		f.moveCursor(-f.viewport.Height)
		return false, nil
	case tea.KeyPgDown:
		f.moveCursor(f.viewport.Height)
		return false, nil
	}

	// Forward to the text input; re-filter if the value changed.
	before := f.input.Value()
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	if f.input.Value() != before {
		f.filter()
	}
	return false, cmd
}

// View composes the overlay content: title row, search input, match
// list (via the viewport), and a hint row. The body is wrapped in
// feBorder so the widget visually matches the diff overlay it sits
// beside in the UI.
func (f *FileExplorerOverlay) View() string {
	if f.width == 0 || f.height == 0 {
		return ""
	}

	title := feTitleStyle.Render(fmt.Sprintf(" Files (%d / %d) ", len(f.results), len(f.files)))
	borderH := feBorder.GetHorizontalFrameSize()
	borderV := feBorder.GetVerticalFrameSize()
	innerWidth := f.width - borderH
	if innerWidth < 0 {
		innerWidth = 0
	}

	topLine := buildFileExplorerTopBorder(title, innerWidth+borderH)
	hint := feHintStyle.Render("↑/↓ select · enter open · esc close")

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		f.input.View(),
		"",
		f.viewport.View(),
		"",
		hint,
	)

	bodyStyled := feBorder.
		Width(innerWidth).
		Height(f.height - 1 - borderV). // -1 for the manually rendered top line
		Render(body)

	return lipgloss.JoinVertical(lipgloss.Left, topLine, bodyStyled)
}

// filter recomputes results from the current input value and resets
// the cursor to the first match so the freshly-typed query doesn't
// leave the highlight on a stale entry.
func (f *FileExplorerOverlay) filter() {
	f.results = FuzzyMatch(f.input.Value(), f.files)
	f.cursor = 0
	f.refreshViewport()
}

// refreshViewport re-renders the match list into the viewport and
// keeps the cursor in view.
func (f *FileExplorerOverlay) refreshViewport() {
	if f.viewport.Width == 0 {
		return
	}
	lines := make([]string, 0, len(f.results))
	for i, r := range f.results {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == f.cursor {
			prefix = "› "
			style = feSelectedStyle
		}
		line := prefix + r.Path
		line = truncateRight(line, f.viewport.Width)
		lines = append(lines, style.Render(line))
	}
	f.viewport.SetContent(strings.Join(lines, "\n"))

	// Keep the cursor inside the visible window. viewport.Model has no
	// direct "scroll to line" helper, so we recompute YOffset to put
	// the cursor comfortably mid-page when it falls out of view.
	if f.viewport.Height > 0 && len(f.results) > 0 {
		cursorLine := f.cursor
		if cursorLine < f.viewport.YOffset {
			f.viewport.SetYOffset(cursorLine)
		} else if cursorLine >= f.viewport.YOffset+f.viewport.Height {
			f.viewport.SetYOffset(cursorLine - f.viewport.Height + 1)
		}
	}
}

// moveCursor nudges the cursor by delta, clamped to the result
// range, and redraws the viewport.
func (f *FileExplorerOverlay) moveCursor(delta int) {
	if len(f.results) == 0 {
		f.cursor = 0
		return
	}
	next := f.cursor + delta
	if next < 0 {
		next = 0
	}
	if next >= len(f.results) {
		next = len(f.results) - 1
	}
	f.cursor = next
	f.refreshViewport()
}

// SelectedPath returns the absolute path of the current selection,
// or the empty string when no results are visible. Exposed for tests
// so they can assert on the selection without reaching into private
// fields.
func (f *FileExplorerOverlay) SelectedPath() string {
	if len(f.results) == 0 {
		return ""
	}
	return filepath.Join(f.root, f.results[f.cursor].Path)
}

// ResultCount returns the number of currently-visible results.
// Exposed for tests; callers outside ui/overlay should prefer the
// visible-in-View counter when rendering.
func (f *FileExplorerOverlay) ResultCount() int {
	return len(f.results)
}

// buildFileExplorerTopBorder composes the top line of the overlay's
// border with an inline title. Mirrors the diffTitle pattern in
// ui/split_pane.go so the two overlays read as a matched pair.
func buildFileExplorerTopBorder(title string, totalWidth int) string {
	if totalWidth < 2 {
		return ""
	}
	titleWidth := lipgloss.Width(title)
	// Left corner + one dash + title + dashes + right corner.
	dashes := totalWidth - 2 - 1 - titleWidth
	if dashes < 0 {
		dashes = 0
	}
	return "╭─" + title + strings.Repeat("─", dashes) + "╮"
}

// truncateRight cuts s to at most width runes, appending an ellipsis
// when truncated. Avoids importing reflow for a one-liner.
func truncateRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	// Simple rune-count truncation. Good enough for paths (no wide
	// CJK chars in typical source trees).
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}
