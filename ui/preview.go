package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session"

	"charm.land/lipgloss/v2"
)

// agentScrollTTL bounds how often the alt-screen state is probed (a tmux
// subprocess) on the scroll hot path. agentPageNotches is how many wheel notches
// a PageUp/Down forwards to a TUI agent.
const (
	agentScrollTTL   = 750 * time.Millisecond
	agentPageNotches = 3
	// wheelEventsPerNotch dampens forwarded wheel speed: one notch is forwarded
	// to the agent per this many same-direction wheel events (1 = native 1:1).
	// Most terminals emit several wheel events per physical notch, so 1:1 feels
	// too fast.
	wheelEventsPerNotch = 2
)

// scrollToTopOffset is a sentinel passed to setOffset for "go to top"; the next
// UpdateContent clamps it to the real top of the captured buffer.
const scrollToTopOffset = 1 << 30

var (
	previewPaneStyle, previewScrollFooterStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildPreviewStyles) }

func rebuildPreviewStyles() {
	previewPaneStyle = lipgloss.NewStyle().Foreground(Text)
	previewScrollFooterStyle = lipgloss.NewStyle().Foreground(Highlight)
}

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
// split view. It tails the emulator's live screen at the live tail, and when
// scrolled windows the emulator's scrollback in-process (ScrollModel); on the
// no-emulator path (snapshot mode / Windows) it paints a window into tmux's
// authoritative history (capture-pane -S -) via snapFallback. Either way live
// output keeps flowing. lastInstanceTitle resets the scroll position on
// selection change rather than persisting a stale offset.
type PreviewPane struct {
	width  int
	height int

	previewState      previewState
	lastInstanceTitle string // tracks the current instance to reset scroll on change

	// scroll owns offset/anchoring/wheel/alt-probe state on the emulator path.
	scroll ScrollModel
	// snapFallback carries the legacy capture-pane windowing state, used
	// only when the instance has no emulator (snapshot / Windows).
	snapFallback snapshotScroll
	// newLinesBelowRender is what String()'s scrolled footer shows; written
	// by UpdateContent on whichever path produced the window.
	newLinesBelowRender int
	// src caches the scroll source resolved in UpdateContent so the arg-less
	// ScrollPercent can query the emulator path. Update-goroutine only; nil on
	// the snapshot / no-session path.
	src scrollSource

	// sel is the current mouse selection over the displayed content.
	// displayedPlain holds the plain (ANSI-stripped) lines most recently rendered
	// by String(), so selection extraction matches exactly what's on screen.
	sel            selection
	displayedPlain []string
}

type previewState struct {
	// fallback is true if the preview pane is displaying fallback text
	fallback bool
	// text is the text displayed in the preview pane
	text string
}

// snapshotScroll is the legacy capture-pane windowing state, kept only for
// the no-emulator path. See ScrollModel for the emulator path.
type snapshotScroll struct {
	offset             int
	starting           bool
	totalAtScrollStart int
	lastTotal          int
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
	// A width change re-wraps the emulator/capture buffer, invalidating any
	// line-based scroll anchor, so a resize while scrolled returns to live tail.
	if width != p.width {
		p.scroll.Reset()
		p.snapFallback = snapshotScroll{}
		p.newLinesBelowRender = 0
	}
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
	p.newLinesBelowRender = 0
	return nil
}

