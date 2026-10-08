package app

import (
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/files"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg is fired after tea.ExecProcess returns from the
// $EDITOR invocation launched by the file-explorer Enter path. The
// error, if any, routes to handleError so the user sees a spawn
// failure (missing binary, non-zero exit) rather than silent loss.
type editorDoneMsg struct{ err error }

// intents.go owns the app-side handlers that back each script
// Intent. Every function here is called from handleScriptIntent in
// app_scripts.go after the matching cs.actions.* primitive enqueues
// its intent and the coroutine yields. The ActionRegistry-era
// precondition predicates live alongside the runXYZ helpers they
// guard — centralizing them here makes it obvious which guard goes
// with which handler.

// selectedNotBusyNotWorkspace gates lifecycle mutations (kill,
// submit, stash): the selected instance must exist, must not be a
// workspace-terminal (no branch/worktree to act on), and must not be
// mid-transition.
func selectedNotBusyNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	// ID 0 is a creation flow's draft row: no session to act on yet.
	if selected == nil || selected.ID == 0 || selected.IsWorkspaceTerminal {
		return false
	}
	s := selected.Status
	return s != session.Loading && s != session.Deleting
}

// selectedResumableNotWorkspace gates the 'r' key: a Paused instance
// (resume → recreate worktree) or a Recoverable orphan (recover → adopt
// the on-disk worktree), never a workspace terminal.
func selectedResumableNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	s := selected.Status
	return s == session.Paused || s == session.Recoverable
}

// selectedPausedNotWorkspace gates the 'R' key (restart with
// different launch options): only a Paused instance qualifies —
// unlike selectedResumableNotWorkspace, Recoverable orphans are
// excluded (they go live only via the explicit recover action, a
// different code path than Resume).
func selectedPausedNotWorkspace(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.IsWorkspaceTerminal {
		return false
	}
	return selected.Status == session.Paused
}

// selectedReadyForInput gates attach/quick-input: the instance must
// exist, have a live tmux pane, and not be mid-lifecycle.
func selectedReadyForInput(m *home) bool {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Paused() || !m.sessionAlive(selected.TmuxSession) {
		return false
	}
	s := selected.Status
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
	if err := m.latchedStorageErr(); err != nil {
		return m, m.handleError(err)
	}

	// Start a background fetch so branches are up to date by the time
	// the picker opens.
	repoDir := m.repoPath()
	fetchCmd := func() tea.Msg {
		git.FetchBranches(repoDir, nil)
		return nil
	}

	m.newDraft("", "", 0)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	m.promptAfterName = true

	// Resolve the base branch alongside the fetch rather than after it —
	// it is a local ref lookup and must not wait on 30s of network.
	return m, tea.Batch(fetchCmd, m.resolveBaseBranchCmd())
}

func runNewInstance(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	if err := m.latchedStorageErr(); err != nil {
		return m, m.handleError(err)
	}
	m.newDraft("", "", 0)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)

	return m, nil
}

func runKillSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	id, title := selected.ID, selected.Title
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	if selected.Status == session.Recoverable {
		message = fmt.Sprintf("[!] Discard recoverable session '%s'? Uncommitted changes are lost; the branch is kept.", selected.Title)
	}
	// The kill is a request (core.Model.Kill): its pre-step (Deleting) and
	// its job both start when the user confirms, and the job reaches the
	// runtime at the end of that Update.
	return m, m.confirmTask(message, overlay.ConfirmationTask{
		Sync: func() { m.core.Kill(id, m.opReq("kill", title)) },
	})
}

// runKillSelectedNoConfirm mirrors runKillSelected but skips the
// confirmation overlay, making the kill request inline. Used by
// cs.actions.kill_selected{confirm=false}.
func runKillSelectedNoConfirm(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	m.core.Kill(selected.ID, m.opReq("kill", selected.Title))
	m.syncViews() // the Deleting it wrote, for the rest of this Update
	return m, nil
}

func runSubmitSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	id, title := selected.ID, selected.Title
	message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
	return m, m.confirmTask(message, overlay.ConfirmationTask{
		Sync: func() { m.core.Push(id, m.opReq("push", title)) },
	})
}

// runSubmitSelectedNoConfirm mirrors runSubmitSelected but skips the
// confirmation overlay. Used by cs.actions.push_selected{confirm=false}.
func runSubmitSelectedNoConfirm(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	m.core.Push(selected.ID, m.opReq("push", selected.Title))
	return m, nil
}

