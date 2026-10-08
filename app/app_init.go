package app

import (
	"context"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/aidan-bailey/loom/log"
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
//   - wsCtx is the resolved workspace context, whose name picks the
//     workspace the TUI starts on ("" or nil: the global one).
//   - registry is the workspace registry, which the model owns from here
//     on: it serves every registered workspace.
//   - appConfig is the pre-loaded config from the resolved workspace dir,
//     read for its theme.
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
//
// startCore starts the model as the TUI talks to it: rpc.InProcess runs
// it on its own loop, serves it over an in-memory pipe, and returns the
// client (a core.Core keeping a replica of the model's published state)
// and the function that stops all three. App's tests replace it with
// rpc.InProcessForTest, whose loop keeps every job for the test to run and
// whose client is synchronous.
var startCore = rpc.InProcess

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
	h, err := newHome(ctx, wsCtx, registry, program, pendingDir, noScripts)
	if err != nil {
		return err
	}
	// The model's loop stops when Run returns, after the program quit; a
	// result landing later is dropped, as a Cmd's was.
	defer h.stopCore()
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
	// The model's wakes (a job's result landed, its tick fired) reach the
	// program as coreWakeMsg; forwardWakes ends when the loop stops.
	go forwardWakes(h.wakes, p.Send)
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

func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, program string, pendingDir string, noScripts bool) (*home, error) {
	// The model serves every workspace: Boot loads the account registry,
	// detects the default account's remote-control auth and loads the
	// global and every registered workspace, before its loop starts (the
	// model is single-goroutine until then). The registry belongs to the
	// model from here on.
	model := core.New(core.Options{Registry: registry, Program: program})
	notices := model.Boot()
	// From here on only the model's loop touches the model, and the TUI
	// reaches it through the client.
	client, stopCore, err := startCore(model)
	if err != nil {
		return nil, err
	}
	startupName := ""
	if wsCtx != nil {
		startupName = wsCtx.Name
	}
	return startHome(ctx, client, stopCore, notices, startupName, program, pendingDir, noScripts)
}

