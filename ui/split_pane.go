package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"strings"

	"charm.land/lipgloss/v2"
)

var highlightColor = BorderActive

// AdjustPreviewWidth adjusts the width of the preview pane per PreviewWidthPercent.
func AdjustPreviewWidth(width int) int {
	return int(float64(width) * PreviewWidthPercent)
}

// FocusAgent and FocusTerminal are the SplitPane focus values: the top
// (agent) or bottom (terminal) pane. Focus determines which pane
// receives scroll and attach keypresses.
const (
	FocusAgent int = iota
	FocusTerminal
)

var dimBorderColor = BorderMuted

var (
	// paneBodyBorder renders left, right, bottom — top line is built manually with an inline title.
	paneBodyBorder = lipgloss.NewStyle().
			BorderForeground(dimBorderColor).
			Border(lipgloss.RoundedBorder(), false, true, true, true)
	focusedPaneBodyBorder = lipgloss.NewStyle().
				BorderForeground(highlightColor).
				Border(lipgloss.RoundedBorder(), false, true, true, true)
	paneTitleStyle = lipgloss.NewStyle().
			Foreground(dimBorderColor)
	focusedPaneTitleStyle = lipgloss.NewStyle().
				Foreground(highlightColor).
				Bold(true)
	diffOverlayTitleStyle = lipgloss.NewStyle().
				Foreground(highlightColor).
				Bold(true)
)

// SplitPane composes the right-hand side of the TUI: an agent preview
// on top (70%), a terminal pane below (30%), and a hotkey-toggled diff
// overlay that replaces both. SplitPane holds the currently-focused
// pane index and inline-attach flag but does not own scroll state —
// each child pane manages its own viewport.
type SplitPane struct {
	agent    *PreviewPane
	terminal *TerminalPane
	diff     *DiffPane

	focusedPane  int
	inlineAttach bool
	diffVisible  bool

	height int
	width  int

	instance *session.Instance
}

// NewSplitPane wires the three child panes into a SplitPane with the
// agent pane focused by default. The caller retains ownership of the
// child panes; SplitPane borrows them for routing.
func NewSplitPane(agent *PreviewPane, diff *DiffPane, terminal *TerminalPane) *SplitPane {
	return &SplitPane{
		agent:       agent,
		diff:        diff,
		terminal:    terminal,
		focusedPane: FocusAgent,
	}
}

// SetInstance sets the instance whose state the child panes will
// render on the next UpdateAgent/UpdateDiff/UpdateTerminal call. The
// child panes read their content from the instance, so switching here
// without calling the Update* methods leaves the previously-rendered
// content in place until the next tick.
func (s *SplitPane) SetInstance(instance *session.Instance) {
	s.instance = instance
}

// SetSize recomputes the 70/30 agent/terminal split for the given
// container dimensions and propagates widths to every child pane,
// including the diff overlay which uses the full inner height.
func (s *SplitPane) SetSize(width, height int) {
	s.width = width
	s.height = height

	borderH := paneBodyBorder.GetHorizontalFrameSize()
	bodyBorderV := paneBodyBorder.GetVerticalFrameSize() // bottom border only = 1

	// Clamp every derived dimension to >= 0. A small terminal drives the content
	// width to 0 (width-borderH) and the available height negative
	// (height-paneChrome); feeding a negative size to a child pane panics its
	// render path (strings.Repeat with a negative count, or a negative slice
	// bound in renderPane). Clamping here is the single chokepoint that sizes all
	// three children, so the whole view degrades to empty panes instead.
	contentWidth := max(s.width-borderH, 0)

	// Each pane = 1 (top border w/ title) + content + bodyBorderV (bottom border)
	// Two panes: 2 top lines + 2× bodyBorderV + agentContent + terminalContent = height
	paneChrome := 2 * (1 + bodyBorderV) // 2 panes × (top line + bottom border)
	availableHeight := max(height-paneChrome, 0)

	agentHeight := int(float64(availableHeight) * SplitAgentPercent)
	terminalHeight := availableHeight - agentHeight

	s.agent.SetSize(contentWidth, agentHeight)
	s.terminal.SetSize(contentWidth, terminalHeight)

	// Diff overlay uses a single pane
	s.diff.SetSize(contentWidth, max(height-1-bodyBorderV, 0)) // 1 top line + bottom border
}

