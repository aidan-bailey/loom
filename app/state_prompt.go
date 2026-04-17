package app

import (
	"claude-squad/session"
	"claude-squad/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStatePromptKey runs while the prompt+branch-picker overlay is
// active. Branch-filter events drive a debounced search; submit kicks
// off Start for a not-yet-started instance or SendPrompt for a running
// one; cancel routes through cancelPromptOverlay to clean up unstarted
// instances.
func handleStatePromptKey(m *home, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle cancel via ctrl+c before delegating to the overlay
	if msg.String() == "ctrl+c" {
		return m, m.cancelPromptOverlay()
	}

	ti := m.textInput()
	if ti == nil {
		return m, nil
	}

	shouldClose, branchFilterChanged := ti.HandleKeyPress(msg)

	if shouldClose {
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}

		if ti.IsCanceled() {
			return m, m.cancelPromptOverlay()
		}

		if ti.IsSubmitted() {
			prompt := ti.GetValue()
			selectedBranch := ti.GetSelectedBranch()
			selectedProgram := ti.GetSelectedProgram()

			if !selected.Started() {
				// Shift+N flow: instance not started yet — set branch, start, then send prompt
				if selectedBranch != "" {
					selected.SetSelectedBranch(selectedBranch)
				}
				if selectedProgram != "" {
					selected.Program = selectedProgram
				}
				selected.Prompt = prompt

				_ = selected.TransitionTo(session.Loading)
				m.newInstanceFinalizer()
				m.dismissOverlay()
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)

				startCmd := func() tea.Msg {
					err := selected.Start(true)
					return instanceStartedMsg{
						instance:        selected,
						err:             err,
						promptAfterName: false,
						selectedBranch:  selectedBranch,
					}
				}

				return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
			}

			// Regular flow: instance already running, just send prompt
			if err := selected.SendPrompt(prompt); err != nil {
				return m, m.handleError(err)
			}
		}

		m.dismissOverlay()
		m.state = stateDefault
		return m, tea.Sequence(
			tea.WindowSize(),
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				m.showHelpScreen(helpStart(selected), nil)
				return nil
			},
		)
	}

	if branchFilterChanged {
		filter := ti.BranchFilter()
		version := ti.BranchFilterVersion()
		return m, m.scheduleBranchSearch(filter, version)
	}

	return m, nil
}
