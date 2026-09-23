package app

import (
	"context"
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"path/filepath"
	"slices"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// scriptShutdownTimeout bounds how long quit waits for the script engine
// to drain and close (see script.Engine.Shutdown).
const scriptShutdownTimeout = 1500 * time.Millisecond

// Run starts the Bubble Tea program and blocks until the user quits or
// ctx is cancelled. It wires the home model, installs a shutdown hook
// that drains suspended Lua coroutines, and swallows no errors — a
// non-nil return means tea.Program.Run failed.
//
// Parameters:
//   - wsCtx is the resolved workspace context; nil falls back to the
//     global config directory.
//   - registry is the workspace registry for the startup workspace picker.
//   - appConfig is the pre-loaded config from the resolved workspace dir.
//   - program overrides the default agent command for new instances
//     (empty string uses appConfig.GetProgram()).
//   - pendingDir is an optional directory to seed the new-instance
//     overlay with (used by `loom` invoked from a non-workspace dir).
//   - noScripts disables loading user scripts from ~/.loom/scripts;
//     embedded defaults still load so core keybindings work.
func Run(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, pendingDir string, noScripts bool) error {
	// Activate the configured theme before any component renders.
	// Package-init styles are theme-hooked (ui.RegisterThemeHook), so
	// this rebuild-on-apply is what makes config-selected themes stick.
	themeName := ""
	if appConfig != nil {
		themeName = appConfig.GetTheme()
	}
	if !ui.ApplyTheme(themeName) && themeName != "" {
		log.For("ui").Warn("unknown_theme", "name", themeName, "fallback", ui.DefaultThemeName)
	}
	h, err := newHome(ctx, wsCtx, registry, appConfig, program, pendingDir, noScripts)
	if err != nil {
		return err
	}
	// Shutdown hook: drain any suspended script coroutines then close
	// the Lua state. The engine's "every coroutine gets resumed" contract
	// would otherwise be violated on process exit — including on the
	// QuitIntent path where tea.Batch does not sequence scriptResumeMsg
	// before tea.QuitMsg, so the awaiting coroutine can be stranded.
	// Bounded: a handler still running (a Lua loop, or slow Go work such
	// as inst:pause()) must not leave the terminal hanging after the TUI
	// has torn down.
	defer func() {
		if h.scripts != nil {
			h.scripts.Shutdown(scriptShutdownTimeout)
		}
	}()
	p := tea.NewProgram(h) // alt-screen + mouse mode are set on the tea.View (see View())
	// Pane events: the output pumps push dirty/quiet/bell/dead into the
	// program from their own goroutines; Send is goroutine-safe by design.
	// Torn down before Run returns so a late timer can't Send into a dead
	// program (Send after Kill is a no-op, but keep the lifecycle explicit).
	tmux.SetNotifier(tmux.Notifier{
		Output: func(s string) { p.Send(paneDirtyMsg{session: s}) },
		Quiet:  func(s string) { p.Send(paneQuietMsg{session: s}) },
		Bell:   func(s string) { p.Send(bellMsg{session: s}) },
		Dead:   func(s string) { p.Send(ptyDeadMsg{session: s}) },
	})
	defer tmux.SetNotifier(tmux.Notifier{})
	_, err = p.Run()
	return err
}

func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, pendingDir string, noScripts bool) (*home, error) {
	cfgDir := ""
	if wsCtx != nil {
		cfgDir = wsCtx.ConfigDir
	}

	// Loom-context injection: establish the global enabled flag and write
	// the config-dir prompt files at process startup, covering BOTH the
	// classic/global path (this function) and the multi-tab slots path
	// (activateWorkspace re-syncs per workspace). Without this, a
	// classic-path launch (single-tab `loom --workspace`, or bare `loom`)
	// would never init the flag and the feature would be inert.
	session.SetLoomContextEnabled(appConfig.LoomContextEnabled())
	session.SetSubagentTrackingEnabled(appConfig.SubagentTrackingEnabled())
	if err := session.WriteLoomContextFiles(cfgDir); err != nil {
		log.For("app").Warn("loom_context.write_failed", "err", err.Error())
	}

	appState := config.LoadStateFrom(cfgDir)

	storage, err := session.NewStorage(appState, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}

	// The classic slot: the startup context's state, focused until (and
	// unless) a workspace tab opens. On the restore path its storage is
	// never loaded unless no workspace activates (loadStartupStorageFallback).
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	h := &home{
		ctx: ctx,
		workspaceSlot: &workspaceSlot{
			wsCtx:     wsCtx,
			storage:   storage,
			appConfig: appConfig,
			appState:  appState,
			splitPane: sp,
			workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
		},
		registry:    registry,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:        ui.NewMenu(),
		overview:    ui.NewOverview(),
		errBox:      ui.NewErrBox(),
		program:     program,
		state:       stateDefault,
		tabBar:      ui.NewWorkspaceTabBar(),
		skipScripts: noScripts,
		hostFocused: true,
	}
	// Built after h so the list can point at h.spinner.
	h.list = ui.NewList(&h.spinner)
	if wsCtx != nil && wsCtx.Name != "" {
		h.list.SetWorkspaceName(wsCtx.Name)
	}

	// Initialize the script engine and load user scripts. Errors are
	// logged but never propagated — a broken script must not block
	// startup of the TUI.
	initScripts(h)

	// Determine whether we'll restore a saved multi-tab set. If so, skip the
	// classic-mode load below: activateWorkspace() will load each slot fresh,
	// and doing both would re-attach tmux ptmx handles for the same sessions.
	var savedOpen []config.Workspace
	if registry != nil {
		savedOpen = registry.GetOpenWorkspaces()
	}
	willRestoreSlots := len(savedOpen) > 0 && pendingDir == ""

	cmdExec := cmd2.MakeExecutor()
	// Probe Claude auth once up front (before any workspace terminal is
	// created) so remote-control launch decisions are synchronous and
	// startup terminals aren't stripped of the flag by fail-closed timing.
	if appConfig != nil && appConfig.RemoteControlEnabled() {
		h.rcAuth = session.DetectClaudeRemoteControlAuth(program, cmdExec)
	}
	var startupRecovery recoverySummary
	if !willRestoreSlots {
		recovery, err := h.loadStartupStorage(cmdExec, true)
		if err != nil {
			return nil, fmt.Errorf("load instances: %w", err)
		}
		startupRecovery = recovery
	}

	if willRestoreSlots {
		h.restoreSavedWorkspaces(savedOpen)
	}

	// Capture the deferred startup-overlay decision in a closure so the
	// orphan-recovery handler can run it AFTER the user commits. Without
	// this hand-off, opening the orphan overlay would shadow pendingDir
	// confirmation and the startup workspace picker — users would
	// silently land in the default state with the registration prompt
	// skipped.
	registerNextOverlay := func() {
		if pendingDir != "" {
			name := filepath.Base(pendingDir)
			h.pendingDir = pendingDir
			confirm := overlay.NewConfirmationOverlay(
				fmt.Sprintf("Register '%s' as workspace '%s'?", pendingDir, name))
			confirm.SetWidth(60)
			h.state = stateConfirm
			h.pendingConfirmation = overlay.ConfirmationTask{
				// Only the request crosses the Cmd: the registry has no
				// lock and Update reads and writes it, so Update runs the
				// Add when registerWorkspaceMsg lands.
				Async: func() tea.Msg {
					return registerWorkspaceMsg{name: name, dir: pendingDir}
				},
			}
			confirm.OnCancel = func() {
				h.pendingConfirmation = overlay.ConfirmationTask{}
				if h.registry != nil && len(h.registry.Workspaces) > 0 {
					h.setOverlay(overlay.NewStartupWorkspacePicker(h.registry.Workspaces), overlayWorkspacePickerStartup)
					h.state = stateWorkspace
				}
			}
			h.setOverlay(confirm, overlayConfirmation)
			return
		}
		if !willRestoreSlots && wsCtx != nil && wsCtx.Name == "" && registry != nil && len(registry.Workspaces) > 0 {
			h.setOverlay(overlay.NewStartupWorkspacePicker(registry.Workspaces), overlayWorkspacePickerStartup)
			h.state = stateWorkspace
		}
	}

	// Orphans are handled inline by reconcileOrphans, so nothing preempts
	// the startup overlay chain (workspace registration confirm / picker).
	registerNextOverlay()

	h.showRecoverySummary(startupRecovery)

	// Apply persisted layout prefs on every startup path. The slot-restore
	// path already applied them via loadSlot — re-applying is idempotent.
	// lastWidth is still 0 here, so this only sets the flags; the first
	// WindowSizeMsg lays out honoring them.
	h.applyUIPrefs()
	return h, nil
}

