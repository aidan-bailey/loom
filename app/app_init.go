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

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

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
	defer func() {
		if h.scripts != nil {
			h.scripts.CleanupAllCoroutines()
			h.scripts.Close()
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

	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	h := &home{
		ctx:         ctx,
		activeCtx:   wsCtx,
		registry:    registry,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:        ui.NewMenu(),
		splitPane:   sp,
		workbench:   ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
		overview:    ui.NewOverview(),
		errBox:      ui.NewErrBox(),
		storage:     storage,
		appConfig:   appConfig,
		program:     program,
		state:       stateDefault,
		appState:    appState,
		tabBar:      ui.NewWorkspaceTabBar(),
		skipScripts: noScripts,
		hostFocused: true,
	}
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
		// LoadAndReconcile centralizes RenameLegacySessions + per-record
		// reconcile, and on a per-record failure stashes the raw data in
		// storage.unrecovered so the next SaveInstances preserves it.
		// The inline loop this replaced silently dropped failures.
		instances, err := storage.LoadAndReconcile(cmdExec)
		if err != nil {
			return nil, fmt.Errorf("load instances: %w", err)
		}

		hasWorkspaceTerminal := false
		for _, instance := range instances {
			if instance.IsWorkspaceTerminal {
				hasWorkspaceTerminal = true
			}
			h.list.AddInstance(instance)
		}

		// Restart crash-recovered instances
		for _, inst := range h.list.GetInstances() {
			if !inst.CrashRecovered {
				continue
			}
			if err := inst.CrashRestart(); err != nil {
				log.For("app").Error("crash_recovery.restart_failed", "title", inst.Title, "err", err)
				if tErr := inst.TransitionTo(session.Paused); tErr != nil {
					log.For("app").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr.Error())
				}
			}
			inst.CrashRecovered = false
		}

		// Discover orphan worktrees (on disk but not in state.json),
		// auto-clean stale leftovers, and add inline Recoverable entries
		// for any with unsaved work or a live agent. Runs before
		// CleanupOrphanedSessions so a live recoverable's tmux (now a
		// list instance) is exempted by the claimedTitles loop below.
		startupRecovery = h.reconcileOrphans(cfgDir, program, h.list, storage, cmdExec)

		// Clean up orphaned tmux sessions from previous crashes, sparing
		// those of records preserved on disk outside the list.
		claimedTitles := make(map[string]bool)
		claimTitles(claimedTitles, h.list, storage)
		if err := session.CleanupOrphanedSessions(claimedTitles, cmdExec); err != nil {
			log.For("app").Error("orphan_cleanup_failed", "err", err)
		}

		// Auto-create workspace terminal if in a workspace context and none
		// exists — unless a record storage preserves but could not load
		// already owns the title (see activateWorkspace).
		wtTitle := "Workspace Terminal"
		if wsCtx != nil && wsCtx.Name != "" {
			wtTitle = wsCtx.Name
		}
		if !hasWorkspaceTerminal && wsCtx != nil && wsCtx.RepoPath != "" && !slices.Contains(storage.PreservedTitles(), wtTitle) {
			wtOpts := launchOptionsFromConfig(appConfig)
			if h.remoteControlBlocked(effectiveRemoteControl(wtOpts), program) {
				// Non-interactive startup: fall back silently but leave an
				// info-style note (clears on the next status update).
				h.errBox.SetInfo("remote control off: " + h.rcAuth.Reason)
			}
			wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
				Title:               wtTitle,
				Path:                wsCtx.RepoPath,
				Program:             applyLaunchOptions(wtOpts, h.rcAuth, program, wtTitle),
				HeadroomProxy:       wtOpts.HeadroomProxy,
				CacheTTL1h:          wtOpts.CacheTTL1h,
				IsWorkspaceTerminal: true,
				ConfigDir:           cfgDir,
			})
			if wtErr != nil {
				log.For("app").Error("workspace_terminal.create_failed", "err", wtErr)
			} else {
				h.list.AddInstance(wtInstance)
				if err := wtInstance.Start(true); err != nil {
					log.For("app").Error("workspace_terminal.start_failed", "err", err)
				}
			}
		}
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
				Async: func() tea.Msg {
					if err := h.registry.Add(name, pendingDir); err != nil {
						return fmt.Errorf("failed to register workspace: %w", err)
					}
					return workspaceRegisteredMsg{dir: pendingDir}
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

// restoreSavedWorkspaces activates all workspaces in `saved` as slots, merging
// the explicit startup target (if any) into the set, then focuses the
// appropriate slot. Missing/failed workspaces are dropped (failures are
// logged, and any failure skips the server-wide orphan sweep). The
// registry's OpenWorkspaces list is rewritten to match what actually activated.
func (m *home) restoreSavedWorkspaces(saved []config.Workspace) {
	explicit := ""
	if m.activeCtx != nil {
		explicit = m.activeCtx.Name
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
		if err := m.activateWorkspace(ws); err != nil {
			log.For("app").Error("workspace.restore_failed", "name", ws.Name, "err", err)
			failed = append(failed, ws.Name)
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
		if err := m.registry.SetOpenWorkspaces(m.slotNames()); err != nil {
			log.For("app").Debug("registry.set_open_failed", "err", err)
		}
		if name := m.slots[focused].wsCtx.Name; name != "" {
			if err := m.registry.UpdateLastUsed(name); err != nil {
				log.For("app").Debug("registry.update_last_used_failed", "workspace", name, "err", err)
			}
		}
	}
}
