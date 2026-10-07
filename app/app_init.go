package app

import (
	"context"
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"path/filepath"
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
//   - uiLock is the takeover lock main holds; the TUI saves and quits
//     when another loom asks to take over (see internal/takeover). Nil
//     when it couldn't be taken: loom then runs unlocked and serves no
//     takeovers.
func Run(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, pendingDir string, noScripts bool, uiLock *takeover.Lock) error {
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
	// as worktree:push()) must not leave the terminal hanging after the TUI
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
	if uiLock != nil {
		if err := uiLock.Listen(takeoverListener(h.fullScreen, p.Send)); err != nil {
			// Not fatal: this loom still holds the lock, so a second one
			// is refused rather than overwriting its sessions.
			log.For("app").Warn("takeover.listen_failed", "err", err)
		}
	}
	_, err = p.Run()
	if h.takenOverBy != nil {
		fmt.Printf("loom: saved and quit; taken over by %s\n", h.takenOverBy)
	}
	return err
}

func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, pendingDir string, noScripts bool) (*home, error) {
	// The model, and its classic workspace: the startup context's state,
	// focused until (and unless) a workspace tab opens. core.New syncs the
	// loom-context flags and writes the prompt files first. On the restore
	// path the classic storage is never loaded unless no workspace
	// activates (core.Model.RestoreSaved's fallback).
	//
	// The context, registry and config belong to the model from here on:
	// newHome reads what it needs of them first, and the model's views and
	// queries after.
	startGlobal := wsCtx != nil && wsCtx.Name == ""
	hasConfig := appConfig != nil
	rcEnabled := hasConfig && appConfig.RemoteControlEnabled()
	model, err := core.New(core.Options{Registry: registry, Program: program, Ctx: wsCtx, Config: appConfig})
	if err != nil {
		return nil, err
	}
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	classic, _ := model.Classic()
	h := &home{
		ctx:        ctx,
		core:       model,
		fullScreen: &foregroundAttach{},
		workspaceSlot: &workspaceSlot{
			id:        classic.ID,
			info:      classic,
			splitPane: sp,
			workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
		},
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:        ui.NewMenu(),
		overview:    ui.NewOverview(),
		errBox:      ui.NewErrBox(),
		state:       stateDefault,
		tabBar:      ui.NewWorkspaceTabBar(),
		skipScripts: noScripts,
		hostFocused: true,
		panes:       ui.NewPaneClients(),
		bells:       make(map[core.InstanceID]bool),
	}
	sp.SetPanes(h.panes)
	// Built after h so the list can point at h.spinner and read h's rows.
	h.list = ui.NewList(&h.spinner, slotRows{h, h.workspaceSlot})
	h.list.SetPanes(h.panes)
	h.seedViews(h.workspaceSlot)
	if h.name() != "" {
		h.list.SetWorkspaceName(h.name())
	}

	// Initialize the script engine and load user scripts. Errors are
	// logged but never propagated — a broken script must not block
	// startup of the TUI.
	initScripts(h)

	// Determine whether we'll restore a saved multi-tab set. If so, skip the
	// classic-mode load below: activateWorkspace() will load each slot fresh,
	// and doing both would re-attach tmux ptmx handles for the same sessions.
	savedOpen := h.core.Registry().Open
	willRestoreSlots := len(savedOpen) > 0 && pendingDir == ""

	cmdExec := cmd2.MakeExecutor()
	h.accountStrip = ui.NewAccountStrip()
	h.core.InitAccounts()
	// The registry's notices (a load error, loom running as an account)
	// land before the startup load's, as they did when set directly.
	h.initCmd = tea.Batch(h.initCmd, h.drainCore())
	// Probe Claude auth once up front (before any workspace terminal is
	// created) so remote-control launch decisions are synchronous and
	// startup terminals aren't stripped of the flag by fail-closed timing.
	// The identity it reads also locates the main config dir extra
	// accounts link to, so it runs whenever one is registered too.
	if rcEnabled || (hasConfig && h.core.HasExtraAccounts()) {
		h.core.SetRCAuth(session.DetectClaudeRemoteControlAuth(program, cmdExec))
	}
	var startupRecovery core.RecoverySummary
	if !willRestoreSlots {
		if err := h.core.LoadClassic(true); err != nil {
			return nil, fmt.Errorf("load instances: %w", err)
		}
		// The load filled the workspace: the store reads it before the
		// drain below, so the attach sees its rows.
		h.seedViews(h.workspaceSlot)
		h.ensureSlotPanes(h.workspaceSlot)
		// The load's notices (a workspace terminal launched without
		// remote control) land before the recovery summary below, as
		// they did when the load set them itself.
		h.initCmd = tea.Batch(h.initCmd, h.drainCore())
		startupRecovery = h.recovery()
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
				if reg := h.core.Registry(); len(reg.Workspaces) > 0 {
					h.setOverlay(overlay.NewStartupWorkspacePicker(reg.Workspaces), overlayWorkspacePickerStartup)
					h.state = stateWorkspace
				}
			}
			h.setOverlay(confirm, overlayConfirmation)
			return
		}
		if reg := h.core.Registry(); !willRestoreSlots && startGlobal && len(reg.Workspaces) > 0 {
			h.setOverlay(overlay.NewStartupWorkspacePicker(reg.Workspaces), overlayWorkspacePickerStartup)
			h.state = stateWorkspace
		}
	}

	// Orphans are handled inline by core's reconcileOrphans, so nothing preempts
	// the startup overlay chain (workspace registration confirm / picker).
	registerNextOverlay()

	h.showRecoverySummary(startupRecovery)

	// Apply persisted layout prefs on every startup path. The slot-restore
	// path already applied them via loadSlot — re-applying is idempotent.
	// lastWidth is still 0 here, so this only sets the flags; the first
	// WindowSizeMsg lays out honoring them.
	h.applyUIPrefs()
	// The program isn't running yet: apply what the model still holds now,
	// keeping its Cmds (an error's hide timer) for Init.
	h.initCmd = tea.Batch(h.initCmd, h.drainCore())
	return h, nil
}

