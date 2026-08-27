package overlay

import (
	"github.com/aidan-bailey/loom/ui"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// newBranchOption is the sentinel item meaning "cut a fresh branch" rather
// than check out an existing one. It is matched by identity in
// visibleItems/GetSelectedBranch, so it must stay a fixed string — the text
// actually shown to the user comes from newBranchLabel, which appends the
// resolved base branch once it is known.
const newBranchOption = "New branch"

// BranchPicker is an embeddable component for selecting a branch.
// It does not hold the full branch list — results are provided asynchronously
// via SetResults after each debounced search.
type BranchPicker struct {
	results       []string // current search results (from git)
	filter        string   // current filter text
	filterVersion uint64   // incremented on each filter change
	cursor        int      // index into visibleItems()
	focused       bool
	width         int
	showNewBranch bool // whether to show the "New branch" option
	// baseBranchName is the ref new branches will be cut from, resolved
	// asynchronously by the host (see git.ResolveBaseCommit). Empty until
	// that lands, in which case the row makes no claim about the base.
	baseBranchName string
}

// NewBranchPicker creates a new empty branch picker.
func NewBranchPicker() *BranchPicker {
	return &BranchPicker{
		showNewBranch: true,
	}
}

// SetWidth sets the width of the branch picker.
func (bp *BranchPicker) SetWidth(w int) {
	bp.width = w
}

// Focus gives the branch picker focus.
func (bp *BranchPicker) Focus() {
	bp.focused = true
}

// Blur removes focus from the branch picker.
func (bp *BranchPicker) Blur() {
	bp.focused = false
}

// IsFocused returns whether the branch picker is focused.
func (bp *BranchPicker) IsFocused() bool {
	return bp.focused
}

// GetFilter returns the current filter text.
func (bp *BranchPicker) GetFilter() string {
	return bp.filter
}

// GetFilterVersion returns a monotonically increasing version that changes on every filter edit.
func (bp *BranchPicker) GetFilterVersion() uint64 {
	return bp.filterVersion
}

// HandleKeyPress processes a key event. Returns (consumed, filterChanged).
func (bp *BranchPicker) HandleKeyPress(msg tea.KeyPressMsg) (consumed bool, filterChanged bool) {
	switch msg.Code {
	case tea.KeyUp:
		if bp.cursor > 0 {
			bp.cursor--
		}
		return true, false
	case tea.KeyDown:
		items := bp.visibleItems()
		if bp.cursor < len(items)-1 {
			bp.cursor++
		}
		return true, false
	case tea.KeyBackspace:
		if len(bp.filter) > 0 {
			runes := []rune(bp.filter)
			bp.filter = string(runes[:len(runes)-1])
			bp.filterVersion++
			return true, true
		}
		return true, false
	default:
		// Printable text (was tea.KeyRunes / tea.KeySpace in v1).
		if msg.Text != "" {
			bp.filter += msg.Text
			bp.filterVersion++
			return true, true
		}
	}
	return false, false
}

// SetResults updates the branch list with search results.
// version must match filterVersion for the results to be accepted (prevents stale updates).
func (bp *BranchPicker) SetResults(branches []string, version uint64) {
	if version != bp.filterVersion {
		return // stale results
	}
	bp.results = branches

	// Hide "New branch" when filter exactly matches a branch name
	bp.showNewBranch = true
	if bp.filter != "" {
		lower := strings.ToLower(bp.filter)
		for _, b := range branches {
			if strings.ToLower(b) == lower {
				bp.showNewBranch = false
				break
			}
		}
	}

	// Clamp cursor
	items := bp.visibleItems()
	if bp.cursor >= len(items) {
		if len(items) > 0 {
			bp.cursor = len(items) - 1
		} else {
			bp.cursor = 0
		}
	}
}

// visibleItems returns the list of items to display.
func (bp *BranchPicker) visibleItems() []string {
	var items []string
	if bp.showNewBranch {
		items = append(items, newBranchOption)
	}
	items = append(items, bp.results...)
	return items
}

// SetBaseBranchName records the ref new branches are cut from, for display
// only. It never affects what GetSelectedBranch reports.
func (bp *BranchPicker) SetBaseBranchName(name string) {
	bp.baseBranchName = name
}

// newBranchLabel is the display text for the new-branch row.
func (bp *BranchPicker) newBranchLabel() string {
	if bp.baseBranchName == "" {
		return newBranchOption
	}
	return newBranchOption + " (from " + bp.baseBranchName + ")"
}

// GetSelectedBranch returns the selected branch name, or empty string for "New branch".
func (bp *BranchPicker) GetSelectedBranch() string {
	items := bp.visibleItems()
	if bp.cursor < 0 || bp.cursor >= len(items) {
		return ""
	}
	selected := items[bp.cursor]
	if selected == newBranchOption {
		return ""
	}
	return selected
}

var (
	bpLabelStyle, bpFilterStyle, bpSelectedStyle, bpDimStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildBranchPickerStyles) }

func rebuildBranchPickerStyles() {
	bpLabelStyle = lipgloss.NewStyle().
		Foreground(ui.Accent).
		Bold(true)
	bpFilterStyle = lipgloss.NewStyle().
		Foreground(ui.Text)
	bpSelectedStyle = lipgloss.NewStyle().
		Background(ui.Accent).
		Foreground(ui.SelectionFg)
	bpDimStyle = lipgloss.NewStyle().
		Foreground(ui.Faint)
}

// Render renders the branch picker.
func (bp *BranchPicker) Render() string {
	var s strings.Builder
	s.WriteString(bpLabelStyle.Render("Branch"))
	if bp.focused {
		cursor := bp.filter + "█"
		s.WriteString(bpFilterStyle.Render(" (filter: " + cursor + ")"))
	} else if bp.filter != "" {
		s.WriteString(bpDimStyle.Render(" (filter: " + bp.filter + ")"))
	}
	s.WriteString("\n\n")

	items := bp.visibleItems()
	if len(items) == 0 {
		s.WriteString(bpDimStyle.Render("  No matching branches"))
		return s.String()
	}

	// Show max 5 visible items, windowed around cursor
	maxVisible := 5
	start := 0
	if bp.cursor >= maxVisible {
		start = bp.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(items) {
		end = len(items)
	}

	for i := start; i < end; i++ {
		prefix := "  "
		label := items[i]
		if label == newBranchOption {
			label = bp.newBranchLabel()
		}
		if i == bp.cursor && bp.focused {
			prefix = "> "
			s.WriteString(bpSelectedStyle.Render(prefix + label))
		} else if i == bp.cursor {
			s.WriteString(prefix + label)
		} else {
			s.WriteString(bpDimStyle.Render(prefix + label))
		}
		if i < end-1 {
			s.WriteString("\n")
		}
	}

	return s.String()
}
