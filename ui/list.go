package ui

import (
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/session"
	"slices"
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

// InstanceSource supplies the rows a List shows, in display order. In
// production it is the list's workspace (core.Workspace), which owns the
// instances; the List only reads it, so adding, removing and replacing
// rows are edits of the source.
type InstanceSource interface {
	Instances() []*session.Instance
}

// List is the left-panel session rail. It owns the selection cursor
// and viewport scroll offset, and delegates per-item rendering to
// [RenderCard] at [DensityRail]. The list does not spawn goroutines or
// mutate Instance state; its rows come from its InstanceSource, and all
// status is read via the Instance accessors.
type List struct {
	// src supplies the rows; nil shows none.
	src InstanceSource
	// selected is the selected row's instance and selectedIdx its row when
	// last resolved. The rows change under the list (the source is edited
	// elsewhere), so the selection is kept by identity and re-resolved on
	// every read (resolveSelection).
	selected    *session.Instance
	selectedIdx int
	// seen is the list's own copy of the rows as of the last resolve: a
	// selection whose instance has since gone is placed against it
	// (lostSelectionRow), which takes every edit made in between into
	// account, not just the last one.
	seen          []*session.Instance
	scrollOffset  int // index of the first visible item in the viewport
	height, width int
	spinner       *spinner.Model
	peers         []PeerSection
	// panes is the attach-client registry the cards' tails and preview
	// sizing go through (SetPanes).
	panes *PaneClients

	// workspaceName is the current workspace name, shown in the title
	workspaceName string
}

// NewList constructs a List showing src's rows, bound to the given
// spinner.
func NewList(spinner *spinner.Model, src InstanceSource) *List {
	return &List{src: src, spinner: spinner}
}

// items returns the source's rows.
func (l *List) items() []*session.Instance {
	if l.src == nil {
		return nil
	}
	return l.src.Instances()
}

// resolveSelection returns the selected row's index in the current rows,
// finding the selected instance by identity: a row added or removed above
// it moves its index, not the selection (inline attach looks the
// selection up per key, so a silent shift would redirect typing). When
// the selected instance is gone, the selection moves to the row that slid
// into its place, or to the new last row (lostSelectionRow); with no rows
// it is 0. These are the rules the list's own removal and
// workspace-terminal prepend applied when it held the rows, and like them
// a selection that moves is scrolled back into view.
func (l *List) resolveSelection() int {
	items := l.items()
	defer l.remember(items)
	if len(items) == 0 {
		l.selected, l.selectedIdx = nil, 0
		return 0
	}
	if l.selectedIdx < len(items) && items[l.selectedIdx] == l.selected {
		return l.selectedIdx
	}
	i := min(l.selectedIdx, len(items)-1)
	if l.selected != nil {
		if i = slices.Index(items, l.selected); i < 0 {
			i = l.lostSelectionRow(items)
		}
	}
	l.selected, l.selectedIdx = items[i], i
	l.scrollToSelected(len(items))
	return i
}

// lostSelectionRow is the row a selection whose instance is gone moves to,
// placed against the rows of the last resolve (seen): the first row after
// the selected one there that is still present (the row that slid into its
// place, however many removals landed in between), else the new last row.
// A lone in-place replacement of the selected row (a recover swapping its
// placeholder) keeps the selection on that row.
func (l *List) lostSelectionRow(items []*session.Instance) int {
	p := slices.Index(l.seen, l.selected)
	if p < 0 {
		return len(items) - 1
	}
	if replacedOnlyAt(l.seen, items, p) {
		return p
	}
	for _, inst := range l.seen[p+1:] {
		if i := slices.Index(items, inst); i >= 0 {
			return i
		}
	}
	return len(items) - 1
}

// replacedOnlyAt reports whether now is before with only row p changed.
func replacedOnlyAt(before, now []*session.Instance, p int) bool {
	if len(before) != len(now) {
		return false
	}
	for i := range now {
		if i != p && now[i] != before[i] {
			return false
		}
	}
	return true
}

// remember records items as the rows of the last resolve (seen), in the
// list's own copy: the source edits its slice in place.
func (l *List) remember(items []*session.Instance) {
	if !slices.Equal(l.seen, items) {
		l.seen = append(l.seen[:0], items...)
	}
}

// selectRow selects row i, which must be in range.
func (l *List) selectRow(i int) {
	l.selected, l.selectedIdx = l.items()[i], i
	l.ensureSelectedVisible()
}

// SetPanes sets the registry the list reads its instances' attach clients
// from.
func (l *List) SetPanes(panes *PaneClients) { l.panes = panes }

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
}