// GetAgentSize returns the current width and height of the agent pane,
// primarily used by the attach flow to size the PTY before handing it
// to the user.
func (s *SplitPane) GetAgentSize() (width, height int) {
	return s.agent.width, s.agent.height
}

// SetInlineAttach toggles whether inline-attach mode is active,
// controlling whether the focused-pane highlight is rendered.
func (s *SplitPane) SetInlineAttach(attached bool) {
	s.inlineAttach = attached
}

// ToggleDiff shows or hides the diff overlay.
func (s *SplitPane) ToggleDiff() {
	s.diffVisible = !s.diffVisible
}

// IsDiffVisible returns true if the diff overlay is currently shown.
func (s *SplitPane) IsDiffVisible() bool {
	return s.diffVisible
}

// GetFocusedPane returns the currently focused pane constant.
func (s *SplitPane) GetFocusedPane() int {
	return s.focusedPane
}

// SetFocusedPane sets focus to the specified pane.
func (s *SplitPane) SetFocusedPane(pane int) {
	s.focusedPane = pane
}

// HitTest maps split-pane-local coordinates (x relative to the split's left edge,
// y from the top of the split) to a content pane and (row, col) within it.
// Returns ok=false for the diff overlay, borders, the title/bottom-border rows,
// or coordinates outside any content area. The returned pane is FocusAgent or
// FocusTerminal. Geometry mirrors renderPane: each box is a title row, a body
// bordered left/right/bottom (no top), stacked agent-over-terminal.
func (s *SplitPane) HitTest(localX, y int) (pane, row, col int, ok bool) {
	if s.diffVisible {
		return 0, 0, 0, false // selection over the diff overlay is unsupported in v1
	}
	col = localX - 1 // left body border
	if col < 0 || col >= s.agent.width {
		return 0, 0, 0, false
	}
	// Agent content occupies rows [1, 1+agentHeight); row 0 is the title border.
	if y >= 1 && y < 1+s.agent.height {
		return FocusAgent, y - 1, col, true
	}
	// Terminal content starts after agent title + agent body + agent bottom
	// border + terminal title = agent.height + 3.
	tTop := s.agent.height + 3
	if y >= tTop && y < tTop+s.terminal.height {
		return FocusTerminal, y - tTop, col, true
	}
	return 0, 0, 0, false
}

// BeginSelection starts a selection on the given content pane (clearing any
// selection on the other pane first).
func (s *SplitPane) BeginSelection(pane, row, col int) {
	s.ClearSelections()
	switch pane {
	case FocusAgent:
		s.agent.BeginSelection(row, col)
	case FocusTerminal:
		s.terminal.BeginSelection(row, col)
	}
}

// ExtendSelection moves the active selection's cursor on the given content pane.
func (s *SplitPane) ExtendSelection(pane, row, col int) {
	switch pane {
	case FocusAgent:
		s.agent.ExtendSelection(row, col)
	case FocusTerminal:
		s.terminal.ExtendSelection(row, col)
	}
}

// ClearSelections clears the selection on both content panes.
func (s *SplitPane) ClearSelections() {
	s.agent.ClearSelection()
	s.terminal.ClearSelection()
}

// SelectedText returns the selected text of the given content pane.
func (s *SplitPane) SelectedText(pane int) string {
	switch pane {
	case FocusAgent:
		return s.agent.SelectedText()
	case FocusTerminal:
		return s.terminal.SelectedText()
	}
	return ""
}

// UpdateAgent updates the agent (preview) pane content. Always updates since it's always visible.
func (s *SplitPane) UpdateAgent(instance *session.Instance) error {
	return s.agent.UpdateContent(instance)
}

// UpdateDiff updates the diff pane content. Only updates when the overlay is visible.
func (s *SplitPane) UpdateDiff(instance *session.Instance) {
	if !s.diffVisible {
		return
	}
	s.diff.SetDiff(instance)
}

// UpdateTerminal updates the terminal pane content. Always updates since it's always visible.
func (s *SplitPane) UpdateTerminal(instance *session.Instance) error {
	return s.terminal.UpdateContent(instance)
}

