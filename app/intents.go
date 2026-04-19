package app

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// intents.go owns the app-side handlers that back each script
// Intent. Every function here is called from handleScriptIntent in
// app_scripts.go after the matching cs.actions.* primitive enqueues
// its intent and the coroutine yields. The ActionRegistry-era
// precondition predicates live alongside the runXYZ helpers they
// guard — centralizing them here makes it obvious which guard goes
// with which handler.

// selectedNotBusyNotWorkspace gates lifecycle mutations (kill,
// submit, checkout): the selected instance must exist, must not be a
// workspace-terminal (no branch/worktree to act on), and must not be
// mid-transition.
func selectedNotBusyNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	s := selected.GetStatus()
	return s != session.Loading && s != session.Deleting
}

// selectedPausedNotWorkspace gates resume: only a paused,
// non-workspace instance has a branch waiting to be checked back out.
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

// selectedReadyForInputNotWorkspace adds the "not a workspace
// terminal" constraint required by full-screen attach, which would
// otherwise take over the main repo shell.
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

// -- Lifecycle --

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
	preAction, killAction := killActionFor(m, selected)
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	return m, m.confirmTask(message, overlay.ConfirmationTask{
		Sync:  preAction,
		Async: killAction,
	})
}

// runKillSelectedNoConfirm mirrors runKillSelected but skips the
// confirmation overlay, running preAction inline before returning
// killAction. Used by cs.actions.kill_selected{confirm=false}.
func runKillSelectedNoConfirm(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	preAction, killAction := killActionFor(m, selected)
	preAction()
	return m, killAction
}

// killActionFor returns the (synchronous pre-step, async body) pair
// that both runKillSelected variants share. preAction flips the
// instance to Deleting; killAction handles I/O off the update
// goroutine and returns the appropriate tea.Msg on completion.
func killActionFor(m *home, selected *session.Instance) (func(), tea.Cmd) {
	previousStatus := selected.GetStatus()
	title := selected.Title

	preAction := func() {
		if err := selected.TransitionTo(session.Deleting); err != nil {
			log.For("app").Warn("kill.preaction_transition_failed", "err", err)
		}
	}

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

		if ts := m.splitPane.DetachTerminalForInstance(title); ts != nil {
			if err := ts.Close(); err != nil {
				log.For("app").Error("kill.terminal_close_failed", "title", title, "err", err)
			}
		}

		if err := selected.Kill(); err != nil {
			log.For("app").Error("kill.instance_kill_failed", "title", title, "err", err)
		}

		if err := m.storage.DeleteInstance(selected.Title); err != nil {
			return transitionFailedMsg{title: title, op: "delete", previousStatus: previousStatus, err: err}
		}

		return killInstanceMsg{title: title}
	}

	return preAction, killAction
}

func runSubmitSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	pushAction := pushActionFor(selected)
	message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
	return m, m.confirmAction(message, pushAction)
}

// runSubmitSelectedNoConfirm mirrors runSubmitSelected but skips the
// confirmation overlay. Used by cs.actions.push_selected{confirm=false}.
func runSubmitSelectedNoConfirm(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	return m, pushActionFor(selected)
}

func pushActionFor(selected *session.Instance) tea.Cmd {
	return func() tea.Msg {
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
}

// runCheckoutSelectedOpts is the parameterized pause path. confirm
// gates the confirmation overlay; help gates the prerequisite help
// screen. Script callers use cs.actions.checkout_selected{confirm=,
// help=} to tune either. Combinations that skip the confirm still
// trigger the Loading transition synchronously so the spinner
// renders immediately.
func runCheckoutSelectedOpts(m *home, confirm, help bool) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	pauseAction := pauseActionFor(m, selected)

	startPause := func() tea.Cmd {
		if !confirm {
			if err := selected.TransitionTo(session.Loading); err != nil {
				log.For("app").Warn("pause.preaction_transition_failed", "err", err)
			}
			return pauseAction
		}
		message := fmt.Sprintf("[!] Pause session '%s'?", selected.Title)
		return m.confirmTask(message, overlay.ConfirmationTask{
			Sync: func() {
				if err := selected.TransitionTo(session.Loading); err != nil {
					log.For("app").Warn("pause.preaction_transition_failed", "err", err)
				}
			},
			Async: pauseAction,
		})
	}

	if help {
		return m.showHelpScreen(helpTypeInstanceCheckout{}, startPause)
	}
	return m, startPause()
}

func pauseActionFor(m *home, selected *session.Instance) tea.Cmd {
	previousStatus := selected.GetStatus()
	pauseTitle := selected.Title
	return func() tea.Msg {
		if ts := m.splitPane.DetachTerminalForInstance(pauseTitle); ts != nil {
			if err := ts.Close(); err != nil {
				log.For("app").Error("pause.terminal_close_failed", "title", pauseTitle, "err", err)
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
}

func runResumeSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()

	// Flip to Loading immediately so the list shows the spinner while
	// Resume's blocking worktree/tmux setup runs in a Cmd goroutine.
	// TransitionTo enforces Paused→Loading atomically, so a concurrent
	// reconcile flip between the precondition check and this write can't
	// leave us starting Resume on a non-Paused instance.
	if err := selected.TransitionTo(session.Loading); err != nil {
		log.For("app").Warn("resume.skipped", "err", err)
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

// -- Attach --

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

// -- Quick input --

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

// -- Help & workspace --

func runShowHelp(m *home) (tea.Model, tea.Cmd) {
	return m.showHelpScreen(helpTypeGeneral{}, nil)
}

func runOpenWorkspacePicker(m *home) (tea.Model, tea.Cmd) {
	registry, err := config.LoadWorkspaceRegistry()
	if err != nil {
		return m, m.handleError(fmt.Errorf("failed to load workspace registry: %w", err))
	}
	if len(registry.Workspaces) == 0 {
		return m, m.handleError(fmt.Errorf("no workspaces registered"))
	}
	activeNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		activeNames[slot.wsCtx.Name] = true
	}
	m.setOverlay(overlay.NewWorkspacePicker(registry.Workspaces, activeNames), overlayWorkspacePicker)
	m.state = stateWorkspace
	return m, nil
}