// Size returns the width and height SetSize last set.
func (l *List) Size() (width, height int) { return l.width, l.height }

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	// New clients attach at this size too (PaneClients.Ensure).
	l.panes.SetDefaultSize(width, height)
	for i, item := range l.items() {
		if !item.Started() || item.Paused() || !item.Pane().TmuxAlive() {
			continue
		}

		if innerErr := l.panes.For(item).SetPreviewSize(width, height); innerErr != nil {
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

// WorkspaceName returns the workspace name displayed in the title.
func (l *List) WorkspaceName() string { return l.workspaceName }

// SetPeerSections sets the peer-workspace summaries rendered under the rail.
func (l *List) SetPeerSections(peers []PeerSection) { l.peers = peers }

// PeerSections returns the peer-workspace summaries currently set.
func (l *List) PeerSections() []PeerSection { return l.peers }

// SelectedIdx returns the current selection index (for jump helpers).
func (l *List) SelectedIdx() int { return l.resolveSelection() }

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
	l.resolveSelection()
	l.scrollToSelected(len(l.items()))
}

// scrollToSelected is ensureSelectedVisible for an already-resolved
// selection over n rows (resolveSelection calls it when the selection
// moves).
func (l *List) scrollToSelected(n int) {
	if n == 0 {
		l.scrollOffset = 0
		return
	}

	maxVisible := l.maxVisibleItems()

	// Clamp scrollOffset to valid range.
	maxOffset := n - maxVisible
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
	return len(l.items())
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
	items := l.items()
	sel := l.selectedIdx

	maxVisible := l.maxVisibleItems()
	startIdx := l.scrollOffset
	endIdx := startIdx + maxVisible
	if endIdx > len(items) {
		endIdx = len(items)
	}

	titleText := "Instances"
	if l.workspaceName != "" {
		titleText = l.workspaceName
	}

	// Show scroll indicators in the header when the rail is truncated.
	hasAbove := startIdx > 0
	hasBelow := endIdx < len(items)
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
		d := BuildCardData(items[i], l.panes.For(items[i]), i == sel, spinnerFrame, 1)
		d.Index = DisplayIndex(items, i)
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
	items := l.items()
	if len(items) == 0 {
		return
	}
	for i := l.resolveSelection() + 1; i < len(items); i++ {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// GetInstanceByTitle returns the instance with the given title, or nil.
func (l *List) GetInstanceByTitle(title string) *session.Instance {
	if idx := l.findByTitle(title); idx >= 0 {
		return l.items()[idx]
	}
	return nil
}

func (l *List) findByTitle(title string) int {
	for i, inst := range l.items() {
		if inst.Title == title {
			return i
		}
	}
	return -1
}

// Up selects the prev non-Deleting item in the list. If every item
// above the cursor is Deleting, selectedIdx stays put.
func (l *List) Up() {
	items := l.items()
	if len(items) == 0 {
		return
	}
	for i := l.resolveSelection() - 1; i >= 0; i-- {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// PageUp jumps the selection up by one visible page, skipping Deleting items.
// If every candidate in the target window is Deleting, the cursor stays put.
func (l *List) PageUp() {
	items := l.items()
	if len(items) == 0 {
		return
	}
	step := l.maxVisibleItems()
	target := l.resolveSelection() - step
	if target < 0 {
		target = 0
	}
	// Prefer the target, then walk upward to find a non-Deleting item.
	for i := target; i >= 0; i-- {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// PageDown jumps the selection down by one visible page, skipping Deleting items.
func (l *List) PageDown() {
	items := l.items()
	if len(items) == 0 {
		return
	}
	step := l.maxVisibleItems()
	target := l.resolveSelection() + step
	if target > len(items)-1 {
		target = len(items) - 1
	}
	for i := target; i < len(items); i++ {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// Top selects the first non-Deleting item.
func (l *List) Top() {
	items := l.items()
	if len(items) == 0 {
		return
	}
	for i := 0; i < len(items); i++ {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// Bottom selects the last non-Deleting item.
func (l *List) Bottom() {
	items := l.items()
	if len(items) == 0 {
		return
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].GetStatus() != session.Deleting {
			l.selectRow(i)
			break
		}
	}
	l.ensureSelectedVisible()
}

// GetSelectedInstance returns the currently selected instance
func (l *List) GetSelectedInstance() *session.Instance {
	items := l.items()
	if len(items) == 0 {
		return nil
	}
	return items[l.resolveSelection()]
}

// SetSelectedInstance sets the selected index. Noop if the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx < 0 || idx >= len(l.items()) {
		return
	}
	l.selectRow(idx)
}

// SelectInstance finds and selects the given instance in the list.
func (l *List) SelectInstance(target *session.Instance) {
	for i, inst := range l.items() {
		if inst == target {
			l.selectRow(i)
			return
		}
	}
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items()
}
