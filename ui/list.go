package ui

import (
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/session"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

var workspaceLabelStyle, railAttentionStyle, railDimStyle lipgloss.Style

func init() { RegisterThemeHook(rebuildListStyles) }

func rebuildListStyles() {
	workspaceLabelStyle = lipgloss.NewStyle().Foreground(Workspace)
	railAttentionStyle = lipgloss.NewStyle().Foreground(Attention)
	railDimStyle = lipgloss.NewStyle().Foreground(Dim)
}

// List is the left-panel session rail. It owns the selection cursor
// and viewport scroll offset, and delegates per-item rendering to
// [RenderCard] at [DensityRail]. The list does not spawn goroutines or
// mutate Instance state beyond reordering; all status is read via the
// Instance accessors.
type List struct {
	items         []*session.Instance
	selectedIdx   int
	scrollOffset  int // index of the first visible item in the viewport
	height, width int
	spinner       *spinner.Model
	peers         []PeerSection

	// workspaceName is the current workspace name, shown in the title
	workspaceName string
}

// NewList constructs an empty List bound to the given spinner. Items
// are added later via AddInstance; the list is ready to render
// immediately.
func NewList(spinner *spinner.Model) *List {
	return &List{
		items:   []*session.Instance{},
		spinner: spinner,
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
}

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	for i, item := range l.items {
		if !item.Started() || item.Paused() || !item.TmuxAlive() {
			continue
		}

		if innerErr := item.SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}

// SetWorkspaceName sets the workspace name displayed in the title.
func (l *List) SetWorkspaceName(name string) {
	l.workspaceName = name
}

// SetPeerSections sets the peer-workspace summaries rendered under the rail.
func (l *List) SetPeerSections(peers []PeerSection) { l.peers = peers }

// PeerSections returns the peer-workspace summaries currently set.
func (l *List) PeerSections() []PeerSection { return l.peers }

// SelectedIdx returns the current selection index (for jump helpers).
func (l *List) SelectedIdx() int { return l.selectedIdx }

// peerLines is the vertical budget the peer footer consumes.
func (l *List) peerLines() int {
	if len(l.peers) == 0 {
		return 0
	}
	return len(l.peers) + 1 // blank separator + one line per peer
}

// maxVisibleItems returns how many rail cards fit: header (2 lines) +
// RailCardLines per item, minus the peer footer.
func (l *List) maxVisibleItems() int {
	n := (l.height - RailHeaderLines - l.peerLines()) / RailCardLines
	if n < 1 {
		n = 1
	}
	return n
}

// ensureSelectedVisible adjusts scrollOffset so that selectedIdx is within
// the visible window.
func (l *List) ensureSelectedVisible() {
	if len(l.items) == 0 {
		l.scrollOffset = 0
		return
	}

	maxVisible := l.maxVisibleItems()

	// Clamp scrollOffset to valid range.
	maxOffset := len(l.items) - maxVisible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if l.scrollOffset > maxOffset {
		l.scrollOffset = maxOffset
	}

	// Scroll to keep selectedIdx visible.
	if l.selectedIdx < l.scrollOffset {
		l.scrollOffset = l.selectedIdx
	}
	if l.selectedIdx >= l.scrollOffset+maxVisible {
		l.scrollOffset = l.selectedIdx - maxVisible + 1
	}
}

// NumInstances returns the number of instances currently held by the
// list. Used by GlobalInstanceLimit checks in the app layer before
// admitting a new instance.
func (l *List) NumInstances() int {
	return len(l.items)
}

// DisplayIndex returns the 1-based number shown in the list UI for the
// item at position i in items. A leading workspace terminal (position
// 0) is numbered 0, not 1, so every other item's displayed number is
// offset by one relative to its slice position. Exported so callers
// that build a session-index picker from the same items (e.g. the
// merge picker) label rows with the exact number the user already saw
// in the main list.
func DisplayIndex(items []*session.Instance, i int) int {
	wsOffset := 0
	if len(items) > 0 && items[0].IsWorkspaceTerminal {
		wsOffset = 1
	}
	return i + 1 - wsOffset
}

func (l *List) String() string {
	l.ensureSelectedVisible()

	maxVisible := l.maxVisibleItems()
	startIdx := l.scrollOffset
	endIdx := startIdx + maxVisible
	if endIdx > len(l.items) {
		endIdx = len(l.items)
	}

	titleText := "Instances"
	if l.workspaceName != "" {
		titleText = l.workspaceName
	}

	// Show scroll indicators in the header when the rail is truncated.
	hasAbove := startIdx > 0
	hasBelow := endIdx < len(l.items)
	arrow := ""
	switch {
	case hasAbove && hasBelow:
		arrow = " ↕"
	case hasAbove:
		arrow = " ↑"
	case hasBelow:
		arrow = " ↓"
	}

	// Section header (RailHeaderLines): workspace label + blank line.
	parts := []string{
		workspaceLabelStyle.Render(truncate(" "+strings.ToUpper(titleText)+arrow, l.width)),
		"",
	}

	// Visible window of rail cards, one blank gap line between cards.
	// See DisplayIndex for the workspace-terminal numbering rule.
	spinnerFrame := l.spinner.View()
	for i := startIdx; i < endIdx; i++ {
		d := BuildCardData(l.items[i], i == l.selectedIdx, spinnerFrame, 1)
		d.Index = DisplayIndex(l.items, i)
		parts = append(parts, RenderCard(d, DensityRail, l.width))
		if i != endIdx-1 {
			parts = append(parts, "")
		}
	}

	// Peer-workspace footer: blank separator + one summary line per peer.
	if len(l.peers) > 0 {
		parts = append(parts, "")
		for _, p := range l.peers {
			parts = append(parts, l.renderPeerLine(p))
		}
	}

	// Place pads short content to the full box but GROWS on over-tall
	// content, which would scroll the alt-screen — clamp hard to the
	// allocated height (degenerate sizes: clamped maxVisibleItems plus
	// a large peer footer can exceed a tiny height).
	return clampHeight(lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top,
		strings.Join(parts, "\n")), l.height)
}

// renderPeerLine renders one peer-workspace summary line: uppercased
// name plus compact status counts ("❯2 ✻1 ·3"), omitting zero
// segments. The attention count is the only loud (Attention-colored)
// element; the rest stays Dim.
func (l *List) renderPeerLine(p PeerSection) string {
	var att, rest string
	if p.Attention > 0 {
		att = fmt.Sprintf(" ❯%d", p.Attention)
	}
	if p.Running > 0 {
		rest += fmt.Sprintf(" ✻%d", p.Running)
	}
	if p.Idle > 0 {
		rest += fmt.Sprintf(" ·%d", p.Idle)
	}
	nameBudget := l.width - 1 - runewidth.StringWidth(att) - runewidth.StringWidth(rest)
	name := truncate(strings.ToUpper(p.Name), nameBudget)
	return workspaceLabelStyle.Render(" "+name) +
		railAttentionStyle.Render(att) +
		railDimStyle.Render(rest)
}

// Down selects the next non-Deleting item in the list. If every item
// below the cursor is Deleting (or the cursor is already on the last
// selectable item), selectedIdx stays put.
func (l *List) Down() {
	if len(l.items) == 0 {
		return
	}
	for i := l.selectedIdx + 1; i < len(l.items); i++ {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// PopSelectedForKill removes the currently selected instance from the list
// and returns it so the caller can run the blocking Kill() (tmux + worktree
// cleanup) off the Bubble Tea update goroutine. Returns nil when the list is
// empty or the selected item is a workspace terminal (which cannot be killed).
//
// Only in-memory bookkeeping happens here: slice pop and selectedIdx
// adjustment. No subprocesses are spawned.
func (l *List) PopSelectedForKill() *session.Instance {
	if len(l.items) == 0 {
		return nil
	}
	targetInstance := l.items[l.selectedIdx]
	if targetInstance.IsWorkspaceTerminal {
		return nil
	}

	// If you delete the last one in the list, select the previous one.
	if l.selectedIdx == len(l.items)-1 {
		defer l.Up()
	}

	// Since there's items after this, the selectedIdx can stay the same.
	l.items = append(l.items[:l.selectedIdx], l.items[l.selectedIdx+1:]...)
	return targetInstance
}

// RemoveInstanceByTitle removes an instance from the list by title.
// Unlike Kill(), this does not perform I/O (no tmux/worktree cleanup) —
// the caller is responsible for that. This is safe to call from the main
// event loop after a Cmd goroutine has already performed I/O cleanup.
func (l *List) RemoveInstanceByTitle(title string) {
	idx := l.findByTitle(title)
	if idx < 0 {
		return
	}
	l.removeAt(idx)
}

// GetInstanceByTitle returns the instance with the given title, or nil.
func (l *List) GetInstanceByTitle(title string) *session.Instance {
	if idx := l.findByTitle(title); idx >= 0 {
		return l.items[idx]
	}
	return nil
}

func (l *List) findByTitle(title string) int {
	for i, inst := range l.items {
		if inst.Title == title {
			return i
		}
	}
	return -1
}

func (l *List) removeAt(idx int) {
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	if l.selectedIdx >= len(l.items) && l.selectedIdx > 0 {
		l.selectedIdx--
	}
	l.ensureSelectedVisible()
}

// Up selects the prev non-Deleting item in the list. If every item
// above the cursor is Deleting, selectedIdx stays put.
func (l *List) Up() {
	if len(l.items) == 0 {
		return
	}
	for i := l.selectedIdx - 1; i >= 0; i-- {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// PageUp jumps the selection up by one visible page, skipping Deleting items.
// If every candidate in the target window is Deleting, the cursor stays put.
func (l *List) PageUp() {
	if len(l.items) == 0 {
		return
	}
	step := l.maxVisibleItems()
	target := l.selectedIdx - step
	if target < 0 {
		target = 0
	}
	// Prefer the target, then walk upward to find a non-Deleting item.
	for i := target; i >= 0; i-- {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// PageDown jumps the selection down by one visible page, skipping Deleting items.
func (l *List) PageDown() {
	if len(l.items) == 0 {
		return
	}
	step := l.maxVisibleItems()
	target := l.selectedIdx + step
	if target > len(l.items)-1 {
		target = len(l.items) - 1
	}
	for i := target; i < len(l.items); i++ {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// Top selects the first non-Deleting item.
func (l *List) Top() {
	if len(l.items) == 0 {
		return
	}
	for i := 0; i < len(l.items); i++ {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// Bottom selects the last non-Deleting item.
func (l *List) Bottom() {
	if len(l.items) == 0 {
		return
	}
	for i := len(l.items) - 1; i >= 0; i-- {
		if l.items[i].GetStatus() != session.Deleting {
			l.selectedIdx = i
			break
		}
	}
	l.ensureSelectedVisible()
}

// AddInstance adds a new instance to the list.
func (l *List) AddInstance(instance *session.Instance) {
	// Workspace terminals are always pinned at index 0
	if instance.IsWorkspaceTerminal {
		l.items = append([]*session.Instance{instance}, l.items...)
	} else {
		l.items = append(l.items, instance)
	}
}

// GetSelectedInstance returns the currently selected instance
func (l *List) GetSelectedInstance() *session.Instance {
	if len(l.items) == 0 || l.selectedIdx >= len(l.items) {
		return nil
	}
	return l.items[l.selectedIdx]
}

// SetSelectedInstance sets the selected index. Noop if the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx >= len(l.items) {
		return
	}
	l.selectedIdx = idx
	l.ensureSelectedVisible()
}

// SelectInstance finds and selects the given instance in the list.
func (l *List) SelectInstance(target *session.Instance) {
	for i, inst := range l.items {
		if inst == target {
			l.selectedIdx = i
			l.ensureSelectedVisible()
			return
		}
	}
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items
}
