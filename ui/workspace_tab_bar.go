package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	wsHighlightColor = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	wsDimColor       = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"}

	wsActiveTabStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder(), true).
				BorderForeground(wsHighlightColor).
				Bold(true).
				Foreground(wsHighlightColor).
				AlignHorizontal(lipgloss.Center)

	wsInactiveTabStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder(), true).
				BorderForeground(wsDimColor).
				AlignHorizontal(lipgloss.Center)
)

var wsPromptIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#e5c07b")).Bold(true)
var wsRunningIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#61afef")).Bold(true)
var wsReadyIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#51bd73")).Bold(true)
var wsLoadingIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#c678dd")).Bold(true)
var wsPausedIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

// TabStatus represents the highest-priority instance status within a workspace tab.
// Precedence (high→low): Prompting > Running > Ready > Loading > Paused > None.
type TabStatus int

const (
	TabStatusNone TabStatus = iota
	TabStatusPaused
	TabStatusLoading
	TabStatusReady
	TabStatusRunning
	TabStatusPrompting
)

// WorkspaceTabBar renders a row of workspace tabs at the top of the TUI.
type WorkspaceTabBar struct {
	names      []string
	focusedIdx int
	width      int
	statuses   []TabStatus // per-tab highest-priority status
}

// NewWorkspaceTabBar creates a new workspace tab bar.
func NewWorkspaceTabBar() *WorkspaceTabBar {
	return &WorkspaceTabBar{}
}

// SetWorkspaces updates the displayed workspace names and focused index.
func (b *WorkspaceTabBar) SetWorkspaces(names []string, focused int) {
	b.names = names
	b.focusedIdx = focused
	b.statuses = make([]TabStatus, len(names))
}

// SetStatuses updates the per-tab status indicators.
func (b *WorkspaceTabBar) SetStatuses(statuses []TabStatus) {
	b.statuses = statuses
}

// SetWidth sets the available width for the tab bar.
func (b *WorkspaceTabBar) SetWidth(w int) {
	b.width = w
}

// Height returns the rendered height: 0 when empty, 3 when tabs are present
// (top border + content + bottom border).
func (b *WorkspaceTabBar) Height() int {
	if len(b.names) == 0 {
		return 0
	}
	return 3
}

// String renders the tab bar. Returns "" when there are no workspaces.
func (b *WorkspaceTabBar) String() string {
	if len(b.names) == 0 || b.width == 0 {
		return ""
	}

	var renderedTabs []string
	tabWidth := b.width / len(b.names)
	lastTabWidth := b.width - tabWidth*(len(b.names)-1)

	for i, name := range b.names {
		width := tabWidth
		if i == len(b.names)-1 {
			width = lastTabWidth
		}

		label := name
		if i < len(b.statuses) {
			switch b.statuses[i] {
			case TabStatusPrompting:
				label = name + " " + wsPromptIndicator.Render("●")
			case TabStatusRunning:
				label = name + " " + wsRunningIndicator.Render("●")
			case TabStatusReady:
				label = name + " " + wsReadyIndicator.Render("●")
			case TabStatusLoading:
				label = name + " " + wsLoadingIndicator.Render("●")
			case TabStatusPaused:
				label = name + " " + wsPausedIndicator.Render("●")
			}
		}

		var style lipgloss.Style
		if i == b.focusedIdx {
			style = wsActiveTabStyle
		} else {
			style = wsInactiveTabStyle
		}
		style = style.Width(width - style.GetHorizontalFrameSize())
		renderedTabs = append(renderedTabs, style.Render(label))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)
}
