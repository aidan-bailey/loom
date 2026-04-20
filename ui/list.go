package ui

import (
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const readyIcon = "● "
const promptingIcon = "● "
const pausedIcon = "⏸ "
const deletingIcon = "✕ "
const workspaceTerminalIcon = "◆ "

var readyStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var promptingStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#e5c07b", Dark: "#e5c07b"})

var addedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var removedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#de613e"))

var pausedStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

var deletingStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#cc6666", Dark: "#cc6666"})

var deletingTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Foreground(TextDim)

var deletingDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Foreground(TextDim)

var workspaceTerminalStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#6c71c4", Dark: "#6c71c4"})

var wtTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Background(lipgloss.AdaptiveColor{Light: "#e8e0f0", Dark: "#2d2640"}).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#c4b5d9"})

var wtDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Background(lipgloss.AdaptiveColor{Light: "#e8e0f0", Dark: "#2d2640"}).
	Foreground(lipgloss.AdaptiveColor{Light: "#6c71c4", Dark: "#8a80b0"})

var wtSelectedTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Background(lipgloss.AdaptiveColor{Light: "#d0c4e8", Dark: "#3d3260"}).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#e0d4f0"})

var wtSelectedDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Background(lipgloss.AdaptiveColor{Light: "#d0c4e8", Dark: "#3d3260"}).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#c4b5d9"})

var titleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var listDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

var selectedTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var selectedDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var mainTitle = lipgloss.NewStyle().
	Background(lipgloss.Color("62")).
	Foreground(lipgloss.Color("230"))

var autoYesStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.Color("#1a1a1a"))

// List is the left-panel instance list. It owns the selection cursor
// and viewport scroll offset, and delegates per-row rendering to
// [InstanceRenderer]. The list does not spawn goroutines or mutate
// Instance state beyond reordering; all status is read via the
// Instance accessors.
type List struct {
	items         []*session.Instance
	selectedIdx   int
	scrollOffset  int // index of the first visible item in the viewport
	height, width int
	renderer      *InstanceRenderer
	autoyes       bool

	// map of repo name to number of instances using it. Used to display the repo name only if there are
	// multiple repos in play.
	repos map[string]int

	// workspaceName is the current workspace name, shown in the title
	workspaceName string
}