// ResetAgentToNormalMode resets the agent pane to normal mode.
func (s *SplitPane) ResetAgentToNormalMode(instance *session.Instance) error {
	return s.agent.ResetToNormalMode(instance)
}

// ScrollUp scrolls the active pane up by one line. Routing order:
// diff overlay (when visible) beats the focused pane. Scroll errors
// are logged rather than propagated because scroll is a view-only
// operation and should not abort the caller's update cycle.
func (s *SplitPane) ScrollUp() {
	if s.diffVisible {
		s.diff.ScrollUp()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.ScrollUp(s.instance); err != nil {
			log.For("ui").Info("split_pane.scroll_agent_up_failed", "err", err)
		}
	case FocusTerminal:
		if err := s.terminal.ScrollUp(); err != nil {
			log.For("ui").Info("split_pane.scroll_terminal_up_failed", "err", err)
		}
	}
}

// ScrollDown is the counterpart of ScrollUp; see ScrollUp for routing
// and error-handling rules.
func (s *SplitPane) ScrollDown() {
	if s.diffVisible {
		s.diff.ScrollDown()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.ScrollDown(s.instance); err != nil {
			log.For("ui").Info("split_pane.scroll_agent_down_failed", "err", err)
		}
	case FocusTerminal:
		if err := s.terminal.ScrollDown(); err != nil {
			log.For("ui").Info("split_pane.scroll_terminal_down_failed", "err", err)
		}
	}
}

// PageUp scrolls the active pane (diff if visible, else focused) up by half a view.
func (s *SplitPane) PageUp() {
	if s.diffVisible {
		s.diff.PageUp()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.PageUp(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to page agent up: %v", err)
		}
	case FocusTerminal:
		if err := s.terminal.PageUp(); err != nil {
			log.InfoLog.Printf("split pane failed to page terminal up: %v", err)
		}
	}
}

// PageDown scrolls the active pane down by half a view.
func (s *SplitPane) PageDown() {
	if s.diffVisible {
		s.diff.PageDown()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.PageDown(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to page agent down: %v", err)
		}
	case FocusTerminal:
		if err := s.terminal.PageDown(); err != nil {
			log.InfoLog.Printf("split pane failed to page terminal down: %v", err)
		}
	}
}

// GotoTop jumps the active pane to the start of its scrollback.
func (s *SplitPane) GotoTop() {
	if s.diffVisible {
		s.diff.GotoTop()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.GotoTop(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to goto agent top: %v", err)
		}
	case FocusTerminal:
		if err := s.terminal.GotoTop(); err != nil {
			log.InfoLog.Printf("split pane failed to goto terminal top: %v", err)
		}
	}
}

// GotoBottom jumps the active pane to the live tail, exiting scroll mode.
func (s *SplitPane) GotoBottom() {
	if s.diffVisible {
		s.diff.GotoBottom()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.GotoBottom(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to goto agent bottom: %v", err)
		}
	case FocusTerminal:
		s.terminal.GotoBottom()
	}
}

// ScrollAgentUp scrolls the agent pane explicitly, ignoring focus/diff.
func (s *SplitPane) ScrollAgentUp() {
	if err := s.agent.ScrollUp(s.instance); err != nil {
		log.InfoLog.Printf("split pane failed to scroll agent up: %v", err)
	}
}

// ScrollAgentDown scrolls the agent pane explicitly.
func (s *SplitPane) ScrollAgentDown() {
	if err := s.agent.ScrollDown(s.instance); err != nil {
		log.InfoLog.Printf("split pane failed to scroll agent down: %v", err)
	}
}

// ScrollTerminalUp scrolls the terminal pane explicitly.
func (s *SplitPane) ScrollTerminalUp() {
	if err := s.terminal.ScrollUp(); err != nil {
		log.InfoLog.Printf("split pane failed to scroll terminal up: %v", err)
	}
}

// ScrollTerminalDown scrolls the terminal pane explicitly.
func (s *SplitPane) ScrollTerminalDown() {
	if err := s.terminal.ScrollDown(); err != nil {
		log.InfoLog.Printf("split pane failed to scroll terminal down: %v", err)
	}
}