// startHome builds the TUI over client, a model booted and served (see
// newHome), whose boot raised notices: it shows the workspace named
// startupName ("" for the global one) while no tab is open, restores the
// registry's saved tabs unless a pendingDir awaits registration, and opens
// the startup overlays. program is what this TUI's drafts default to. On
// an error it stops the client (stopCore).
func startHome(ctx context.Context, client *rpc.Client, stopCore func(), notices []core.Event, startupName, program, pendingDir string, noScripts bool) (*home, error) {
	startGlobal := startupName == ""
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	h := &home{
		ctx:         ctx,
		core:        client,
		wakes:       client.Wakes(),
		stopCore:    stopCore,
		program:     program,
		startupName: startupName,
		fullScreen:  &foregroundAttach{},
		workspaceSlot: &workspaceSlot{
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
	// The classic slot shows the startup workspace, which the model has
	// served since it booted.
	classic, ok := h.servedNamed(startupName)
	if !ok {
		stopCore()
		return nil, fmt.Errorf("initialize storage: the %s workspace could not be loaded (see loom.log)", labelOf(startupName))
	}
	h.id, h.info = classic.ID, classic
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
	// classic-mode open below: restoreSavedWorkspaces opens each tab, and
	// the classic workspace only when none opens.
	savedOpen := h.core.Registry().Open
	willRestoreSlots := len(savedOpen) > 0 && pendingDir == ""

	h.accountStrip = ui.NewAccountStrip()
	// The published state (the account strip's), then the boot's notices
	// (the account registry's: a load error, loom running as an account),
	// land before the startup open's, as they did when the TUI loaded the
	// account registry itself.
	h.initCmd = tea.Batch(h.initCmd, h.drainCore())
	for _, ev := range notices {
		h.initCmd = tea.Batch(h.initCmd, h.applyCoreEvent(ev))
	}
	var startupRecovery core.RecoverySummary
	if !willRestoreSlots {
		// The first open starts the workspace's terminal. A workspace
		// whose load failed has nothing else to show.
		if _, err := h.core.Open(h.id); err != nil {
			h.stopCore()
			return nil, fmt.Errorf("load instances: %w", err)
		}
		// The open may have filled the workspace: the store reads it
		// before the drain below, so the attach sees its rows.
		h.seedViews(h.workspaceSlot)
		h.ensureSlotPanes(h.workspaceSlot)
		// The open's notices (a workspace terminal launched without
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

// labelOf names the workspace called name in notices: its name, or
// "global" for "".
func labelOf(name string) string {
	if name == "" {
		return "global"
	}
	return name
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

// restoreSavedWorkspaces opens the registry's saved tabs (saved, the open
// list from the last run) as tabs, plus the startup workspace when it is
// registered and not among them, and focuses the startup workspace's tab,
// else the last used, else the first. A saved tab that fails to open is
// logged and kept in the open list to be retried (failedOpen); the list is
// then persisted, and the focused tab recorded as the last used. With no
// tab open, the classic slot shows the startup workspace instead: opening
// it fails closed, with the error shown rather than an exit, so the user
// can still open a workspace from the picker (its storage's write latch
// refuses every save). Formerly core.Model.RestoreSaved and its classic
// fallback.
func (m *home) restoreSavedWorkspaces(saved []config.Workspace) {
	inSaved := func(name string) bool {
		return slices.ContainsFunc(saved, func(w config.Workspace) bool { return w.Name == name })
	}
	desired := slices.Clone(saved)
	if name := m.startupName; name != "" && !inSaved(name) {
		if i := slices.IndexFunc(m.core.Registry().Workspaces, func(w config.Workspace) bool { return w.Name == name }); i >= 0 {
			desired = append(desired, m.core.Registry().Workspaces[i])
		}
	}
	var opened []core.WorkspaceView
	for _, def := range desired {
		v, err := m.openNamed(def.Name)
		if err != nil {
			log.For("app").Error("workspace.restore_failed", "name", def.Name, "err", err)
			if inSaved(def.Name) {
				// Was open: keep it open, to be retried.
				m.failedOpen = append(m.failedOpen, def.Name)
			}
			continue
		}
		opened = append(opened, v)
	}

	if len(opened) == 0 {
		// No tab opened: the classic workspace is shown in their place.
		_, err := m.core.Open(m.id)
		m.seedViews(m.workspaceSlot)
		m.ensureSlotPanes(m.workspaceSlot)
		m.initCmd = tea.Batch(m.initCmd, m.drainCore())
		if err != nil {
			m.initCmd = tea.Batch(m.initCmd, m.handleError(fmt.Errorf("no workspace could be restored, and loading sessions failed (nothing will be saved): %w", err)))
		}
		m.showRecoverySummary(m.recovery())
		return
	}
	// The workspace terminals' notices land before the summary, as when
	// each activation set its own.
	m.initCmd = tea.Batch(m.initCmd, m.drainCore())
	classic := m.workspaceSlot
	for _, v := range opened {
		m.slots = append(m.slots, m.newSlotView(v))
	}
	focus := 0
	focusName := m.startupName
	if focusName == "" {
		focusName = m.core.Registry().LastUsed
	}
	if i := slices.IndexFunc(m.slots, func(s *workspaceSlot) bool { return focusName != "" && s.name() == focusName }); i >= 0 {
		focus = i
	}
	m.loadSlot(focus)
	// The first tab dropped the classic slot, which this path never
	// opened, so the release is nil in practice. Were it not, running it
	// here is safe: the program is not running yet (Run installs the pane
	// notifier after newHome), so no pump can block on Send.
	runNow(tea.Batch(releaseSlotCmd(classic), m.prunePanes()))
	m.persistOpenList()
	if err := m.core.SetLastUsed(m.name()); err != nil {
		log.For("app").Debug("registry.update_last_used_failed", "workspace", m.name(), "err", err)
	}
	m.updateTabBarStatuses()
	m.showRecoverySummary(m.recovery())
}
