package app

import (
	"fmt"
	"github.com/aidan-bailey/loom/config"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStateWorkspaceKey drives the workspace picker overlay. In
// startup mode the committed selection activates exactly one workspace
// slot; in mid-session mode the committed set is diffed against the
// current slots and applied via applyWorkspaceToggle.
func handleStateWorkspaceKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wp := m.workspacePicker()
	if wp == nil {
		return m, nil
	}
	committed, _ := wp.HandleKeyPress(msg)
	if !committed {
		return m, nil
	}

	if wp.IsStartup() {
		selected := wp.GetSelectedWorkspace()
		m.dismissOverlay()
		m.state = stateDefault
		if selected != nil {
			wsCtx := config.WorkspaceContextFor(selected)
			m.activeCtx = wsCtx
			if err := m.activateWorkspace(*selected); err != nil {
				return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
			}
			m.loadSlot(0)
			m.updateTabBarStatuses()
			if m.registry != nil {
				_ = m.registry.UpdateLastUsed(selected.Name)
			}
			m.saveOpenWorkspaces()
		}
		// else: Global selected, keep current (global) state.
		return m, tea.WindowSize()
	}
	// Mid-session toggle: diff active workspaces.
	desired := wp.GetActiveWorkspaces()
	m.dismissOverlay()
	m.state = stateDefault
	return m, m.applyWorkspaceToggle(desired)
}