// PageTerminalUp pages the terminal pane explicitly.
func (s *SplitPane) PageTerminalUp() {
	if err := s.terminal.PageUp(); err != nil {
		log.InfoLog.Printf("split pane failed to page terminal up: %v", err)
	}
}

// PageTerminalDown pages the terminal pane explicitly.
func (s *SplitPane) PageTerminalDown() {
	if err := s.terminal.PageDown(); err != nil {
		log.InfoLog.Printf("split pane failed to page terminal down: %v", err)
	}
}

// ScrollDiffUp scrolls the diff overlay explicitly (no-op if not visible).
func (s *SplitPane) ScrollDiffUp() {
	if s.diffVisible {
		s.diff.ScrollUp()
	}
}

// ScrollDiffDown scrolls the diff overlay explicitly (no-op if not visible).
func (s *SplitPane) ScrollDiffDown() {
	if s.diffVisible {
		s.diff.ScrollDown()
	}
}

// IsAgentInScrollMode returns true if the agent pane is scrolled away from
// the live tail.
func (s *SplitPane) IsAgentInScrollMode() bool {
	return s.agent.IsScrolling()
}

// IsTerminalInScrollMode returns true if the terminal pane is in scroll mode.
func (s *SplitPane) IsTerminalInScrollMode() bool {
	return s.terminal.IsScrolling()
}

// ResetTerminalToNormalMode exits scroll mode on the terminal pane.
func (s *SplitPane) ResetTerminalToNormalMode() {
	s.terminal.ResetToNormalMode()
}

// TerminalTmuxSession returns the live tmux session backing the currently
// displayed terminal pane, or nil if none exists. Callers use this to drive a
// full-screen attach via tea.ExecProcess.
func (s *SplitPane) TerminalTmuxSession() *tmux.TmuxSession {
	return s.terminal.CurrentTmuxSession()
}

// CleanupTerminal closes the terminal session.
func (s *SplitPane) CleanupTerminal() {
	s.terminal.Close()
}

// DetachTerminalForInstance removes the cached terminal entry for the given
// instance title and returns the popped tmux session, so the caller can Close
// it off the update goroutine. Returns nil if nothing was cached.
func (s *SplitPane) DetachTerminalForInstance(title string) *tmux.TmuxSession {
	return s.terminal.DetachSessionForInstance(title)
}

// SendTerminalPrompt sends text followed by Enter to the terminal pane's tmux session.
func (s *SplitPane) SendTerminalPrompt(text string) error {
	return s.terminal.SendPrompt(text)
}

// SendTerminalKeysToInstance sends text followed by Enter to the named
// instance's cached terminal session. Unlike SendTerminalPrompt, this does
// not require the instance to be currently displayed.
func (s *SplitPane) SendTerminalKeysToInstance(title, text string) error {
	return s.terminal.SendKeysToInstance(title, text)
}

// SendTerminalKeysRaw writes raw bytes to the terminal pane's tmux PTY.
func (s *SplitPane) SendTerminalKeysRaw(b []byte) error {
	return s.terminal.SendKeysRaw(b)
}

// ForwardTerminalMouse forwards one SGR mouse event to the terminal pane's session.
func (s *SplitPane) ForwardTerminalMouse(cb, col, row int, press bool) error {
	return s.terminal.ForwardMouse(cb, col, row, press)
}

// PasteTerminal sends text to the terminal pane's session as a bracketed paste.
func (s *SplitPane) PasteTerminal(text string) error {
	return s.terminal.Paste(text)
}