// NewList constructs an empty List bound to the given spinner and
// auto-yes indicator. Items are added later via AddInstance; the list
// is ready to render immediately.
func NewList(spinner *spinner.Model, autoYes bool) *List {
	return &List{
		items:    []*session.Instance{},
		renderer: &InstanceRenderer{spinner: spinner},
		repos:    make(map[string]int),
		autoyes:  autoYes,
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
	l.renderer.setWidth(width)
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

// maxVisibleItems returns the maximum number of items that fit in the
// list's current height. The layout is:
//
//	header: 4 lines (2 blank + title + 1 blank)
//	each item: 4 lines (top-pad + title + branch + bottom-pad)
//	separator between items: 1 line
//
// So N items occupy 4 + 4N + (N-1) = 3 + 5N lines.
func (l *List) maxVisibleItems() int {
	n := (l.height - 3) / 5
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

// InstanceRenderer handles rendering of session.Instance objects
type InstanceRenderer struct {
	spinner *spinner.Model
	width   int
}

func (r *InstanceRenderer) setWidth(width int) {
	r.width = AdjustPreviewWidth(width)
}

// ɹ and ɻ are other options.
const branchIcon = "Ꮧ"

// Render produces the single-line representation of instance i at the
// given index, applying the style preset matching the instance's
// status and selection flag. hasMultipleRepos controls whether a repo
// badge is appended to disambiguate cross-workspace lists.
func (r *InstanceRenderer) Render(i *session.Instance, idx int, selected bool, hasMultipleRepos bool) string {
	prefix := fmt.Sprintf(" %d. ", idx)
	if idx >= 10 {
		prefix = prefix[:len(prefix)-1]
	}
	var titleS, descS lipgloss.Style
	switch {
	case i.GetStatus() == session.Deleting:
		titleS = deletingTitleStyle
		descS = deletingDescStyle
	case i.IsWorkspaceTerminal && selected:
		titleS = wtSelectedTitleStyle
		descS = wtSelectedDescStyle
	case i.IsWorkspaceTerminal:
		titleS = wtTitleStyle
		descS = wtDescStyle
	case selected:
		titleS = selectedTitleStyle
		descS = selectedDescStyle
	default:
		titleS = titleStyle
		descS = listDescStyle
	}

	// add spinner next to title if it's running
	status := i.GetStatus()
	var join string
	if i.IsWorkspaceTerminal {
		// Workspace terminal always shows its distinct icon, plus spinner if running
		if status == session.Running || status == session.Loading {
			join = fmt.Sprintf("%s%s ", workspaceTerminalStyle.Render(workspaceTerminalIcon), r.spinner.View())
		} else if status == session.Prompting {
			join = fmt.Sprintf("%s%s", workspaceTerminalStyle.Render(workspaceTerminalIcon), promptingStyle.Render(promptingIcon))
		} else if status == session.Deleting {
			join = deletingStyle.Render(deletingIcon)
		} else {
			join = workspaceTerminalStyle.Render(workspaceTerminalIcon)
		}
	} else {
		switch status {
		case session.Running, session.Loading:
			join = fmt.Sprintf("%s ", r.spinner.View())
		case session.Prompting:
			join = promptingStyle.Render(promptingIcon)
		case session.Ready:
			join = readyStyle.Render(readyIcon)
		case session.Paused:
			join = pausedStyle.Render(pausedIcon)
		case session.Deleting:
			join = deletingStyle.Render(deletingIcon)
		default:
		}
	}

	// Compute the width of the join suffix (status icon / spinner) so the
	// Place block can shrink to keep the total within the padded width.
	// Without this, workspace-terminal items (wider icon) overflow l.width,
	// causing lipgloss.JoinHorizontal to widen every list line past the
	// terminal width and scroll the alt-screen.
	joinWidth := lipgloss.Width(join)
	placeWidth := r.width - 1 - joinWidth // 1 for the " " separator
	if placeWidth < 1 {
		placeWidth = 1
	}

	// Cut the title if it's too long
	titleText := i.Title
	widthAvail := placeWidth - runewidth.StringWidth(prefix) - 1
	if widthAvail > 0 && runewidth.StringWidth(titleText) > widthAvail {
		titleText = runewidth.Truncate(titleText, widthAvail-3, "...")
	}
	title := titleS.Render(lipgloss.JoinHorizontal(
		lipgloss.Left,
		lipgloss.Place(placeWidth, 1, lipgloss.Left, lipgloss.Center, fmt.Sprintf("%s %s", prefix, titleText)),
		" ",
		join,
	))

	stat := i.GetDiffStats()

	var diff string
	var addedDiff, removedDiff string
	if stat == nil || stat.Error != nil || stat.IsEmpty() {
		// Don't show diff stats if there's an error or if they don't exist
		addedDiff = ""
		removedDiff = ""
		diff = ""
	} else {
		addedDiff = fmt.Sprintf("+%d", stat.Added)
		removedDiff = fmt.Sprintf("-%d ", stat.Removed)
		diff = lipgloss.JoinHorizontal(
			lipgloss.Center,
			addedLinesStyle.Background(descS.GetBackground()).Render(addedDiff),
			lipgloss.Style{}.Background(descS.GetBackground()).Foreground(descS.GetForeground()).Render(","),
			removedLinesStyle.Background(descS.GetBackground()).Render(removedDiff),
		)
	}

	remainingWidth := r.width
	remainingWidth -= runewidth.StringWidth(prefix)
	remainingWidth -= runewidth.StringWidth(branchIcon)
	remainingWidth -= 2 // for the literal " " and "-" in the branchLine format string

	diffWidth := runewidth.StringWidth(addedDiff) + runewidth.StringWidth(removedDiff)
	if diffWidth > 0 {
		diffWidth += 1
	}

	// Use fixed width for diff stats to avoid layout issues
	remainingWidth -= diffWidth

	branch := i.GetBranch()
	if i.Started() && hasMultipleRepos {
		repoName, err := i.RepoName()
		if err != nil {
			log.For("ui").Error("list.repo_name_failed", "context", "instance_renderer", "err", err)
		} else {
			branch += fmt.Sprintf(" (%s)", repoName)
		}
	}
	// Don't show branch if there's no space for it. Or show ellipsis if it's too long.
	branchWidth := runewidth.StringWidth(branch)
	if remainingWidth < 0 {
		branch = ""
	} else if remainingWidth < branchWidth {
		if remainingWidth < 3 {
			branch = ""
		} else {
			// We know the remainingWidth is at least 4 and branch is longer than that, so this is safe.
			branch = runewidth.Truncate(branch, remainingWidth-3, "...")
		}
	}
	remainingWidth -= runewidth.StringWidth(branch)

	// Add spaces to fill the remaining width.
	spaces := ""
	if remainingWidth > 0 {
		spaces = strings.Repeat(" ", remainingWidth)
	}

	branchLine := fmt.Sprintf("%s %s-%s%s%s", strings.Repeat(" ", len(prefix)), branchIcon, branch, spaces, diff)

	// join title and subtitle
	text := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		descS.Render(branchLine),
	)

	return text
}

func (l *List) String() string {
	l.ensureSelectedVisible()

	maxVisible := l.maxVisibleItems()
	startIdx := l.scrollOffset
	endIdx := startIdx + maxVisible
	if endIdx > len(l.items) {
		endIdx = len(l.items)
	}

	titleText := " Instances "
	if l.workspaceName != "" {
		titleText = fmt.Sprintf(" %s ", l.workspaceName)
	}

	// Show scroll indicators in title when the list is truncated.
	hasAbove := startIdx > 0
	hasBelow := endIdx < len(l.items)
	if hasAbove || hasBelow {
		arrow := " ↓"
		if hasAbove && hasBelow {
			arrow = " ↕"
		} else if hasAbove {
			arrow = " ↑"
		}
		titleText = fmt.Sprintf("%s%s ", strings.TrimRight(titleText, " "), arrow)
	}

	const autoYesText = " auto-yes "

	// Write the title.
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("\n")

	// Write title line
	// add padding of 2 because the border on list items adds some extra characters
	titleWidth := AdjustPreviewWidth(l.width) + 2
	if !l.autoyes {
		b.WriteString(lipgloss.Place(
			titleWidth, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText)))
	} else {
		title := lipgloss.Place(
			titleWidth/2, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText))
		autoYes := lipgloss.Place(
			titleWidth-(titleWidth/2), 1, lipgloss.Right, lipgloss.Bottom, autoYesStyle.Render(autoYesText))
		b.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top, title, autoYes))
	}

	b.WriteString("\n")
	b.WriteString("\n")

	// Render only the visible window of items. Workspace terminal at index 0
	// gets number 0, regular instances are numbered starting from 1.
	wsOffset := 0
	if len(l.items) > 0 && l.items[0].IsWorkspaceTerminal {
		wsOffset = 1
	}
	for i := startIdx; i < endIdx; i++ {
		item := l.items[i]
		num := i + 1 - wsOffset
		b.WriteString(l.renderer.Render(item, num, i == l.selectedIdx, len(l.repos) > 1))
		if i != endIdx-1 {
			b.WriteString("\n\n")
		}
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, b.String())
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
// Only in-memory bookkeeping happens here: repo-name unregister, slice pop,
// selectedIdx adjustment. No subprocesses are spawned.
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

	// Unregister the reponame.
	repoName, err := targetInstance.RepoName()
	if err != nil {
		log.For("ui").Error("list.repo_name_failed", "context", "kill_instance", "err", err)
	} else {
		l.rmRepo(repoName)
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
	idx := -1
	for i, inst := range l.items {
		if inst.Title == title {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}

	// Unregister the repo name.
	repoName, err := l.items[idx].RepoName()
	if err != nil {
		log.For("ui").Error("list.repo_name_failed", "context", "remove_at_idx", "err", err)
	} else {
		l.rmRepo(repoName)
	}

	l.items = append(l.items[:idx], l.items[idx+1:]...)

	// Adjust selectedIdx if it pointed at or past the removed item.
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

func (l *List) addRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		l.repos[repo] = 0
	}
	l.repos[repo]++
}

func (l *List) rmRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		log.For("ui").Error("list.repo_not_found", "repo", repo)
		return
	}
	l.repos[repo]--
	if l.repos[repo] == 0 {
		delete(l.repos, repo)
	}
}

// AddInstance adds a new instance to the list. It returns a finalizer function that should be called when the instance
// is started. If the instance was restored from storage or is paused, you can call the finalizer immediately.
// When creating a new one and entering the name, you want to call the finalizer once the name is done.
func (l *List) AddInstance(instance *session.Instance) (finalize func()) {
	// Workspace terminals are always pinned at index 0
	if instance.IsWorkspaceTerminal {
		l.items = append([]*session.Instance{instance}, l.items...)
	} else {
		l.items = append(l.items, instance)
	}
	// The finalizer registers the repo name once the instance is started.
	return func() {
		repoName, err := instance.RepoName()
		if err != nil {
			log.For("ui").Error("list.repo_name_failed", "context", "add_finalizer", "err", err)
			return
		}

		l.addRepo(repoName)
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