// UpdateContent refreshes the pane from the given instance. At the live tail it
// tails the live emulator screen; when scrolled it windows the emulator's
// scrollback in-process (ScrollModel), or, on the no-emulator path, tmux's
// authoritative history (capture-pane -S -) at the current offset, anchoring the
// view to its content as live output accrues below. Falls back to splash text
// for nil/loading/paused instances and resets the offset on instance change.
func (p *PreviewPane) UpdateContent(instance *session.Instance) error {
	// Reset to live tail when the selected instance changes.
	newTitle := ""
	if instance != nil {
		newTitle = instance.Title
	}
	if newTitle != p.lastInstanceTitle {
		p.lastInstanceTitle = newTitle
		p.scroll.Reset()
		p.snapFallback = snapshotScroll{}
		p.newLinesBelowRender = 0
		p.src = nil
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
				Foreground(Highlight).
				Render(fmt.Sprintf(
					"The instance can be checked out at '%s' (copied to your clipboard)",
					instance.GetBranch(),
				)),
		))
		return nil
	case instance.GetStatus() == session.Recoverable:
		p.setFallbackState(lipgloss.JoinVertical(lipgloss.Center,
			"Recoverable session (found on disk).",
			"Its worktree may hold a live agent or uncommitted work.",
			"",
			lipgloss.NewStyle().
				Foreground(Highlight).
				Render(fmt.Sprintf("Branch: %s", instance.GetBranch())),
			"",
			"Press 'r' to recover it, or 'D' to discard the worktree (branch is kept).",
		))
		return nil
	}

	if !p.scroll.IsScrolling() && p.snapFallback.offset == 0 {
		return p.liveTail(instance)
	}

	rows := p.height - 1
	if rows < 1 {
		rows = 1
	}
	// Emulator path: window in-process from the emulator's scrollback.
	if src, srcOK := scrollSourceFor(instance); srcOK {
		w, live, ok := p.scroll.AdvanceAndRender(src, rows)
		if ok {
			p.src = src
			if live {
				return p.liveTail(instance)
			}
			p.previewState = previewState{fallback: false, text: w}
			p.newLinesBelowRender = p.scroll.NewLinesBelow()
			return nil
		}
	}
	// No emulator: fall back to the legacy capture-pane windowing.
	return p.updateContentSnapshotScrolled(instance, rows)
}

// updateContentSnapshotScrolled windows tmux's authoritative capture-pane
// buffer at p.snapFallback.offset. Used only on the no-emulator path
// (snapshot mode / Windows); the emulator path is handled inline in
// UpdateContent via ScrollModel. It anchors the view to its content as live
// output accrues below, and — unlike the emulator path — snaps back to the
// live tail when the captured buffer SHRINKS (clear-history / alt-screen
// flip / re-wrap), where the offset anchor is meaningless.
func (p *PreviewPane) updateContentSnapshotScrolled(instance *session.Instance, rows int) error {
	// Scrolled: window into tmux's authoritative buffer (scrollback + visible).
	// The in-process emulator only mirrors the visible screen, so windowed
	// history must come from tmux, not emu.Scrollback().
	hist, ok := instance.CaptureHistory()
	if !ok {
		p.snapFallback.offset = 0
		return p.liveTail(instance)
	}
	lines := strings.Split(strings.TrimRight(hist, "\n"), "\n")
	total := len(lines)

	switch {
	case p.snapFallback.starting:
		// First tick of this scroll gesture: baseline the new-lines counter.
		p.snapFallback.totalAtScrollStart = total
		p.snapFallback.lastTotal = total
		p.snapFallback.starting = false
	case p.snapFallback.lastTotal > 0 && total < p.snapFallback.lastTotal:
		// Buffer shrank (clear-history / alt-screen flip / re-wrap): the anchor
		// is meaningless — snap back to live instead of drifting.
		p.snapFallback = snapshotScroll{}
		p.newLinesBelowRender = 0
		return p.liveTail(instance)
	case p.snapFallback.lastTotal > 0 && total > p.snapFallback.lastTotal:
		// New output appended below while scrolled: bump the offset by the same
		// amount so the content under the cursor stays put.
		p.snapFallback.offset += total - p.snapFallback.lastTotal
	}
	p.snapFallback.lastTotal = total

	maxOff := total - rows
	if maxOff < 0 {
		maxOff = 0
	}
	if p.snapFallback.offset > maxOff {
		p.snapFallback.offset = maxOff
	}
	if p.snapFallback.offset <= 0 {
		// Anchored back to the bottom -> live tail.
		p.snapFallback.offset = 0
		return p.liveTail(instance)
	}

	window := windowLines(lines, p.snapFallback.offset, rows)
	p.previewState = previewState{fallback: false, text: strings.Join(window, "\n")}
	if newBelow := total - p.snapFallback.totalAtScrollStart; newBelow > 0 {
		p.newLinesBelowRender = newBelow
	} else {
		p.newLinesBelowRender = 0
	}
	return nil
}