func (s *SplitPane) String() string {
	if s.width == 0 || s.height == 0 {
		return ""
	}

	if s.diffVisible {
		bodyBorderV := focusedPaneBodyBorder.GetVerticalFrameSize()
		diffContent := s.diff.String()
		topLine := s.buildTopBorder(diffTitle(s.diff.ScrollPercent()), true)
		// .Width is the total box width (border included), so pass the full pane
		// width — matching renderPane and the manual top border's right corner.
		body := focusedPaneBodyBorder.
			Width(s.width).
			Height(s.height - 1 - bodyBorderV). // -1 for top line
			Render(diffContent)
		return clampHeight(lipgloss.JoinVertical(lipgloss.Left, topLine, body), s.height)
	}

	showFocus := s.inlineAttach
	agentTitle := " Agent" + scrollSuffix(s.agent.ScrollPercent()) + " "
	terminalTitle := " Terminal" + scrollSuffix(s.terminal.ScrollPercent()) + " "
	agentBox := s.renderPane(agentTitle, s.agent.String(), s.agent.height, showFocus && s.focusedPane == FocusAgent)
	terminalBox := s.renderPane(terminalTitle, s.terminal.String(), s.terminal.height, showFocus && s.focusedPane == FocusTerminal)

	// Hard-clamp to the allocated height: if a pane's content ever renders taller
	// than its box (e.g. an over-wide line wraps into extra rows), the whole view
	// would otherwise overflow the terminal and push the status/quick-input bar
	// off-screen.
	return clampHeight(lipgloss.JoinVertical(lipgloss.Left, agentBox, terminalBox), s.height)
}

// clampHeight truncates s to at most n rows so a component never overflows its
// allocated height.
func clampHeight(s string, n int) string {
	if n < 0 {
		n = 0
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// scrollSuffix returns " (NN% ↑)" when the pane is scrolled back from
// the bottom, or "" when at the bottom (= live tail for agent/terminal).
// Agent/terminal panes return 1.0 whenever they're not in scroll mode,
// so the suffix is only emitted during active review of past output.
func scrollSuffix(percent float64) string {
	if percent >= 1.0 {
		return ""
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf(" (%d%% ↑)", int(percent*100))
}

// diffTitle composes the diff overlay's title with an optional scroll
// indicator. The close hint always stays on; the percentage slots in
// just before it when scrolled: " Diff (42% ↑ · d/Esc to close) ".
func diffTitle(percent float64) string {
	if percent >= 1.0 {
		return " Diff (d/Esc to close) "
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf(" Diff (%d%% ↑ · d/Esc to close) ", int(percent*100))
}

// renderPane wraps content in a bordered box with the title embedded in the top border line.
func (s *SplitPane) renderPane(title, content string, innerHeight int, focused bool) string {
	if innerHeight < 0 {
		innerHeight = 0 // never slice/size with a negative bound (tiny terminal)
	}
	topLine := s.buildTopBorder(title, focused)

	border := paneBodyBorder
	if focused {
		border = focusedPaneBodyBorder
	}

	// Cap the content to innerHeight ROWS (not columns). If an over-wide line
	// wrapped into extra rows, the box would otherwise render taller than its
	// allocation, and the split-pane clamp would then clip its bottom border off.
	// Truncating rows here keeps the box (and its full border) exactly the right
	// size; truncating columns instead would shift the body and break the right
	// border's corner alignment.
	if lines := strings.Split(content, "\n"); len(lines) > innerHeight {
		content = strings.Join(lines[:innerHeight], "\n")
	}

	// lipgloss .Width/.Height set the TOTAL box size (border included), so pass
	// the full pane width — the left/right border consume 2 columns inside it,
	// leaving an s.width-2 content area that matches what the child panes render
	// to. Subtracting the frame here (as if the border were added outside) made
	// the body 2 columns short, so JoinVertical right-padded it with spaces and
	// the bottom/right border fell short of the manual top border's corner.
	body := border.
		Width(s.width).
		Height(innerHeight).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Left, topLine, body)
}

// buildTopBorder creates a top border line with an inline title: ╭── Title ─────────╮
func (s *SplitPane) buildTopBorder(title string, focused bool) string {
	borderColor := dimBorderColor
	titleStyle := paneTitleStyle
	if focused {
		borderColor = highlightColor
		titleStyle = focusedPaneTitleStyle
	}
	bc := lipgloss.NewStyle().Foreground(borderColor)

	titleRendered := titleStyle.Render(title)
	titleWidth := lipgloss.Width(titleRendered)

	// ╭ + ── + title + ─── ... ─── + ╮
	innerWidth := s.width - 2 // minus corners
	leftDashes := 2
	rightDashes := innerWidth - leftDashes - titleWidth
	if rightDashes < 0 {
		rightDashes = 0
	}

	return bc.Render("╭") +
		bc.Render(strings.Repeat("─", leftDashes)) +
		titleRendered +
		bc.Render(strings.Repeat("─", rightDashes)) +
		bc.Render("╮")
}
