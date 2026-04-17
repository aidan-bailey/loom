package app

import (
	"claude-squad/keys"
	"claude-squad/session"

	tea "github.com/charmbracelet/bubbletea"
)

// Action describes a single user-initiated operation bound to a key.
// Preconditions centralize guards (e.g. "no instance selected", "busy")
// that used to repeat across the handleKeyPress switch. Run returns
// the replacement model + any tea.Cmd the runtime should dispatch.
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
// a matching action existed; a missing key yields (nil, nil, false)
// so the caller can fall through to other state-specific handlers.
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
// input would race with in-flight lifecycle work.
func selectedNotBusy(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil {
		return false
	}
	s := selected.GetStatus()
	return s != session.Loading && s != session.Deleting
}

// selectedNotBusyNotWorkspace gates lifecycle mutations (kill, submit,
// checkout) that don't make sense for workspace-terminal instances —
// those have no branch/worktree to act on.
func selectedNotBusyNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	s := selected.GetStatus()
	return s != session.Loading && s != session.Deleting
}

// selectedPausedNotWorkspace gates resume: only a paused, non-workspace
// instance has a branch waiting to be checked back out.
func selectedPausedNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	return selected.GetStatus() == session.Paused
}

// selectedReadyForInput gates attach/quick-input: the instance must
// exist, have a live tmux pane, and not be mid-lifecycle.
func selectedReadyForInput(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !selected.TmuxAlive() {
		return false
	}
	s := selected.GetStatus()
	return s != session.Loading && s != session.Deleting
}

// selectedReadyForInputNotWorkspace adds the "not a workspace terminal"
// constraint required by full-screen attach, which would otherwise
// take over the main repo shell.
func selectedReadyForInputNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	return selectedReadyForInput(m)
}

// selectedReadyForQuickInput adds the "diff overlay not open" guard:
// the quick-input bar shares screen real estate with the diff view.
func selectedReadyForQuickInput(m *home) bool {
	if !selectedReadyForInput(m) {
		return false
	}
	return !m.splitPane.IsDiffVisible()
}

// hasMultipleSlots gates workspace-tab cycling: with 0 or 1 slot
// open, left/right are no-ops.
func hasMultipleSlots(m *home) bool {
	return len(m.slots) > 1
}

// defaultActions returns the full registry merged from each topical
// sub-registry. Every key handled in the main TUI state lives in one
// of these groups; Dispatch returning ok=false signals a truly
// unbound key (e.g., an ignored modifier combo).
func defaultActions() ActionRegistry {
	reg := ActionRegistry{}
	for _, sub := range []ActionRegistry{
		navActions(),
		lifecycleActions(),
		attachActions(),
		workspaceActions(),
		quickActions(),
	} {
		for k, v := range sub {
			reg[k] = v
		}
	}
	return reg
}
