package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/ui/overlay"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// workspaceActions registers keys that interact with workspace slots:
// open the picker, cycle left/right through active slots. The picker
// handles an empty registry with a user-visible error, so that logic
// stays in Run rather than in a precondition.
func workspaceActions() ActionRegistry {
	return ActionRegistry{
		keys.KeyWorkspace:      {Run: runOpenWorkspacePicker},
		keys.KeyWorkspaceLeft:  {Precondition: hasMultipleSlots, Run: runWorkspaceLeft},
		keys.KeyWorkspaceRight: {Precondition: hasMultipleSlots, Run: runWorkspaceRight},
	}
}

func runOpenWorkspacePicker(m *home) (tea.Model, tea.Cmd) {
	registry, err := config.LoadWorkspaceRegistry()
	if err != nil {
		return m, m.handleError(fmt.Errorf("failed to load workspace registry: %w", err))
	}
	if len(registry.Workspaces) == 0 {
		return m, m.handleError(fmt.Errorf("no workspaces registered"))
	}
	activeNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		activeNames[slot.wsCtx.Name] = true
	}
	m.workspacePicker = overlay.NewWorkspacePicker(registry.Workspaces, activeNames)
	m.state = stateWorkspace
	return m, nil
}

func runWorkspaceLeft(m *home) (tea.Model, tea.Cmd) {
	m.saveCurrentSlot()
	newIdx := (m.focusedSlot - 1 + len(m.slots)) % len(m.slots)
	m.loadSlot(newIdx)
	m.updateTabBarStatuses()
	m.persistFocusedWorkspace()
	return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
}

func runWorkspaceRight(m *home) (tea.Model, tea.Cmd) {
	m.saveCurrentSlot()
	newIdx := (m.focusedSlot + 1) % len(m.slots)
	m.loadSlot(newIdx)
	m.updateTabBarStatuses()
	m.persistFocusedWorkspace()
	return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
}