// runStashSelectedOpts is the parameterized pause path. confirm
// gates the confirmation overlay; help gates the prerequisite help
// screen. Script callers use cs.actions.stash_selected{confirm=,
// help=} to tune either. Combinations that skip the confirm still
// trigger the Loading transition synchronously so the spinner
// renders immediately.
func runStashSelectedOpts(m *home, confirm, help bool) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	id, title := selected.ID, selected.Title

	// The pause is a request (core.Model.Pause), which moves the session
	// to Loading itself.
	startPause := func() tea.Cmd {
		if !confirm {
			m.core.Pause(id, m.opReq("pause", title))
			m.syncViews() // the Loading it wrote, for the rest of this Update
			return nil
		}
		message := fmt.Sprintf("[!] Pause session '%s'?", selected.Title)
		return m.confirmTask(message, overlay.ConfirmationTask{
			Sync: func() { m.core.Pause(id, m.opReq("pause", title)) },
		})
	}

	if help {
		return m.showHelpScreen(helpTypeInstanceStash{}, startPause)
	}
	return m, startPause()
}

func runResumeSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()

	// Flip to Loading immediately (core.Model.Resume) so the list shows
	// the spinner while Resume's blocking worktree/tmux setup runs in a
	// Cmd goroutine. The request refuses a session that is no longer
	// Paused (its precondition), so a concurrent reconcile flip between
	// the key's gate and this request can't start Resume on a non-Paused
	// instance.
	m.core.Resume(selected.ID, m.opReq("resume", selected.Title))
	m.syncViews() // the Loading it wrote, for instanceChanged
	return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged())
}

// runResumeOrRecover routes the 'r' key: Recoverable orphans are adopted
// (recover), Paused instances are resumed.
func runResumeOrRecover(m *home) (tea.Model, tea.Cmd) {
	if m.list.GetSelectedInstance().Status == session.Recoverable {
		return runRecoverSelected(m)
	}
	return runResumeSelected(m)
}

// runRestartWithOptionsSelected opens the Session Launch Options modal
// for the selected Paused instance, seeded from its current
// (reverse-parsed) launch options. Confirming re-composes Program
// against the recovered base program and resumes through the same
// Loading-transition/Resume/save-checkpoint shape runResumeSelected
// uses directly; canceling leaves the instance Paused and untouched
// (pendingLaunchOptionsCancel, not the creation flow's discard of its
// draft).
func runRestartWithOptionsSelected(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	opts, base := launch.Parse(selected.Program)
	// HeadroomProxy/CacheTTL1h are never baked into the program (see
	// session.HeadroomProxyEnv/CacheTTL1hEnv) — launch.Parse can't
	// recover them, so seed them from the instance's own settings instead.
	opts.HeadroomProxy = selected.HeadroomProxy
	opts.CacheTTL1h = selected.CacheTTL1h
	// The account never reaches the program either; seed it from the
	// instance so R preselects the session's own account.
	opts.Account = accountOrDefault(selected.Account)

	id := selected.ID
	m.pendingLaunchOptions = func(newOpts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		// The session's row as it is now: it may have gone while the
		// modal was open.
		row, _ := m.viewByID(id)
		if row == nil {
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, nil
		}
		// The resume is a request (core.Model.ResumeWith): it records the
		// chosen options, recomposing the program from base, moves the
		// session to Loading and starts its job, all when the user
		// confirms.
		resumeTask := overlay.ConfirmationTask{
			Sync: func() {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				m.core.ResumeWith(id, newOpts, base, m.opReq("resume", row.Title))
			},
			Async: tea.RequestWindowSize,
		}
		if m.remoteControlBlockedOn(newOpts.Account, launch.EffectiveRemoteControl(newOpts), row.Program) {
			return m, m.promptRestartRemoteControlBlocked(resumeTask, m.core.RCAuthFor(newOpts.Account).Reason)
		}
		return m, tea.Batch(m.runTask(resumeTask), m.instanceChanged())
	}
	m.pendingLaunchOptionsCancel = func() (tea.Model, tea.Cmd) {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, nil
	}
	m.state = stateLaunchOptions
	lo, reloaded := m.newLaunchOptionsOverlay(opts, base)
	// The branch already exists, so the prefix row shows it read-only rather
	// than implying a rename that restarting cannot perform.
	lo.SetBranchPrefixLocked(selected.Branch)
	m.setOverlay(lo, overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)
	m.core.RequestUsageProbe()
	return m, tea.Batch(tea.RequestWindowSize, reloaded)
}

