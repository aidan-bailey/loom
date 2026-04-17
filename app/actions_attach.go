package app

import (
	"claude-squad/keys"
	"claude-squad/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// attachActions registers keys that attach the terminal to an instance
// pane: inline (split-pane takeover) and full-screen (process exec).
// All four variants require a live, non-busy instance; the
// full-screen variants additionally reject workspace-terminal
// instances since the full-screen exec would steal the main repo.
func attachActions() ActionRegistry {
	return ActionRegistry{
		keys.KeyDirectAttachAgent: {
			Precondition: selectedReadyForInput,
			Run:          runInlineAttachAgent,
		},
		keys.KeyDirectAttachTerminal: {
			Precondition: selectedReadyForInput,
			Run:          runInlineAttachTerminal,
		},
		keys.KeyFullScreenAttachAgent: {
			Precondition: selectedReadyForInputNotWorkspace,
			Run:          runFullScreenAttachAgent,
		},
		keys.KeyFullScreenAttachTerminal: {
			Precondition: selectedReadyForInputNotWorkspace,
			Run:          runFullScreenAttachTerminal,
		},
	}
}

func runInlineAttachAgent(m *home) (tea.Model, tea.Cmd) {
	m.splitPane.SetFocusedPane(ui.FocusAgent)
	m.splitPane.SetInlineAttach(true)
	m.state = stateInlineAttach
	m.menu.SetState(ui.StateInlineAttach)
	return m, tea.WindowSize()
}

func runInlineAttachTerminal(m *home) (tea.Model, tea.Cmd) {
	m.splitPane.SetFocusedPane(ui.FocusTerminal)
	m.splitPane.SetInlineAttach(true)
	m.state = stateInlineAttach
	m.menu.SetState(ui.StateInlineAttach)
	return m, tea.WindowSize()
}

func runFullScreenAttachAgent(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	// Show the help overlay (if unseen); when dismissed, fire the attach
	// message. Running tea.ExecProcess from inside a dismiss closure would
	// execute within Update; instead we return it as a Cmd to the runtime.
	return m.showHelpScreen(helpTypeInstanceAttach{}, func() tea.Cmd {
		return startAttachCmd(selected, attachTargetAgent)
	})
}

func runFullScreenAttachTerminal(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	return m.showHelpScreen(helpTypeInstanceAttach{}, func() tea.Cmd {
		return startAttachCmd(selected, attachTargetTerminal)
	})
}
