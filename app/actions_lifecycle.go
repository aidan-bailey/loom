package app

import (
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// lifecycleActions registers keys that mutate an instance's state
// machine: new, prompt, kill, submit (push), checkout (pause), resume.
// Preconditions gate the targets that must exist in a usable status;
// the error-returning capacity check for KeyNew/KeyPrompt stays in
// Run since it produces a user-visible error message.
func lifecycleActions() ActionRegistry {
	return ActionRegistry{
		keys.KeyPrompt:   {Run: runPromptNewInstance},
		keys.KeyNew:      {Run: runNewInstance},
		keys.KeyKill:     {Precondition: selectedNotBusyNotWorkspace, Run: runKillSelected},
		keys.KeySubmit:   {Precondition: selectedNotBusyNotWorkspace, Run: runSubmitSelected},
		keys.KeyCheckout: {Precondition: selectedNotBusyNotWorkspace, Run: runCheckoutSelected},
		keys.KeyResume:   {Precondition: selectedPausedNotWorkspace, Run: runResumeSelected},
	}
}

func runPromptNewInstance(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}

	// Start a background fetch so branches are up to date by the time
	// the picker opens.
	repoDir := m.repoPath()
	fetchCmd := func() tea.Msg {
		git.FetchBranches(repoDir, nil)
		return nil
	}

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "",
		Path:      repoDir,
		Program:   m.program,
		ConfigDir: m.configDir(),
	})
	if err != nil {
		return m, m.handleError(err)
	}

	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	m.promptAfterName = true

	return m, fetchCmd
}

func runNewInstance(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "",
		Path:      m.repoPath(),
		Program:   m.program,
		ConfigDir: m.configDir(),
	})
	if err != nil {
		return m, m.handleError(err)
	}

	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)

	return m, nil
}

func runKillSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	previousStatus := selected.GetStatus()
	title := selected.Title

	// preAction runs synchronously in the main goroutine when the user
	// confirms. It marks the instance as Deleting immediately.
	preAction := func() {
		if err := selected.TransitionTo(session.Deleting); err != nil {
			log.WarningLog.Printf("kill preAction transition: %v", err)
		}
	}

	// killAction runs in a goroutine — only I/O, no state mutations.
	killAction := func() tea.Msg {
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return transitionFailedMsg{title: title, op: "delete", previousStatus: previousStatus, err: err}
		}

		checkedOut, err := worktree.IsBranchCheckedOut()
		if err != nil {
			return transitionFailedMsg{title: title, op: "delete", previousStatus: previousStatus, err: err}
		}

		if checkedOut {
			return transitionFailedMsg{
				title:          title,
				op:             "delete",
				previousStatus: previousStatus,
				err:            fmt.Errorf("instance %s is currently checked out", selected.Title),
			}
		}

		// Close the cached terminal-pane tmux session. DetachTerminalForInstance
		// is pure state; the blocking Close() runs here, off the update goroutine.
		if ts := m.splitPane.DetachTerminalForInstance(title); ts != nil {
			if err := ts.Close(); err != nil {
				log.ErrorLog.Printf("terminal pane: failed to close session for %s: %v", title, err)
			}
		}

		if err := selected.Kill(); err != nil {
			log.ErrorLog.Printf("could not kill instance: %v", err)
		}

		if err := m.storage.DeleteInstance(selected.Title); err != nil {
			return transitionFailedMsg{title: title, op: "delete", previousStatus: previousStatus, err: err}
		}

		return killInstanceMsg{title: title}
	}

	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	return m, m.confirmTask(message, overlay.ConfirmationTask{
		Sync:  preAction,
		Async: killAction,
	})
}

func runSubmitSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()

	pushAction := func() tea.Msg {
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return err
		}
		if err = worktree.PushChanges(commitMsg, true); err != nil {
			return err
		}
		return nil
	}

	message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
	return m, m.confirmAction(message, pushAction)
}

func runCheckoutSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	previousStatus := selected.GetStatus()
	pauseTitle := selected.Title

	pauseAction := func() tea.Msg {
		// Close the cached terminal-pane tmux session off the update goroutine
		// so subprocess I/O can't stall the UI.
		if ts := m.splitPane.DetachTerminalForInstance(pauseTitle); ts != nil {
			if err := ts.Close(); err != nil {
				log.ErrorLog.Printf("terminal pane: failed to close session for %s: %v", pauseTitle, err)
			}
		}
		saveFunc := func() error {
			return m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
		}
		if err := selected.Pause(saveFunc); err != nil {
			return transitionFailedMsg{title: pauseTitle, op: "pause", previousStatus: previousStatus, err: err}
		}
		return pauseInstanceMsg{title: pauseTitle}
	}

	// Show help screen before confirming pause. Once the user confirms,
	// flip to Loading synchronously so the spinner renders immediately
	// while the blocking commit/tmux/worktree work runs in pauseAction.
	return m.showHelpScreen(helpTypeInstanceCheckout{}, func() tea.Cmd {
		message := fmt.Sprintf("[!] Pause session '%s'?", selected.Title)
		return m.confirmTask(message, overlay.ConfirmationTask{
			Sync: func() {
				if err := selected.TransitionTo(session.Loading); err != nil {
					log.WarningLog.Printf("pause preAction transition: %v", err)
				}
			},
			Async: pauseAction,
		})
	})
}

func runResumeSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()

	// Flip to Loading immediately so the list shows the spinner while
	// Resume's blocking worktree/tmux setup runs in a Cmd goroutine.
	// TransitionTo enforces Paused→Loading atomically, so a concurrent
	// reconcile flip between the precondition check and this write can't
	// leave us starting Resume on a non-Paused instance.
	if err := selected.TransitionTo(session.Loading); err != nil {
		log.WarningLog.Printf("skip resume: %v", err)
		return m, nil
	}
	saveFunc := func() error {
		return m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
	}
	resumeTitle := selected.Title
	resumeCmd := func() tea.Msg {
		if err := selected.Resume(saveFunc); err != nil {
			return transitionFailedMsg{title: resumeTitle, op: "resume", previousStatus: session.Paused, err: err}
		}
		return resumeDoneMsg{}
	}
	return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), resumeCmd)
}