// ShowingFallback reports whether the pane is displaying splash/fallback
// text instead of live terminal content (no cursor applies there).
func (p *PreviewPane) ShowingFallback() bool {
	return p.previewState.fallback
}

// Returns the preview pane content as a string.
func (p *PreviewPane) String() string {
	if p.width == 0 || p.height == 0 {
		return strings.Repeat("\n", max(p.height, 0)) // height may be negative on a tiny terminal
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
	if p.scroll.IsScrolling() || p.snapFallback.offset > 0 {
		wlines := strings.Split(p.previewState.text, "\n")
		display, plain := renderWithSelection(wlines, p.sel)
		p.displayedPlain = plain
		footer := previewScrollFooterStyle.Render(scrollFooter(p.newLinesBelowRender))
		body := lipgloss.JoinVertical(lipgloss.Left, strings.Join(display, "\n"), footer)
		return previewPaneStyle.Width(p.width).Render(body)
	}

	// Live-tail display. The emulator (and the snapshot capture) is sized to the
	// pane, so show every row — no ellipsis reservation. Reserving a row here cut
	// the agent's bottom row (e.g. Claude's input box, replaced by "...") and was
	// inconsistent with the terminal pane, which fills its full height.
	availableHeight := p.height

	lines := strings.Split(p.previewState.text, "\n")

	if availableHeight > 0 {
		if len(lines) > availableHeight {
			lines = lines[:availableHeight]
		} else {
			// Pad with empty lines to fill available height
			padding := availableHeight - len(lines)
			lines = append(lines, make([]string, padding)...)
		}
	}

	display, plain := renderWithSelection(lines, p.sel)
	p.displayedPlain = plain
	content := strings.Join(display, "\n")

	rendered := previewPaneStyle.Width(p.width).Render(content)
	return rendered
}

// BeginSelection starts a selection anchored at content (row, col).
func (p *PreviewPane) BeginSelection(row, col int) {
	p.sel = selection{active: true, anchorRow: row, anchorCol: col, curRow: row, curCol: col}
}

// ExtendSelection moves the active selection's cursor to content (row, col).
func (p *PreviewPane) ExtendSelection(row, col int) {
	if !p.sel.active {
		return
	}
	p.sel.curRow = row
	p.sel.curCol = col
}

// ClearSelection clears any active selection.
func (p *PreviewPane) ClearSelection() { p.sel = selection{} }

// SelectedText returns the currently selected text (plain), or "" if none.
func (p *PreviewPane) SelectedText() string { return extractSelection(p.displayedPlain, p.sel) }

// emulatorScroll reports the scroll source when the instance is emulator-backed
// (live session with an emulator). ok=false routes the caller to the snapshot
// (capture-pane) fallback used in snapshot mode / on Windows. This is the same
// "does this pane have an emulator" decision UpdateContent makes via
// scrollSourceFor + AdvanceAndRender's internal ok — duplicated here (rather
// than shared) because the scroll methods must pick a branch (emulator vs.
// snapshot probe/state) before acting, whereas UpdateContent can try the
// emulator path and let AdvanceAndRender's ok fall through inline.
func (p *PreviewPane) emulatorScroll(instance *session.Instance) (scrollSource, bool) {
	src, ok := scrollSourceFor(instance)
	if !ok {
		return nil, false
	}
	if _, emuOK := src.ScrollbackLen(); !emuOK {
		return nil, false
	}
	return src, true
}

// ScrollUp scrolls one line up into history (or forwards a damped wheel-up to a TUI agent).
func (p *PreviewPane) ScrollUp(instance *session.Instance) error {
	if src, ok := p.emulatorScroll(instance); ok {
		return p.scroll.ScrollUp(src)
	}
	// Snapshot path: probe tmux directly (rare path, no TTL cache).
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(true, 1)
	}
	p.snapshotScrollBy(instance, +1)
	return nil
}

