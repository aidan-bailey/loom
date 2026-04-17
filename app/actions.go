package app

import (
	"claude-squad/keys"
	"claude-squad/session"

	tea "github.com/charmbracelet/bubbletea"
)

// Action describes a single user-initiated operation bound to a key.
// Preconditions centralize guards (e.g. "no instance selected", "busy")
// that currently repeat across the handleKeyPress switch. Run returns
// the replacement model + any tea.Cmd the runtime should dispatch.
//
// This type is the seed of a broader migration: today only a handful
// of simple, read-only actions live here. Mutating actions (kill,
// pause, resume, new instance) stay in handleKeyPress until they can
// be ported with the same behavior and test coverage.
type Action struct {
	// Precondition returns true when the action may run. false skips
	// Run entirely — useful for "instance is busy" gates that would
	// otherwise noop inside every handler.
	Precondition func(*home) bool
	// Run executes the action and returns the standard Bubble Tea
	// model+cmd pair.
	Run func(*home) (tea.Model, tea.Cmd)
}

// ActionRegistry maps keys to actions. Lookup is O(1); registration
// is done once at home construction.
type ActionRegistry map[keys.KeyName]Action

// Dispatch executes the action for the given key. ok reports whether
// a matching action existed; when ok is false the caller falls back
// to the legacy switch.
func (r ActionRegistry) Dispatch(name keys.KeyName, m *home) (tea.Model, tea.Cmd, bool) {
	action, exists := r[name]
	if !exists {
		return nil, nil, false
	}
	if action.Precondition != nil && !action.Precondition(m) {
		return m, nil, true
	}
	model, cmd := action.Run(m)
	return model, cmd, true
}

// hasInstanceSelected is a common precondition: an instance must be
// selected for the action to make sense. Used by nav/diff/etc.
func hasInstanceSelected(m *home) bool {
	return m.list.GetSelectedInstance() != nil
}

// selectedNotBusy returns true unless the selected instance is in a
// terminal/transient status (Loading or Deleting) where further
// input would race with in-flight lifecycle work. Matches the
// existing pattern repeated in the kill/pause/resume handlers.
func selectedNotBusy(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil {
		return false
	}
	s := selected.GetStatus()
	return s != session.Loading && s != session.Deleting
}

// defaultActions returns the registry populated with the subset of
// keypresses that have been migrated from handleKeyPress. Unmigrated
// keys fall through to the legacy switch — the dispatcher caller
// MUST treat a missing key as "try the switch next".
func defaultActions() ActionRegistry {
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
