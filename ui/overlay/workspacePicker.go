package overlay

import (
	"claude-squad/config"
	"claude-squad/ui"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// WorkspacePicker is an overlay that lets the user toggle active workspaces.
// In startup mode, it acts as a single-select picker with a "Global" option.
type WorkspacePicker struct {
	workspaces []config.Workspace
	cursor     int
	width      int
	active     map[string]bool
	// isStartup controls single-select behavior and adds a "Global" option.
	isStartup bool
	// totalItems is len(workspaces) or len(workspaces)+1 in startup mode (for Global).
	totalItems int
}

// NewWorkspacePicker creates a workspace picker overlay for toggling active workspaces.
// activeNames is a set of workspace names that are currently active.
func NewWorkspacePicker(workspaces []config.Workspace, activeNames map[string]bool) *WorkspacePicker {
	active := make(map[string]bool, len(activeNames))
	for k, v := range activeNames {
		active[k] = v
	}
	return &WorkspacePicker{
		workspaces: workspaces,
		cursor:     0,
		width:      50,
		active:     active,
		totalItems: len(workspaces),
	}
}

// NewStartupWorkspacePicker creates a single-select workspace picker for startup.
// Includes a "Global (default)" option at the end.
func NewStartupWorkspacePicker(workspaces []config.Workspace) *WorkspacePicker {
	return &WorkspacePicker{
		workspaces: workspaces,
		cursor:     0,
		width:      50,
		active:     make(map[string]bool),
		isStartup:  true,
		totalItems: len(workspaces) + 1, // +1 for Global
	}
}

// HandleKeyPress processes navigation and toggle/select keys.
// Returns (committed, _). committed=true means the overlay should close and apply state.
func (w *WorkspacePicker) HandleKeyPress(msg tea.KeyMsg) (bool, bool) {
	switch msg.String() {
	case "up", "k":
		if w.cursor > 0 {
			w.cursor--
		}
	case "down", "j":
		if w.cursor < w.totalItems-1 {
			w.cursor++
		}
	case " ", "enter":
		if w.isStartup {
			// In startup mode, enter commits the selection immediately.
			return true, false
		}
		if w.cursor < len(w.workspaces) {
			name := w.workspaces[w.cursor].Name
			w.active[name] = !w.active[name]
		}
	case "esc", "q":
		return true, false
	}
	return false, false
}

// IsStartup returns whether this is a startup picker.
func (w *WorkspacePicker) IsStartup() bool {
	return w.isStartup
}

// GetSelectedWorkspace returns the workspace selected in startup mode.
// Returns nil if "Global" is selected or if not in startup mode.
func (w *WorkspacePicker) GetSelectedWorkspace() *config.Workspace {
	if !w.isStartup {
		return nil
	}
	if w.cursor < len(w.workspaces) {
		ws := w.workspaces[w.cursor]
		return &ws
	}
	return nil // Global selected
}

// GetActiveWorkspaces returns workspaces that are currently toggled on.
func (w *WorkspacePicker) GetActiveWorkspaces() []config.Workspace {
	var result []config.Workspace
	for _, ws := range w.workspaces {
		if w.active[ws.Name] {
			result = append(result, ws)
		}
	}
	return result
}

// HandleKey satisfies the Overlay interface. The second bool from
// HandleKeyPress is reserved for future use and currently always
// false; HandleKey collapses to (closed, nil).
func (w *WorkspacePicker) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	closed, _ := w.HandleKeyPress(msg)
	return closed, nil
}

// View satisfies the Overlay interface.
func (w *WorkspacePicker) View() string {
	return w.Render()
}

// SetSize satisfies the Overlay interface. WorkspacePicker uses only
// width; height is accepted but ignored.
func (w *WorkspacePicker) SetSize(width, _ int) {
	w.width = width
}

// Render renders the workspace picker overlay.
func (w *WorkspacePicker) Render() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	selectedStyle := lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	normalStyle := lipgloss.NewStyle().Foreground(ui.TextPrimary)
	pathStyle := lipgloss.NewStyle().Foreground(ui.TextHint)

	var content string
	if w.isStartup {
		content += titleStyle.Render("Select Workspace") + "\n\n"
	} else {
		content += titleStyle.Render("Toggle Workspaces") + "\n\n"
	}

	for i, ws := range w.workspaces {
		cursor := "  "
		if i == w.cursor {
			cursor = "> "
		}

		if w.isStartup {
			line := fmt.Sprintf("%s  %s", cursor, ws.Name)
			path := fmt.Sprintf("      %s", ws.Path)
			if i == w.cursor {
				content += selectedStyle.Render(line) + "\n"
				content += selectedStyle.Render(path) + "\n"
			} else {
				content += normalStyle.Render(line) + "\n"
				content += pathStyle.Render(path) + "\n"
			}
		} else {
			check := "[ ]"
			if w.active[ws.Name] {
				check = "[x]"
			}
			line := fmt.Sprintf("%s%s %s", cursor, check, ws.Name)
			path := fmt.Sprintf("      %s", ws.Path)
			if i == w.cursor {
				content += selectedStyle.Render(line) + "\n"
				content += selectedStyle.Render(path) + "\n"
			} else {
				content += normalStyle.Render(line) + "\n"
				content += pathStyle.Render(path) + "\n"
			}
		}
	}

	// Render "Global" option in startup mode.
	if w.isStartup {
		globalIdx := len(w.workspaces)
		cursor := "  "
		if w.cursor == globalIdx {
			cursor = "> "
		}
		line := fmt.Sprintf("%s  Global (default)", cursor)
		if w.cursor == globalIdx {
			content += selectedStyle.Render(line) + "\n"
		} else {
			content += normalStyle.Render(line) + "\n"
		}
	}

	helpStyle := lipgloss.NewStyle().Foreground(ui.TextHint)
	if w.isStartup {
		content += "\n" + helpStyle.Render("enter select • esc global")
	} else {
		content += "\n" + helpStyle.Render("space toggle • esc done")
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(w.width)

	return border.Render(content)
}

// SetWidth sets the width of the overlay.
func (w *WorkspacePicker) SetWidth(width int) {
	w.width = width
}
