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

var (
	splitPaneBorder = lipgloss.NewStyle().
			BorderForeground(highlightColor).
			Border(lipgloss.NormalBorder())
	separatorStyle = lipgloss.NewStyle().
			Foreground(highlightColor)
	focusedSeparatorStyle = lipgloss.NewStyle().
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

	focusedPane int
	diffVisible bool

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
	s.width = AdjustPreviewWidth(width)
	s.height = height

	contentWidth := s.width - splitPaneBorder.GetHorizontalFrameSize()
	borderV := splitPaneBorder.GetVerticalFrameSize()

	// 1 line for the separator between panes
	separatorHeight := 1
	availableHeight := height - borderV - separatorHeight

	// 70/30 split
	agentHeight := int(float64(availableHeight) * 0.7)
	terminalHeight := availableHeight - agentHeight

	s.agent.SetSize(contentWidth, agentHeight)
	s.terminal.SetSize(contentWidth, terminalHeight)

	// Diff overlay gets the full content area
	s.diff.SetSize(contentWidth, height-borderV)
}

func (s *SplitPane) GetAgentSize() (width, height int) {
	return s.agent.width, s.agent.height
}

// ToggleFocus swaps focus between agent and terminal panes.
func (s *SplitPane) ToggleFocus() {
	if s.focusedPane == FocusAgent {
		s.focusedPane = FocusTerminal
	} else {
		s.focusedPane = FocusAgent
	}
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

	borderV := splitPaneBorder.GetVerticalFrameSize()

	if s.diffVisible {
		// Render diff overlay filling the full area
		diffContent := s.diff.String()
		title := diffOverlayTitleStyle.Render(" Diff ") +
			lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
				Render("(d/Esc to close)")
		window := splitPaneBorder.Render(
			lipgloss.Place(
				s.width-splitPaneBorder.GetHorizontalFrameSize(),
				s.height-borderV,
				lipgloss.Left, lipgloss.Top,
				diffContent))
		return lipgloss.JoinVertical(lipgloss.Left, title, window)
	}

	// Render both panes stacked
	agentContent := s.agent.String()
	terminalContent := s.terminal.String()

	// Build separator line with focus indicator
	sep := s.buildSeparator()

	// Stack: agent, separator, terminal
	stacked := lipgloss.JoinVertical(lipgloss.Left,
		agentContent,
		sep,
		terminalContent,
	)

	window := splitPaneBorder.Render(
		lipgloss.Place(
			s.width-splitPaneBorder.GetHorizontalFrameSize(),
			s.height-borderV,
			lipgloss.Left, lipgloss.Top,
			stacked))

	return window
}

// buildSeparator creates the horizontal separator between panes with a focus label.
func (s *SplitPane) buildSeparator() string {
	contentWidth := s.width - splitPaneBorder.GetHorizontalFrameSize()

	var label string
	if s.focusedPane == FocusTerminal {
		label = " Terminal "
	} else {
		label = " Agent "
	}

	labelRendered := focusedSeparatorStyle.Render(label)
	labelWidth := lipgloss.Width(labelRendered)

	remaining := contentWidth - labelWidth
	if remaining < 0 {
		remaining = 0
	}
	leftLen := 2
	rightLen := remaining - leftLen
	if rightLen < 0 {
		rightLen = 0
		leftLen = remaining
	}

	left := separatorStyle.Render(strings.Repeat("─", leftLen))
	right := separatorStyle.Render(strings.Repeat("─", rightLen))

	return left + labelRendered + right
}
