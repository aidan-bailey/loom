package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
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
		// A creation flow's prompt targets its pending instance; otherwise
		// the overlay was opened to prompt the selected, running session.
		selected := m.pendingNew
		if selected == nil {
			selected = m.list.GetSelectedInstance()
		}
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

				// "#123 …" expands into the issue's seeded prompt before the
				// launch options modal opens. The fetch is async, so the
				// overlay is dismissed now and the flow resumes in
				// handleIssueExpanded once it resolves.
				if n, rest, ok := github.ParseShorthand(prompt); ok && !(m.ghAvailable.checked && !m.ghAvailable.ok) {
					m.dismissOverlay()
					m.state = stateDefault
					// The flow is suspended until the expansion lands;
					// openLaunchOptionsForNew re-arms pendingNew then.
					m.pendingNew = nil
					return m, issueExpandCmd(m.repoPath(), n, selected, rest, prompt, selectedBranch)
				}

				m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
					owner := m.workspaceSlot // stamped for instanceStartedMsg
					startTask := overlay.ConfirmationTask{
						Sync: func() {
							m.pendingNew = nil // the start owns it now
							selected.Program = applyLaunchOptions(opts, m.rcAuth, selected.Program, selected.Title)
							selected.HeadroomProxy = opts.HeadroomProxy
							selected.CacheTTL1h = opts.CacheTTL1h
							// Always recorded, edited or not, so branch composition has
							// a single source of truth instead of falling back to a
							// re-read of config.json inside the git package.
							selected.SetBranchPrefix(opts.BranchPrefix)
							_ = selected.TransitionTo(session.Loading)
							m.state = stateDefault
							m.menu.SetState(ui.StateDefault)
						},
						Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
							err := selected.Start(true)
							return instanceStartedMsg{
								instance:       selected,
								err:            err,
								selectedBranch: selectedBranch,
								slot:           owner,
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
