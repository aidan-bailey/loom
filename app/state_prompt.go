package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/github"

	tea "charm.land/bubbletea/v2"
)

// handleStatePromptKey runs while the prompt+branch-picker overlay is
// active. Branch-filter events drive a debounced search; submit moves a
// creation flow's draft on to the launch options (and Create), or sends
// SendPrompt to a running session; cancel routes through
// cancelPromptOverlay to discard the draft.
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
		// A creation flow's prompt targets its draft; otherwise the overlay
		// was opened to prompt the selected, running session.
		d := m.draft
		var selected *core.InstanceView
		if d == nil {
			if selected = m.list.GetSelectedInstance(); selected == nil {
				return m, nil
			}
		}

		if ti.IsCanceled() {
			return m, m.cancelPromptOverlay()
		}

		if ti.IsSubmitted() {
			prompt := ti.GetValue()
			selectedBranch := ti.GetSelectedBranch()
			selectedProgram := ti.GetSelectedProgram()

			if d != nil {
				// Shift+N flow: no session yet — set the draft's branch,
				// program and prompt, then show the Session Launch Options
				// modal before creating it.
				if selectedBranch != "" {
					d.branch = selectedBranch
				}
				if selectedProgram != "" {
					d.program = selectedProgram
				}
				d.prompt = prompt

				// "#123 …" expands into the issue's seeded prompt before the
				// launch options modal opens. The fetch is async, so the
				// overlay is dismissed now and the flow resumes in
				// handleIssueExpanded once it resolves.
				if n, rest, ok := github.ParseShorthand(prompt); ok && !m.core.GitHubUnavailable() {
					m.dismissOverlay()
					m.state = stateDefault
					// The flow is suspended until the expansion lands; the
					// draft stays open, its row shown, and
					// handleIssueExpanded reopens the flow on it.
					return m, issueExpandCmd(m.repoPath(), n, d, rest, prompt, selectedBranch)
				}

				return m.openLaunchOptionsForNew(d, selectedBranch)
			}

			// Regular flow: instance already running, just send the prompt,
			// off the Update goroutine (three tmux subprocesses and a pause):
			// a request, whose job the drain hands to the runtime.
			// The overlay closes now; a failed send comes back as an error,
			// and a send still landing holds this one (sendingTo says so).
			if !m.sendingTo(selected) {
				m.sendPrompt(selected, prompt)
			}
		}

		m.dismissOverlay()
		m.state = stateDefault
		// The help names the session's branch and program: its row (the
		// draft's, for a flow closed neither submitted nor cancelled).
		var started *core.InstanceView
		if d != nil {
			row := d.row(m)
			started = &row
		} else {
			started, _ = m.viewByID(selected.ID)
		}
		// showHelpScreen mutates model state and writes app state to
		// disk, so it must run on the main goroutine — hand it back via
		// a message instead of calling it inside the (goroutine-run)
		// Sequence closure. The handler also resets the menu state.
		return m, tea.Sequence(
			tea.RequestWindowSize,
			func() tea.Msg { return showHelpScreenMsg{helpType: helpStart(started)} },
		)
	}

	if branchFilterChanged {
		filter := ti.BranchFilter()
		version := ti.BranchFilterVersion()
		return m, m.scheduleBranchSearch(filter, version)
	}

	return m, nil
}
