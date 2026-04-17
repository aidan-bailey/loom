package app

import (
	"claude-squad/keys"

	tea "github.com/charmbracelet/bubbletea"
)

// navActions registers pure navigation / view-toggle keys: cursor
// movement and the diff overlay. These never mutate lifecycle state,
// so their preconditions are limited to "something is selected".
func navActions() ActionRegistry {
	return ActionRegistry{
		keys.KeyUp: {
			Precondition: hasInstanceSelected,
			Run: func(m *home) (tea.Model, tea.Cmd) {
				m.list.Up()
				return m, m.instanceChanged()
			},
		},
		keys.KeyDown: {
			Precondition: hasInstanceSelected,
			Run: func(m *home) (tea.Model, tea.Cmd) {
				m.list.Down()
				return m, m.instanceChanged()
			},
		},
		keys.KeyDiff: {
			Run: func(m *home) (tea.Model, tea.Cmd) {
				m.splitPane.ToggleDiff()
				return m, m.instanceChanged()
			},
		},
	}
}
