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
				// Shift+N flow: instance not started yet — set branch, then
				// show the Session Launch Options modal before starting.
				if selectedBranch != "" {
					selected.SetSelectedBranch(selectedBranch)
				}
				if selectedProgram != "" {
					selected.Program = selectedProgram
				}
				selected.Prompt = prompt

				m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
					startTask := overlay.ConfirmationTask{
						Sync: func() {
							selected.Program = applyLaunchOptions(opts, m.rcAuth, selected.Program, selected.Title)
							selected.HeadroomProxy = opts.HeadroomProxy
							selected.CacheTTL1h = opts.CacheTTL1h
							_ = selected.TransitionTo(session.Loading)
							m.newInstanceFinalizer()
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

					if m.remoteControlBlocked(effectiveRemoteControl(opts), selected.Program) {
						return m, m.promptRemoteControlBlocked(startTask)
					}
					return m, tea.Batch(startTask.Run(), m.instanceChanged())
				}
				m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
				m.state = stateLaunchOptions
				m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
				m.menu.SetState(ui.StateNewInstance)
				return m, tea.RequestWindowSize
			}

			// Regular flow: instance already running, just send prompt
			if err := selected.SendPrompt(prompt); err != nil {
				return m, m.handleError(err)
			}
		}

		m.dismissOverlay()
		m.state = stateDefault
		// showHelpScreen mutates model state and writes app state to
		// disk, so it must run on the main goroutine — hand it back via
		// a message instead of calling it inside the (goroutine-run)
		// Sequence closure. The handler also resets the menu state.
		return m, tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg { return showHelpScreenMsg{helpType: helpStart(selected)} },
		)
	}

	if branchFilterChanged {
		filter := ti.BranchFilter()
		version := ti.BranchFilterVersion()
		return m, m.scheduleBranchSearch(filter, version)
	}

	return m, nil
}
