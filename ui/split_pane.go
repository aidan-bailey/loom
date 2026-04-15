package ui

import (
	"claude-squad/log"
	"claude-squad/session"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var highlightColor = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

// AdjustPreviewWidth adjusts the width of the preview pane to be 90% of the provided width.
func AdjustPreviewWidth(width int) int {
	return int(float64(width) * 0.9)
}

const (
	FocusAgent    int = iota // top pane
	FocusTerminal            // bottom pane
)

var dimBorderColor = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"}

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

// SplitPane displays agent (preview) and terminal panes stacked vertically,
// with an optional diff overlay triggered by hotkey.
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

func NewSplitPane(agent *PreviewPane, diff *DiffPane, terminal *TerminalPane) *SplitPane {
	return &SplitPane{
		agent:       agent,
		diff:        diff,
		terminal:    terminal,
		focusedPane: FocusAgent,
	}
}

func (s *SplitPane) SetInstance(instance *session.Instance) {
	s.instance = instance
}

func (s *SplitPane) SetSize(width, height int) {
	s.width = width
	s.height = height

	borderH := paneBodyBorder.GetHorizontalFrameSize()
	bodyBorderV := paneBodyBorder.GetVerticalFrameSize() // bottom border only = 1

	contentWidth := s.width - borderH

	// Each pane = 1 (top border w/ title) + content + bodyBorderV (bottom border)
	// Two panes: 2 top lines + 2× bodyBorderV + agentContent + terminalContent = height
	paneChrome := 2 * (1 + bodyBorderV) // 2 panes × (top line + bottom border)
	availableHeight := height - paneChrome

	// 70/30 split
	agentHeight := int(float64(availableHeight) * 0.7)
	terminalHeight := availableHeight - agentHeight

	s.agent.SetSize(contentWidth, agentHeight)
	s.terminal.SetSize(contentWidth, terminalHeight)

	// Diff overlay uses a single pane
	s.diff.SetSize(contentWidth, height-1-bodyBorderV) // 1 top line + bottom border
}

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

func (s *SplitPane) ScrollUp() {
	if s.diffVisible {
		s.diff.ScrollUp()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.ScrollUp(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to scroll agent up: %v", err)
		}
	case FocusTerminal:
		if err := s.terminal.ScrollUp(); err != nil {
			log.InfoLog.Printf("split pane failed to scroll terminal up: %v", err)
		}
	}
}

func (s *SplitPane) ScrollDown() {
	if s.diffVisible {
		s.diff.ScrollDown()
		return
	}
	switch s.focusedPane {
	case FocusAgent:
		if err := s.agent.ScrollDown(s.instance); err != nil {
			log.InfoLog.Printf("split pane failed to scroll agent down: %v", err)
		}
	case FocusTerminal:
		if err := s.terminal.ScrollDown(); err != nil {
			log.InfoLog.Printf("split pane failed to scroll terminal down: %v", err)
		}
	}
}

// IsAgentInScrollMode returns true if the agent pane is in scroll mode.
func (s *SplitPane) IsAgentInScrollMode() bool {
	return s.agent.isScrolling
}

// IsTerminalInScrollMode returns true if the terminal pane is in scroll mode.
func (s *SplitPane) IsTerminalInScrollMode() bool {
	return s.terminal.IsScrolling()
}

// ResetTerminalToNormalMode exits scroll mode on the terminal pane.
func (s *SplitPane) ResetTerminalToNormalMode() {
	s.terminal.ResetToNormalMode()
}

// AttachTerminal attaches to the terminal tmux session.
func (s *SplitPane) AttachTerminal() (chan struct{}, error) {
	return s.terminal.Attach()
}

// CleanupTerminal closes the terminal session.
func (s *SplitPane) CleanupTerminal() {
	s.terminal.Close()
}

// CleanupTerminalForInstance closes the cached terminal session for the given instance title.
func (s *SplitPane) CleanupTerminalForInstance(title string) {
	s.terminal.CloseForInstance(title)
}

// SendTerminalPrompt sends text followed by Enter to the terminal pane's tmux session.
func (s *SplitPane) SendTerminalPrompt(text string) error {
	return s.terminal.SendPrompt(text)
}

// SendTerminalKeysRaw writes raw bytes to the terminal pane's tmux PTY.
func (s *SplitPane) SendTerminalKeysRaw(b []byte) error {
	return s.terminal.SendKeysRaw(b)
}

func (s *SplitPane) String() string {
	if s.width == 0 || s.height == 0 {
		return ""
	}

	if s.diffVisible {
		bodyBorderV := focusedPaneBodyBorder.GetVerticalFrameSize()
		borderH := focusedPaneBodyBorder.GetHorizontalFrameSize()
		contentWidth := s.width - borderH
		diffContent := s.diff.String()
		topLine := s.buildTopBorder(" Diff (d/Esc to close) ", true)
		body := focusedPaneBodyBorder.
			Width(contentWidth).
			Height(s.height - 1 - bodyBorderV). // -1 for top line
			Render(diffContent)
		return lipgloss.JoinVertical(lipgloss.Left, topLine, body)
	}

	showFocus := s.inlineAttach
	agentBox := s.renderPane(" Agent ", s.agent.String(), s.agent.height, showFocus && s.focusedPane == FocusAgent)
	terminalBox := s.renderPane(" Terminal ", s.terminal.String(), s.terminal.height, showFocus && s.focusedPane == FocusTerminal)

	return lipgloss.JoinVertical(lipgloss.Left, agentBox, terminalBox)
}

// renderPane wraps content in a bordered box with the title embedded in the top border line.
func (s *SplitPane) renderPane(title, content string, innerHeight int, focused bool) string {
	borderH := paneBodyBorder.GetHorizontalFrameSize()
	contentWidth := s.width - borderH

	topLine := s.buildTopBorder(title, focused)

	border := paneBodyBorder
	if focused {
		border = focusedPaneBodyBorder
	}

	body := border.
		Width(contentWidth).
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
