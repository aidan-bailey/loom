package app

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	runewidth "github.com/mattn/go-runewidth"
)

// handleStateNewKey runs while the title-entry overlay is active. The
// draft is already shown as the list's last row and held as m.draft;
// Enter finalizes its title and moves on to the prompt or the Session
// Launch Options modal, Esc/ctrl+c discards it (discardDraft).
func handleStateNewKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Handle quit commands first. Don't handle q because the user might want to type that.
	if msg.String() == "ctrl+c" {
		m.state = stateDefault
		m.promptAfterName = false
		m.menu.SetState(ui.StateDefault)
		m.discardDraft()
		return m, tea.Batch(m.instanceChanged(), tea.RequestWindowSize)
	}

	// The draft being named — by identity, never the selection or the
	// list's last row, which a completion landing mid-flow can change.
	d := m.draft
	if d == nil {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, nil
	}
	switch msg.Code {
	// Start the instance (enable previews etc) and go back to the main menu state.
	case tea.KeyEnter:
		if len(d.title) == 0 {
			return m, m.handleError(fmt.Errorf("title cannot be empty"))
		}
		if err := m.preservedTitleErr(d.title); err != nil {
			return m, m.handleError(err)
		}

		// If promptAfterName, show prompt+branch overlay before starting
		if m.promptAfterName {
			m.promptAfterName = false
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			ti := m.newPromptOverlay()
			m.setOverlay(ti, overlayTextInput)
			// Trigger initial branch search (no debounce, version 0)
			initialSearch := m.runBranchSearch("", ti.BranchFilterVersion())
			return m, tea.Batch(tea.RequestWindowSize, initialSearch)
		}

		// Show the Session Launch Options modal, seeded from the global
		// config, before actually starting. Confirming there runs the
		// closure openLaunchOptionsForNew stashes (Create the draft with
		// the chosen overrides) via handleStateLaunchOptionsKey.
		return m.openLaunchOptionsForNew(d, "")
	case tea.KeyBackspace:
		runes := []rune(d.title)
		if len(runes) == 0 {
			return m, nil
		}
		d.title = string(runes[:len(runes)-1])
	case tea.KeySpace:
		d.title += " "
	case tea.KeyEsc:
		m.discardDraft()
		m.state = stateDefault
		// Before instanceChanged, whose menu refresh leaves the
		// new-instance menu state alone.
		m.menu.SetState(ui.StateDefault)

		return m, tea.Batch(
			// instanceChanged's returned Cmd surfaces pane-update errors;
			// discarding it would silently swallow them.
			m.instanceChanged(),
			tea.RequestWindowSize,
		)
	default:
		// Printable text (was tea.KeyRunes in v1).
		if msg.Text == "" {
			break
		}
		if runewidth.StringWidth(d.title) >= 32 {
			return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
		}
		d.title += msg.Text
	}
	// The split pane and the menu hold a copy of the draft's row.
	m.refreshSelection()
	return m, nil
}

// preservedTitleErr rejects a new session title that belongs to a record
// the focused storage preserves on disk but could not load (a reconcile
// failure, or a newer loom's record after a downgrade). Storage never
// dedupes against such records, so creating the session would persist a
// second record under the title, and the two would share a tmux session
// name. Returns nil when the title is free.
func (m *home) preservedTitleErr(title string) error {
	if m.storage() == nil || !slices.Contains(m.storage().PreservedTitles(), title) {
		return nil
	}
	return fmt.Errorf("title %q belongs to a saved session this version of loom could not load; choose another", title)
}

// latchedStorageErr refuses to create a session while the focused storage's
// write latch is engaged (Storage.WritesRefused): the session could never be
// persisted. It also keeps a latched list exactly as loaded — empty — which
// is what makes skipping its save lossless (applyWorkspaceToggle,
// handleQuit). Returns nil when the storage accepts writes.
func (m *home) latchedStorageErr() error {
	if m.storage() == nil || !m.storage().WritesRefused() {
		return nil
	}
	return fmt.Errorf("new sessions can't be created here: this workspace's saved sessions could not be read, so nothing can be saved (see loom.log)")
}
