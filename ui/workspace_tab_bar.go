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

// WorkspaceTabBar renders a row of workspace tabs at the top of the TUI.
type WorkspaceTabBar struct {
	names      []string
	focusedIdx int
	width      int
}

// NewWorkspaceTabBar creates a new workspace tab bar.
func NewWorkspaceTabBar() *WorkspaceTabBar {
	return &WorkspaceTabBar{}
}

// SetWorkspaces updates the displayed workspace names and focused index.
func (b *WorkspaceTabBar) SetWorkspaces(names []string, focused int) {
	b.names = names
	b.focusedIdx = focused
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

		var style lipgloss.Style
		if i == b.focusedIdx {
			style = wsActiveTabStyle
		} else {
			style = wsInactiveTabStyle
		}
		style = style.Width(width - style.GetHorizontalFrameSize())
		renderedTabs = append(renderedTabs, style.Render(name))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)
}