// loadStartupStorage loads the startup storage (m.storage, for m.wsCtx)
// into the focused list with classic-startup semantics (loadSlotStorage).
// Classic startup runs it directly; restoreSavedWorkspaces runs it as the
// fallback when no workspace could be restored. sweepTmux adds the
// server-wide orphan tmux sweep — the fallback passes false, because the
// workspaces that failed to load still have live sessions whose titles it
// cannot read.
func (m *home) loadStartupStorage(cmdExec cmd2.Executor, sweepTmux bool) (recoverySummary, error) {
	cfgDir := ""
	if m.wsCtx != nil {
		cfgDir = m.wsCtx.ConfigDir
	}
	return m.loadSlotStorage(m.workspaceSlot, cfgDir, cmdExec, sweepTmux)
}

// loadSlotStorage loads slot's storage into slot's (empty) list with
// startup semantics: LoadAndReconcile, crash-restart, inline orphan
// recovery, then the workspace-terminal auto-create for a workspace
// context. cfgDir is the directory slot's storage lives in. sweepTmux adds
// the server-wide orphan tmux sweep (see loadStartupStorage). A load error
// is returned before anything is added to the list; the storage's write
// latch is then engaged, so nothing can overwrite the unreadable payload.
// Used by startup (the focused classic slot) and enterGlobalMode (the
// global slot it is about to focus).
func (m *home) loadSlotStorage(slot *workspaceSlot, cfgDir string, cmdExec cmd2.Executor, sweepTmux bool) (recoverySummary, error) {
	storage := slot.storage
	wsCtx := slot.wsCtx

	// LoadAndReconcile centralizes RenameLegacySessions + per-record
	// reconcile, and on a per-record failure stashes the raw data in
	// storage.unrecovered so the next SaveInstances preserves it.
	// The inline loop this replaced silently dropped failures.
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		return recoverySummary{}, err
	}

	hasWorkspaceTerminal := false
	for _, instance := range instances {
		if instance.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		slot.list.AddInstance(instance)
	}

	// Restart crash-recovered instances
	for _, inst := range slot.list.GetInstances() {
		if !inst.CrashRecovered() {
			continue
		}
		if err := inst.CrashRestart(); err != nil {
			log.For("app").Error("crash_recovery.restart_failed", "title", inst.Title, "err", err)
			if tErr := inst.TransitionTo(session.Paused); tErr != nil {
				log.For("app").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr.Error())
			}
		}
		inst.SetCrashRecovered(false)
	}

	// Discover orphan worktrees (on disk but not in state.json),
	// auto-clean stale leftovers, and add inline Recoverable entries
	// for any with unsaved work or a live agent. Runs before
	// CleanupOrphanedSessions so a live recoverable's tmux (now a
	// list instance) is exempted by the claimedTitles loop below.
	recovery := m.reconcileOrphans(cfgDir, m.program, slot.list, storage, cmdExec)

	// Clean up orphaned tmux sessions from previous crashes, sparing
	// those of records preserved on disk outside the list.
	if sweepTmux {
		claimedTitles := make(map[string]bool)
		claimTitles(claimedTitles, slot.list, storage)
		if err := session.CleanupOrphanedSessions(claimedTitles, cmdExec); err != nil {
			log.For("app").Error("orphan_cleanup_failed", "err", err)
		}
	}

	// Auto-create workspace terminal if in a workspace context and none
	// exists — unless a record storage preserves but could not load
	// already owns the title (see activateWorkspace).
	wtTitle := "Workspace Terminal"
	if wsCtx != nil && wsCtx.Name != "" {
		wtTitle = wsCtx.Name
	}
	if !hasWorkspaceTerminal && wsCtx != nil && wsCtx.RepoPath != "" && !slices.Contains(storage.PreservedTitles(), wtTitle) {
		wtOpts := launchOptionsFromConfig(slot.appConfig)
		if m.remoteControlBlocked(effectiveRemoteControl(wtOpts), m.program) {
			// Non-interactive startup: fall back silently but leave an
			// info-style note (clears on the next status update).
			m.errBox.SetInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             applyLaunchOptions(wtOpts, m.rcAuth, m.program, wtTitle),
			HeadroomProxy:       wtOpts.HeadroomProxy,
			CacheTTL1h:          wtOpts.CacheTTL1h,
			IsWorkspaceTerminal: true,
			ConfigDir:           cfgDir,
		})
		if wtErr != nil {
			log.For("app").Error("workspace_terminal.create_failed", "err", wtErr)
		} else {
			slot.list.AddInstance(wtInstance)
			if err := wtInstance.Start(true); err != nil {
				log.For("app").Error("workspace_terminal.start_failed", "err", err)
			}
		}
	}
	return recovery, nil
}