// runRecoverSelected adopts the selected Recoverable orphan: core's
// Recover flips it to Loading for the spinner and queues the job running
// ReconcileAndRestore (which adopts the existing worktree and spawns tmux)
// off the UI goroutine. The list swap + persist happen when the model
// delivers its result, on the main goroutine.
func runRecoverSelected(m *home) (tea.Model, tea.Cmd) {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return m, nil
	}
	m.core.Recover(sel.ID, m.opReq("recover", sel.Title))
	m.syncViews() // the Loading it wrote, for instanceChanged
	return m, m.instanceChanged()
}

// -- Attach --

func runInlineAttachAgent(m *home) (tea.Model, tea.Cmd) {
	// Keys typed while a prompt send to the session is still landing
	// would go between its paste and its Enter (sendingTo says so).
	if m.sendingTo(m.list.GetSelectedInstance()) {
		return m, nil
	}
	// If the pane is scrolled back, drop to live tail first — otherwise
	// the user's keystrokes go to live tmux while they're still looking
	// at scrolled-back history. ResetAgentToNormalMode is nil/Paused-safe
	// (preview.go:325) and a no-op when not scrolling.
	if err := m.splitPane.ResetAgentToNormalMode(m.list.GetSelectedInstance()); err != nil {
		log.For("app").Info("inline_attach.reset_agent_failed", "err", err)
	}
	m.setPaneFocus(ui.FocusAgent)
	m.splitPane.SetInlineAttach(true)
	m.state = stateInlineAttach
	m.menu.SetState(ui.StateInlineAttach)
	return m, tea.RequestWindowSize
}

// ensureTerminalVisible unhides the terminal pane before a
// terminal-targeting intent runs: with the pane hidden these intents
// would drop the user into an interact/input mode with zero visual
// feedback (typing into an invisible pane). The user asked for the
// terminal — show it. Re-layout rides the tea.RequestWindowSize the
// callers already return.
func (m *home) ensureTerminalVisible() {
	// Workbench mode: the terminal IS visible — in the panel's terminal
	// tab (handleWorkbenchKey switches to it before dispatching). The
	// split terminal is force-hidden by design (two places, one pane);
	// un-hiding it here would break that layout AND durably persist
	// TerminalHidden=false over the user's focus-mode pref.
	if m.viewMode == viewWorkbench {
		return
	}
	if !m.splitPane.IsTerminalHidden() {
		return
	}
	m.splitPane.SetTerminalHidden(false)
	m.mutateUIPrefs(func(p *config.UIPrefs) { p.TerminalHidden = false })
}

func runInlineAttachTerminal(m *home) (tea.Model, tea.Cmd) {
	m.ensureTerminalVisible()
	m.splitPane.ResetTerminalToNormalMode()
	m.setPaneFocus(ui.FocusTerminal)
	m.splitPane.SetInlineAttach(true)
	m.state = stateInlineAttach
	m.menu.SetState(ui.StateInlineAttach)
	return m, tea.RequestWindowSize
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
	// A second send would land between the first's paste and its Enter
	// (sendingTo says so).
	if m.sendingTo(m.list.GetSelectedInstance()) {
		return m, nil
	}
	m.state = stateQuickInteract
	m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetAgent)
	m.menu.SetState(ui.StateQuickInteract)
	return m, tea.RequestWindowSize
}

func runQuickInputTerminal(m *home) (tea.Model, tea.Cmd) {
	// See ensureTerminalVisible — the user is sending text to the
	// terminal, so it must be on screen.
	m.ensureTerminalVisible()
	m.state = stateQuickInteract
	m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetTerminal)
	m.menu.SetState(ui.StateQuickInteract)
	return m, tea.RequestWindowSize
}

// -- Help & workspace --

func runShowHelp(m *home) (tea.Model, tea.Cmd) {
	return m.showHelpScreen(helpTypeGeneral{}, nil)
}

