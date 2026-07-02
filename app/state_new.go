package app

import (
	"fmt"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	runewidth "github.com/mattn/go-runewidth"
)

// handleStateNewKey runs while the title-entry overlay is active. The
// instance is already appended to the list (in a pre-started form);
// Enter finalizes it and kicks off Start, Esc/ctrl+c pops it back out.
func handleStateNewKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Handle quit commands first. Don't handle q because the user might want to type that.
	if msg.String() == "ctrl+c" {
		m.state = stateDefault
		m.promptAfterName = false
		popped := m.list.PopSelectedForKill()
		return m, tea.Batch(
			tea.Sequence(
				tea.RequestWindowSize,
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			),
			backgroundKillCmd(popped),
		)
	}

	instance := m.list.GetInstances()[m.list.NumInstances()-1]
	switch msg.Code {
	// Start the instance (enable previews etc) and go back to the main menu state.
	case tea.KeyEnter:
		if len(instance.Title) == 0 {
			return m, m.handleError(fmt.Errorf("title cannot be empty"))
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

		// Finalize + launch. Remote control is applied in Sync (only added
		// when auth is OK); when auth is Blocked we prompt first, and the
		// same task runs on "start anyway".
		startTask := overlay.ConfirmationTask{
			Sync: func() {
				instance.Program = permissionModeProgram(m.appConfig, remoteControlProgram(m.appConfig, m.rcAuth, instance.Program, instance.Title))
				_ = instance.TransitionTo(session.Loading)
				m.newInstanceFinalizer()
				m.promptAfterName = false
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
			},
			Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}),
		}

		if m.remoteControlBlocked(instance.Program) {
			return m, m.promptRemoteControlBlocked(startTask)
		}
		return m, tea.Batch(startTask.Run(), m.instanceChanged())
	case tea.KeyBackspace:
		runes := []rune(instance.Title)
		if len(runes) == 0 {
			return m, nil
		}
		if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
			return m, m.handleError(err)
		}
	case tea.KeySpace:
		if err := instance.SetTitle(instance.Title + " "); err != nil {
			return m, m.handleError(err)
		}
	case tea.KeyEsc:
		popped := m.list.PopSelectedForKill()
		m.state = stateDefault
		m.instanceChanged()

		return m, tea.Batch(
			tea.Sequence(
				tea.RequestWindowSize,
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			),
			backgroundKillCmd(popped),
		)
	default:
		// Printable text (was tea.KeyRunes in v1).
		if msg.Text == "" {
			break
		}
		if runewidth.StringWidth(instance.Title) >= 32 {
			return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
		}
		if err := instance.SetTitle(instance.Title + msg.Text); err != nil {
			return m, m.handleError(err)
		}
	}
	return m, nil
}