// restoreSavedWorkspaces activates all workspaces in `saved` as slots, merging
// the explicit startup target (if any) into the set, then focuses the
// appropriate slot. Missing/failed workspaces are not opened (failures
// are logged, and any failure skips the server-wide orphan sweep); if none
// activates, the startup storage is loaded instead
// (loadStartupStorageFallback). The registry's OpenWorkspaces list is
// rewritten to what activated plus the saved workspaces that failed
// (restoreFailed): those keep their place so a later launch retries them
// rather than sweeping their live sessions.
func (m *home) restoreSavedWorkspaces(saved []config.Workspace) {
	explicit := ""
	if m.wsCtx != nil {
		explicit = m.wsCtx.Name
	}

	desired := saved
	if explicit != "" && m.registry != nil {
		found := false
		for _, w := range desired {
			if w.Name == explicit {
				found = true
				break
			}
		}
		if !found {
			if ws := m.registry.Get(explicit); ws != nil {
				desired = append(desired, *ws)
			}
		}
	}

	var failed []string
	for _, ws := range desired {
		release, err := m.activateWorkspace(ws)
		if err != nil {
			log.For("app").Error("workspace.restore_failed", "name", ws.Name, "err", err)
			failed = append(failed, ws.Name)
			if slices.ContainsFunc(saved, func(s config.Workspace) bool { return s.Name == ws.Name }) {
				// Was open: keep it open, to be retried (restoreFailed).
				m.restoreFailed = append(m.restoreFailed, ws.Name)
			}
		}
		// The first tab drops the classic slot, which this path never
		// loaded, so release is nil in practice. Were it not, running it
		// here is safe: the program is not running yet (Run installs the
		// pane notifier after newHome), so no pump can block on Send.
		if release != nil {
			release()
		}
	}

	// Sweep orphan tmux sessions left by prior crashes. The classic
	// startup path does this inline in activateWorkspace's caller; the
	// multi-tab restore path historically did not, so stale
	// loom_*/claudesquad_* sessions accumulated across restarts. Each
	// slot's activateWorkspace call above already ran reconcileOrphans,
	// which adds recovered-but-undecided orphans as Recoverable rows
	// directly into slot.list — so the claimed set here (built from every
	// slot's live instances, Recoverable included, plus the records each
	// slot's storage preserves outside its list) is complete without a
	// separate pending-orphans accumulator.
	//
	// Fail closed when any workspace failed to load: its titles are
	// unreadable, so the sweep can't spare them and would kill its live
	// sessions. Skipping only defers stale-session cleanup to a later run.
	if len(failed) > 0 {
		log.For("app").Warn("orphan_cleanup_skipped", "reason", "workspace_load_failed", "workspaces", failed)
	} else {
		claimedTitles := make(map[string]bool)
		for _, slot := range m.slots {
			claimTitles(claimedTitles, slot.list, slot.storage)
		}
		if err := session.CleanupOrphanedSessions(claimedTitles, m.executor()); err != nil {
			log.For("app").Error("orphan_cleanup_failed", "err", err)
		}
	}

	if len(m.slots) == 0 {
		m.loadStartupStorageFallback()
		return
	}

	focused := 0
	focusName := explicit
	if focusName == "" && m.registry != nil {
		focusName = m.registry.LastUsed
	}
	if focusName != "" {
		for i, s := range m.slots {
			if s.wsCtx.Name == focusName {
				focused = i
				break
			}
		}
	}
	m.loadSlot(focused)
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.slots[focused].recovery)

	if m.registry != nil {
		m.saveOpenWorkspaces()
		if name := m.slots[focused].wsCtx.Name; name != "" {
			if err := m.registry.UpdateLastUsed(name); err != nil {
				log.For("app").Debug("registry.update_last_used_failed", "workspace", name, "err", err)
			}
		}
	}
}

