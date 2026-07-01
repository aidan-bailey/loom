package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// handleStatePromptKey runs while the prompt+branch-picker overlay is
// active. Branch-filter events drive a debounced search; submit kicks
// off Start for a not-yet-started instance or SendPrompt for a running
// one; cancel routes through cancelPromptOverlay to clean up unstarted
// instances.
func handleStatePromptKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

				// Finalize + launch. Remote control is applied in Sync (only
				// added when auth is OK); when auth is Blocked we prompt first,
				// and the same task runs on "start anyway".
				startTask := overlay.ConfirmationTask{
					Sync: func() {
						selected.Program = remoteControlProgram(m.appConfig, m.rcAuth, selected.Program, selected.Title)
						_ = selected.TransitionTo(session.Loading)
						m.newInstanceFinalizer()
						m.dismissOverlay()
						m.state = stateDefault
						m.menu.SetState(ui.StateDefault)
					},
					Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
						err := selected.Start(true)
						return instanceStartedMsg{
							instance:        selected,
							err:             err,
							promptAfterName: false,
							selectedBranch:  selectedBranch,
						}
					}),
				}

				if m.remoteControlBlocked(selected.Program) {
					return m, m.promptRemoteControlBlocked(startTask)
				}
				return m, tea.Batch(startTask.Run(), m.instanceChanged())
			}

			// Regular flow: instance already running, just send prompt
			if err := selected.SendPrompt(prompt); err != nil {
				return m, m.handleError(err)
			}
		}

		m.dismissOverlay()
		m.state = stateDefault
		return m, tea.Sequence(
			tea.RequestWindowSize,
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