func runOpenWorkspacePicker(m *home) (tea.Model, tea.Cmd) {
	// The model rereads the registry first: another process (`loom
	// workspace add`, a second loom) may have registered one since.
	if err := m.core.ReloadRegistry(); err != nil {
		return m, m.handleError(fmt.Errorf("failed to load workspace registry: %w", err))
	}
	registry := m.core.Registry()
	if len(registry.Workspaces) == 0 {
		return m, m.handleError(fmt.Errorf("no workspaces registered"))
	}
	activeNames := m.pickerActiveNames()
	// allowGlobal=true gives the user a single-keystroke return to global
	// mode without quitting. Required to round-trip the global ↔
	// workspace transition that applyWorkspaceToggle now handles.
	picker := overlay.NewWorkspacePicker(registry.Workspaces, activeNames, true)
	// Restore failures stay checked so their live sessions survive; the
	// picker warns that closing one gives them up to the next launch's
	// orphan sweep.
	picker.MarkFailedToLoad(m.failedOpen...)
	m.setOverlay(picker, overlayWorkspacePicker)
	m.state = stateWorkspace
	return m, nil
}

// pickerActiveNames returns the set of workspace names shown pre-checked
// in the workspace picker: the open (tab) slots, plus the ones that failed
// to restore — still open as far as the user is concerned, so leaving one
// checked retries it and unchecking it is the explicit close.
func (m *home) pickerActiveNames() map[string]bool {
	active := make(map[string]bool, len(m.slots))
	for _, name := range m.openList() {
		active[name] = true
	}
	return active
}

// runOpenSettings opens the settings overlay over the active workspace's
// config. authBlocked/authReason are passed as plain values (not
// session.RemoteControlAuth) to keep ui/overlay decoupled from session.
func runOpenSettings(m *home) (tea.Model, tea.Cmd) {
	if m.id == 0 {
		return m, m.handleError(fmt.Errorf("no configuration loaded"))
	}
	m.core.ReloadAccounts()
	reloaded := m.drainCore()
	// The overlay edits a config of its own, built from the workspace's
	// published settings; each change goes back to the model as a
	// SaveSettings request (handleStateSettingsKey).
	m.settingsEdit = config.FromSettings(m.settings())
	so := overlay.NewSettingsOverlay(m.settingsEdit, m.core.RCAuth().Blocked(), m.core.RCAuth().Reason)
	so.SetAccountRows(m.accountRows(m.accountStatuses()))
	so.SetAccountNotice(m.accountsScreenNotice())
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings
	m.core.RequestUsageProbe()
	return m, reloaded
}

// -- File explorer --

// runToggleFileExplorer opens the file-explorer overlay scoped to the
// selected instance's worktree (or the workspace repo path when the
// selection is a workspace terminal or missing). The overlay replaces
// the right pane inline so the sessions list stays visible.
func runToggleFileExplorer(m *home) (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()

	var root string
	switch {
	case selected == nil:
		root = m.repoPath()
	case selected.IsWorkspaceTerminal:
		root = selected.WorktreePath
		if root == "" {
			root = m.repoPath()
		}
	default:
		root = selected.WorktreePath
		if root == "" {
			return m, m.handleError(fmt.Errorf("instance not ready"))
		}
	}
	if root == "" {
		return m, m.handleError(fmt.Errorf("no repository to explore"))
	}

	result, err := files.List(root, cmd2.MakeExecutor())
	if err != nil {
		return m, m.handleError(fmt.Errorf("list files: %w", err))
	}

	ov := overlay.NewFileExplorerOverlay(root, result.Paths, openInEditorCmd)
	m.setOverlay(ov, overlayFileExplorer)
	m.state = stateFileExplorer
	return m, tea.RequestWindowSize
}

// openInEditorCmd launches $EDITOR on absPath via tea.ExecProcess so
// the editor owns the terminal for the duration of the edit.
// Whitespace in the EDITOR value splits flag arguments (e.g.
// "code --wait"); paths with embedded spaces aren't supported — the
// workaround is a symlink. Errors return through editorDoneMsg.
func openInEditorCmd(absPath string) tea.Cmd {
	parts := strings.Fields(resolveEditor())
	if len(parts) == 0 {
		return func() tea.Msg {
			return editorDoneMsg{err: fmt.Errorf("no editor configured")}
		}
	}
	args := append(parts[1:], absPath)
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorDoneMsg{err: err}
	})
}