// ScrollDown scrolls one line down toward the live tail (or forwards a damped wheel-down).
func (p *PreviewPane) ScrollDown(instance *session.Instance) error {
	if src, ok := p.emulatorScroll(instance); ok {
		return p.scroll.ScrollDown(src)
	}
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(false, 1)
	}
	p.snapshotScrollBy(instance, -1)
	return nil
}

// PageUp scrolls up by half a pane height (or forwards a burst of wheel-ups).
func (p *PreviewPane) PageUp(instance *session.Instance) error {
	if src, ok := p.emulatorScroll(instance); ok {
		return p.scroll.PageUp(src, p.height)
	}
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(true, agentPageNotches)
	}
	p.snapshotScrollBy(instance, +(p.height / 2))
	return nil
}

// PageDown scrolls down by half a pane height (or forwards a burst of wheel-downs).
func (p *PreviewPane) PageDown(instance *session.Instance) error {
	if src, ok := p.emulatorScroll(instance); ok {
		return p.scroll.PageDown(src, p.height)
	}
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(false, agentPageNotches)
	}
	p.snapshotScrollBy(instance, -(p.height / 2))
	return nil
}

// GotoTop jumps to the oldest line of captured history (TUI: a large wheel-up burst).
func (p *PreviewPane) GotoTop(instance *session.Instance) error {
	if src, ok := p.emulatorScroll(instance); ok {
		p.scroll.GotoTop(src)
		return nil
	}
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(true, 30)
	}
	p.snapshotScrollBy(instance, scrollToTopOffset)
	return nil
}

// GotoBottom returns to the live tail (TUI: a large wheel-down burst).
func (p *PreviewPane) GotoBottom(instance *session.Instance) error {
	if _, ok := p.emulatorScroll(instance); ok {
		p.scroll.Reset()
		return nil
	}
	if instance != nil && instance.IsAlternateScreen() {
		return instance.ForwardWheel(false, 30)
	}
	p.snapFallback = snapshotScroll{}
	p.newLinesBelowRender = 0
	return nil
}

// ScrollPercent returns the scroll position as a fraction [0, 1]; 1.0 == live
// tail (bottom).
func (p *PreviewPane) ScrollPercent() float64 {
	if p.scroll.IsScrolling() && p.src != nil {
		return p.scroll.ScrollPercent(p.src)
	}
	if p.snapFallback.offset <= 0 || p.snapFallback.lastTotal <= 0 {
		return 1.0
	}
	return 1.0 - float64(p.snapFallback.offset)/float64(p.snapFallback.lastTotal)
}

// IsScrolling reports whether the pane is scrolled away from the live tail.
func (p *PreviewPane) IsScrolling() bool {
	return p.scroll.IsScrolling() || p.snapFallback.offset > 0
}

// ResetToNormalMode returns the pane to the live tail on both paths.
func (p *PreviewPane) ResetToNormalMode(instance *session.Instance) error {
	p.scroll.Reset()
	p.snapFallback = snapshotScroll{}
	p.newLinesBelowRender = 0
	return nil
}

// snapshotScrollBy applies a lines-from-bottom delta to the legacy
// capture-pane offset (no-emulator path). It floors at 0, marks the start of a
// gesture when leaving the tail, and clears the new-lines counters at the tail
// — the old setOffset semantics on p.snapFallback. The real top-of-buffer
// clamp happens in updateContentSnapshotScrolled, which has the captured line
// count.
func (p *PreviewPane) snapshotScrollBy(instance *session.Instance, delta int) {
	if instance != nil && (instance.GetStatus() == session.Paused || instance.GetStatus() == session.Recoverable) {
		return
	}
	off := p.snapFallback.offset + delta
	if off < 0 {
		off = 0
	}
	wasBottom := p.snapFallback.offset == 0
	p.snapFallback.offset = off
	if wasBottom && off > 0 {
		p.snapFallback.starting = true
	}
	if off == 0 {
		p.newLinesBelowRender = 0
		p.snapFallback.lastTotal = 0
	}
}