// runNow runs cmd on the calling goroutine, with every Cmd of a
// tea.BatchMsg it yields, recursively, and discards their messages. A
// batched Cmd called directly only returns the BatchMsg the runtime
// would expand, so none of its Cmds would run. For self-contained work
// (releases) before the program runs; once it runs, Cmds belong to it.
func runNow(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runNow(c)
		}
	}
}

// restoreSavedWorkspaces opens the registry's saved tabs (core's
// RestoreSaved: the activations, the restore-failure bookkeeping, the
// orphan sweep, and the classic fallback when none opens) and builds their
// views, then focuses the startup workspace's tab, else the last used.
func (m *home) restoreSavedWorkspaces(saved []config.Workspace) {
	focus := m.core.RestoreSaved(saved)
	if focus < 0 {
		// No tab opened: the classic workspace was loaded in their place.
		m.seedViews(m.workspaceSlot)
		m.ensureSlotPanes(m.workspaceSlot)
		// Its load error (a notice) lands before the summary, as when the
		// fallback set it itself.
		m.initCmd = tea.Batch(m.initCmd, m.drainCore())
		m.showRecoverySummary(m.recovery())
		return
	}
	// The workspace terminals' notices land before the summary, as when
	// each activation set its own.
	m.initCmd = tea.Batch(m.initCmd, m.drainCore())
	classic := m.workspaceSlot
	for _, v := range m.core.Tabs() {
		m.slots = append(m.slots, m.newSlotView(v))
	}
	m.loadSlot(focus)
	// The first tab dropped the classic slot, which this path never
	// loaded, so the release is nil in practice. Were it not, running it
	// here is safe: the program is not running yet (Run installs the pane
	// notifier after newHome), so no pump can block on Send.
	runNow(tea.Batch(releaseSlotCmd(classic), m.prunePanes()))
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.slots[focus].recovery())
}
