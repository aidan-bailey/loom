package app

import (
	"claude-squad/keys"
	"claude-squad/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// quickActions registers low-ceremony keys: quick input bars (send a
// single line to the agent/terminal pane without attaching) and the
// help screen.
func quickActions() ActionRegistry {
	return ActionRegistry{
		keys.KeyQuickInputAgent: {
			Precondition: selectedReadyForQuickInput,
			Run:          runQuickInputAgent,
		},
		keys.KeyQuickInputTerminal: {
			Precondition: selectedReadyForQuickInput,
			Run:          runQuickInputTerminal,
		},
		keys.KeyHelp: {Run: runShowHelp},
	}
}

func runQuickInputAgent(m *home) (tea.Model, tea.Cmd) {
	m.state = stateQuickInteract
	m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetAgent)
	m.menu.SetState(ui.StateQuickInteract)
	return m, tea.WindowSize()
}

func runQuickInputTerminal(m *home) (tea.Model, tea.Cmd) {
	m.state = stateQuickInteract
	m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetTerminal)
	m.menu.SetState(ui.StateQuickInteract)
	return m, tea.WindowSize()
}

func runShowHelp(m *home) (tea.Model, tea.Cmd) {
	return m.showHelpScreen(helpTypeGeneral{}, nil)
}