// resolveEditor picks the editor binary from the usual env vars.
// VISUAL wins over EDITOR because by tradition VISUAL names a
// full-screen editor while EDITOR can be a line editor (ed, ex);
// when Loom suspends the TUI it always has a full-screen terminal to
// hand over. Falls back to notepad on Windows and vi elsewhere.
func resolveEditor() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// -- Merge --

// runMergeSelected opens the merge-picker overlay for the currently
// focused session, following the same precondition-then-open shape as
// runOpenWorkspacePicker. Reuses selectedNotBusyNotWorkspace — the same
// gate push/kill/stash already use — rather than inventing bespoke
// eligibility rules; see
// docs/superpowers/specs/2026-07-01-git-merge-hotkey-design.md.
func runMergeSelected(m *home) (tea.Model, tea.Cmd) {
	if !selectedNotBusyNotWorkspace(m) {
		return m, m.handleError(fmt.Errorf("no session selected, or the selection can't be merged into"))
	}
	target := m.list.GetSelectedInstance()
	if !target.Started {
		return m, m.handleError(fmt.Errorf("merge: cannot get git worktree for instance that has not been started"))
	}

	// The dirty check reads only the worktree directory, which the view
	// carries. It still runs git on the Update goroutine, as it always
	// has (a follow-up for the model's own goroutine, stage 1D).
	worktree := git.NewGitWorktreeFromStorage(target.RepoPath, target.WorktreePath, target.TmuxSession, target.Branch, "", false, "")
	dirty, err := worktree.IsDirty()
	if err != nil {
		return m, m.handleError(fmt.Errorf("merge: failed to check worktree status: %w", err))
	}
	if dirty {
		return m, m.handleError(fmt.Errorf("session '%s' has uncommitted changes — commit or stash before merging", target.Title))
	}

	rows := mergeSourceRows(m.list.GetInstances(), target)
	if len(rows) == 0 {
		return m, m.handleError(fmt.Errorf("no other sessions available to merge into '%s'", target.Title))
	}

	m.pendingMergeTarget = target
	m.pendingMergeSourceItems = make([]core.InstanceView, len(m.list.GetInstances()))
	copy(m.pendingMergeSourceItems, m.list.GetInstances())

	m.setOverlay(overlay.NewMergePicker(target.Title, rows), overlayMergePicker)
	m.state = stateMergePicker
	return m, nil
}

// mergeSourceRows builds the picker's row list: every instance in
// items passing the same eligibility filter as the target
// (selectedNotBusyNotWorkspace's rules, applied per-instance) except
// the target itself, labeled with its original position in items
// (ui.DisplayIndex) so a typed digit matches what's on-screen in the
// main list.
func mergeSourceRows(items []core.InstanceView, target *core.InstanceView) []overlay.MergePickerRow {
	var rows []overlay.MergePickerRow
	for i, inst := range items {
		// ID 0 is a creation flow's draft row, which has no branch yet.
		if inst.ID == 0 || inst.ID == target.ID || inst.IsWorkspaceTerminal {
			continue
		}
		status := inst.Status
		if status == session.Loading || status == session.Deleting {
			continue
		}
		rows = append(rows, overlay.MergePickerRow{
			Index:  ui.DisplayIndex(items, i),
			Title:  inst.Title,
			Branch: inst.Branch,
			Status: status.String(),
		})
	}
	return rows
}

// instanceByDisplayIndex returns the instance whose ui.DisplayIndex
// position (the same number rendered in the main list) matches idx, or
// nil if none matches. Called against m.pendingMergeSourceItems — a
// snapshot frozen at the moment the picker opened, not live m.list data
// — so a background message reassigning m.list's selection while the
// picker is open can't redirect the merge to a different session (see
// runMergeSelected). The trade-off: if a source instance is killed or
// otherwise removed from m.list between the picker opening and commit,
// this can still resolve it from the snapshot; the commit then re-resolves
// both sides by ID and fails with a surfaced error (the session is gone,
// see handleStateMergePickerKey) rather than silently succeeding or
// corrupting anything. The model's Merge request re-checks the rest of
// the precondition (neither side busy), and refuses a merge whose source
// or target became busy meanwhile.
func instanceByDisplayIndex(items []core.InstanceView, idx int) *core.InstanceView {
	for i := range items {
		if ui.DisplayIndex(items, i) == idx {
			return &items[i]
		}
	}
	return nil
}