// loadStartupStorageFallback runs when no workspace could be restored.
// newHome deferred loading the startup storage to restoreSavedWorkspaces,
// so without this the user would land in global mode over a never-loaded
// storage whose first save (quit, a new session) replaces its readable
// records with the empty list. Load it like classic startup does, minus
// the orphan sweep (see loadStartupStorage). On failure it fails closed:
// the storage's write latch refuses every save, and the error is shown
// rather than exiting, so the user can still open a workspace from the
// picker. The failed workspaces stay in the registry's open list, to be
// retried on the next launch (see restoreFailed).
func (m *home) loadStartupStorageFallback() {
	// Each failed activation re-synced these process-wide flags from its
	// own workspace's config; put the startup config's values back before
	// anything below launches a session.
	if m.appConfig != nil {
		session.SetLoomContextEnabled(m.appConfig.LoomContextEnabled())
		session.SetSubagentTrackingEnabled(m.appConfig.SubagentTrackingEnabled())
	}
	recovery, err := m.loadStartupStorage(m.executor(), false)
	if err != nil {
		// No Cmd path out of startup: the toast expires via
		// ErrBox.ExpireIfDue on the periodic tick instead.
		_ = m.handleError(fmt.Errorf("no workspace could be restored, and loading sessions failed (nothing will be saved): %w", err))
		return
	}
	m.showRecoverySummary(recovery)
}
