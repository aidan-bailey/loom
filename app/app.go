package app

import (
	"bytes"
	"context"
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/keys"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// GlobalInstanceLimit caps the number of simultaneously-tracked
// instances per workspace slot. Once reached, New/Prompt flows short-
// circuit with an error-bar message instead of allocating another
// worktree. The cap is a soft guardrail — tmux server memory and git
// worktree overhead are the real upper bound; raise deliberately.
const GlobalInstanceLimit = 10

var inlineAttachHintStyle = lipgloss.NewStyle().
	Foreground(ui.BorderActive).
	Bold(true)

var statusLineStyle = lipgloss.NewStyle().
	Foreground(ui.BorderMuted)

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
//   - autoYes enables the daemon-driven auto-confirm flow.
//   - pendingDir is an optional directory to seed the new-instance
//     overlay with (used by `loom` invoked from a non-workspace dir).
//   - noScripts disables loading user scripts from ~/.loom/scripts;
//     embedded defaults still load so core keybindings work.
func Run(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, autoYes bool, pendingDir string, noScripts bool) error {
	h, err := newHome(ctx, wsCtx, registry, appConfig, program, autoYes, pendingDir, noScripts)
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
	p := tea.NewProgram(
		h,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err = p.Run()
	return err
}

// metadataResult holds I/O results for one instance from the parallel
// metadata tick. Written by goroutine; status updates applied on main thread.
type metadataResult struct {
	instance   *session.Instance
	tmuxAlive  bool
	updated    bool
	hasPrompt  bool
	captureErr error
	diffErr    error
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateWorkspace is the state when the workspace picker is displayed.
	stateWorkspace
	// stateQuickInteract is the state when the quick input bar is displayed.
	stateQuickInteract
	// stateInlineAttach is the state when keystrokes are forwarded to the tmux session
	// while the UI remains visible.
	stateInlineAttach
	// stateFileExplorer is the state when the file explorer overlay
	// replaces the right pane. Keys route to the overlay until it
	// closes (Esc) or the user picks a file (Enter -> $EDITOR via
	// tea.ExecProcess).
	stateFileExplorer
	// stateOrphanRecovery is the state when the startup orphan-
	// recovery overlay is displayed. Triggered when DiscoverOrphans
	// finds worktrees on disk that aren't referenced in any loaded
	// state.json — the user picks which to recover and which to skip.
	stateOrphanRecovery
)

// workspaceSlot bundles per-workspace state so multiple workspaces can be
// loaded in memory simultaneously.
type workspaceSlot struct {
	wsCtx     *config.WorkspaceContext
	storage   *session.Storage
	appConfig *config.Config
	appState  config.AppState
	list      *ui.List
	splitPane *ui.SplitPane
}

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// splitPane displays the agent and terminal panes with diff overlay
	splitPane *ui.SplitPane
	// quickInputBar displays the inline input bar for quick interactions
	quickInputBar *ui.QuickInputBar
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// activeOverlay is the currently displayed modal (nil when no overlay
	// is open). The concrete type is inspected through the typed
	// helpers below (textInput(), confirmation(), etc.) rather than by
	// holding one pointer field per overlay variety.
	activeOverlay overlay.Overlay
	// activeOverlayKind carries the rendering hint the interface alone
	// can't supply — the workspace picker, for example, needs
	// fullscreen placement on startup and overlay placement
	// mid-session.
	activeOverlayKind overlayKind
	// pendingConfirmation bundles the work to run when the user
	// confirms the active modal. Sync flips in-process state (e.g.,
	// transitioning to Deleting) before the Async tea.Cmd fires, so
	// the spinner is visible by the next render.
	pendingConfirmation overlay.ConfirmationTask
	// pendingDir is the directory path awaiting workspace registration confirmation
	pendingDir string
	// pendingAttachTarget is the instance whose tmux session should be
	// full-screen-attached after the attach help overlay is dismissed.
	pendingAttachTarget *session.Instance

	// -- Workspace slots --

	// activeCtx is the WorkspaceContext for the currently focused workspace.
	activeCtx *config.WorkspaceContext
	// registry is the loaded workspace registry, retained for the picker flow.
	registry *config.WorkspaceRegistry
	// slots holds per-workspace state for all active workspaces
	slots []workspaceSlot
	// focusedSlot is the index into slots for the currently displayed workspace
	focusedSlot int
	// tabBar renders workspace tabs at the top of the TUI
	tabBar *ui.WorkspaceTabBar
	// lastWidth and lastHeight cache the terminal size for sizing new slots
	lastWidth  int
	lastHeight int

	// listWidth is the current rendered width of the left list panel
	// (= int(lastWidth * ListWidthPercent)). Cached for mouse-wheel
	// hit-testing in the tea.MouseMsg branch.
	listWidth int
	// agentBottomY is the screen Y (inclusive) of the last row of the
	// agent pane's bottom border. Mouse events with Y <= agentBottomY
	// route to the agent pane; anything greater routes to the terminal
	// pane. Recomputed on every WindowSizeMsg so the formula stays in
	// sync with SplitPane.SetSize.
	agentBottomY int

	// lastPreviewHash caches the content hash of the selected instance
	// to skip preview ticks when nothing has changed.
	lastPreviewHash []byte
	// lastPreviewTitle tracks which instance the hash belongs to.
	lastPreviewTitle string

	// scripts owns the Lua script engine for user-bound keybindings.
	// Lazily populated by initScripts() on first construction; never
	// nil in normal operation (a failed load still produces an empty
	// engine so Dispatch returns matched=false instead of panicking).
	scripts *script.Engine
	// skipScripts mirrors the --no-scripts CLI flag: when true, the
	// engine still boots with embedded defaults, but ~/.loom/scripts
	// is skipped. Provides an escape hatch when a user script
	// broke the keymap.
	skipScripts bool

	// pendingOrphans accumulates orphan candidates discovered across
	// every loaded storage at startup (global plus each workspace
	// slot). Cleared when the user dismisses the recovery overlay.
	pendingOrphans []session.OrphanCandidate
	// orphanCfgDirs maps each pending orphan's WorktreePath to the
	// configDir of the storage that should host the recovered
	// instance. Avoids re-deriving the configDir from path-stripping
	// at apply time, which would couple two layers to the same
	// "<cfgDir>/worktrees/<user>/<dir>" convention.
	orphanCfgDirs map[string]string
	// pendingStartupOverlay is the deferred next-overlay closure
	// captured when newHome chooses to show the orphan-recovery
	// overlay first. Invoked by handleStateOrphanRecoveryKey after
	// the user dismisses the overlay so the rest of the startup-
	// dialog chain (pendingDir confirm, startup workspace picker)
	// isn't lost. nil after running or when no chained overlay exists.
	pendingStartupOverlay func()
}

func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, autoYes bool, pendingDir string, noScripts bool) (*home, error) {
	cfgDir := ""
	if wsCtx != nil {
		cfgDir = wsCtx.ConfigDir
	}

	appState := config.LoadStateFrom(cfgDir)

	storage, err := session.NewStorage(appState, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}

	h := &home{
		ctx:         ctx,
		activeCtx:   wsCtx,
		registry:    registry,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:        ui.NewMenu(),
		splitPane:   ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:      ui.NewErrBox(),
		storage:     storage,
		appConfig:   appConfig,
		program:     program,
		autoYes:     autoYes,
		state:       stateDefault,
		appState:    appState,
		tabBar:      ui.NewWorkspaceTabBar(),
		skipScripts: noScripts,
	}
	h.list = ui.NewList(&h.spinner, autoYes)
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
	if !willRestoreSlots {
		instancesData, err := storage.LoadInstanceData()
		if err != nil {
			return nil, fmt.Errorf("load instances: %w", err)
		}

		// Rename any pre-rename (claudesquad_*) tmux sessions to their
		// loom_* equivalents so reconcile finds live sessions after the
		// v0.1.0 prefix flip. Idempotent no-op after first launch.
		legacyTitles := make([]string, 0, len(instancesData))
		for _, d := range instancesData {
			legacyTitles = append(legacyTitles, d.Title)
		}
		tmux.RenameLegacySessions(legacyTitles, cmdExec)

		// Reconcile each instance against tmux/worktree reality
		hasWorkspaceTerminal := false
		for _, data := range instancesData {
			if data.IsWorkspaceTerminal {
				hasWorkspaceTerminal = true
			}
			instance, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
			if err != nil {
				log.For("app").Error("reconcile_failed", "title", data.Title, "err", err, "action", "skipping")
				continue
			}
			h.list.AddInstance(instance)()
			if autoYes {
				instance.AutoYes = true
			}
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

		// Discover orphan worktrees (on disk but not in state.json)
		// before CleanupOrphanedSessions runs — a recovered orphan
		// claims its tmux session, so the orphan-tmux sweep below
		// must not kill it. The user is prompted via the startup
		// overlay (set up below in the picker-dispatch block).
		h.recordOrphans(cfgDir, h.list.GetInstances(), cmdExec)

		// Clean up orphaned tmux sessions from previous crashes
		claimedTitles := make(map[string]bool)
		for _, inst := range h.list.GetInstances() {
			claimedTitles[inst.Title] = true
		}
		// Also exempt orphan candidates: the user hasn't decided yet,
		// and pre-emptively killing their tmux would lose the live PTY.
		for _, cand := range h.pendingOrphans {
			claimedTitles[cand.Title] = true
		}
		if err := session.CleanupOrphanedSessions(claimedTitles, cmdExec); err != nil {
			log.For("app").Error("orphan_cleanup_failed", "err", err)
		}

		// Auto-create workspace terminal if in a workspace context and none exists
		if !hasWorkspaceTerminal && wsCtx != nil && wsCtx.RepoPath != "" {
			wtTitle := "Workspace Terminal"
			if wsCtx.Name != "" {
				wtTitle = wsCtx.Name
			}
			wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
				Title:               wtTitle,
				Path:                wsCtx.RepoPath,
				Program:             program,
				IsWorkspaceTerminal: true,
				ConfigDir:           cfgDir,
			})
			if wtErr != nil {
				log.For("app").Error("workspace_terminal.create_failed", "err", wtErr)
			} else {
				h.list.AddInstance(wtInstance)()
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

	// Orphan recovery preempts every other startup overlay. Recovered
	// instances may belong to a workspace the picker would otherwise
	// ask about, so the user has to triage orphans first. The deferred
	// closure runs after the user dismisses the overlay so the next
	// dialog isn't lost.
	if len(h.pendingOrphans) > 0 {
		h.pendingStartupOverlay = registerNextOverlay
		h.setOverlay(overlay.NewOrphanRecoveryPicker(h.pendingOrphans), overlayOrphanRecovery)
		h.state = stateOrphanRecovery
	} else {
		registerNextOverlay()
	}

	return h, nil
}

// restoreSavedWorkspaces activates all workspaces in `saved` as slots, merging
// the explicit startup target (if any) into the set, then focuses the
// appropriate slot. Missing/failed workspaces are dropped silently. The
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

	for _, ws := range desired {
		if err := m.activateWorkspace(ws); err != nil {
			log.For("app").Error("workspace.restore_failed", "name", ws.Name, "err", err)
		}
	}

	// Per-workspace orphan discovery, deferred until each slot has
	// been appended to m.slots. This keeps activateWorkspace itself
	// free of startup-only side effects (so mid-session callers like
	// workspaceRegisteredMsg don't queue orphans the user can't act
	// on — the overlay only opens from newHome's startup chain).
	cmdExec := cmd2.MakeExecutor()
	for _, slot := range m.slots {
		if slot.wsCtx == nil {
			continue
		}
		m.recordOrphans(slot.wsCtx.ConfigDir, slot.list.GetInstances(), cmdExec)
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

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	m.lastWidth = msg.Width
	m.lastHeight = msg.Height
	m.tabBar.SetWidth(msg.Width)

	listWidth := int(float32(msg.Width) * ui.ListWidthPercent)
	paneWidth := msg.Width - listWidth

	// Content gets all height minus tab bar, status line (1), and error box (1).
	contentHeight := msg.Height - m.tabBar.Height() - 2
	m.errBox.SetSize(int(float32(msg.Width)*ui.PreviewWidthPercent), 1)

	if m.state == stateQuickInteract && m.quickInputBar != nil {
		m.quickInputBar.SetWidth(int(float32(msg.Width) * 0.5))
	}
	m.splitPane.SetSize(paneWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	// Cache mouse-wheel hit-test anchors. Mirrors SplitPane.SetSize's
	// own arithmetic — kept in sync here rather than added as a
	// SplitPane accessor because the mouse handler lives in app/.
	m.listWidth = listWidth
	const paneChromePerPane = 2 // 1 top border + 1 bottom border
	availableHeight := contentHeight - 2*paneChromePerPane
	if availableHeight < 0 {
		availableHeight = 0
	}
	agentContentHeight := int(float64(availableHeight) * ui.SplitAgentPercent)
	// Screen-Y inclusive end of the agent's bottom border:
	//   tabBar + 1 (agent top border) + content + 1 (agent bottom border) - 1
	m.agentBottomY = m.tabBar.Height() + 1 + agentContentHeight

	if m.activeOverlay != nil {
		if m.activeOverlayKind == overlayFileExplorer {
			// File explorer replaces the right pane wholesale, so it
			// wants pane-width/content-height rather than the centered
			// overlay percentages used by the other modals.
			m.activeOverlay.SetSize(paneWidth, contentHeight)
		} else {
			m.activeOverlay.SetSize(
				int(float32(msg.Width)*ui.OverlayWidthPercent),
				int(float32(msg.Height)*ui.OverlayHeightPercent),
			)
		}
	}

	agentWidth, agentHeight := m.splitPane.GetAgentSize()
	if err := m.list.SetSessionPreviewSize(agentWidth, agentHeight); err != nil {
		log.For("app").Error("session_preview_size_failed", "err", err)
	}
}

// Init implements tea.Model. It starts the spinner and kicks off the
// preview and metadata tick loops — those loops re-arm themselves by
// returning the same tick message, so Init fires exactly once per Run.
func (m *home) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd,
	)
}

// Update implements tea.Model.
func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case scriptDoneMsg:
		return m, m.handleScriptDone(msg)
	case scriptResumeMsg:
		return m, m.handleScriptResume(msg)
	case previewTickMsg:
		// Check if inline-attached instance is still alive
		inlineAttachExited := false
		if m.state == stateInlineAttach {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Paused() || !selected.TmuxAlive() {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				inlineAttachExited = true
			}
		}

		// Use faster tick during inline attach for responsive feedback
		tickDuration := 100 * time.Millisecond
		if m.state == stateInlineAttach {
			tickDuration = 33 * time.Millisecond
		}
		nextTick := func() tea.Msg {
			time.Sleep(tickDuration)
			return previewTickMsg{}
		}

		// Skip update if same instance and content hash unchanged,
		// but always update during inline attach for responsive feedback.
		if m.state != stateInlineAttach && !inlineAttachExited {
			selected := m.list.GetSelectedInstance()
			var currentHash []byte
			var currentTitle string
			if selected != nil {
				currentHash = selected.GetContentHash()
				currentTitle = selected.Title
			}

			if currentTitle == m.lastPreviewTitle &&
				currentHash != nil &&
				bytes.Equal(currentHash, m.lastPreviewHash) {
				// Agent content unchanged, but the terminal pane has its
				// own independent tmux session whose content may have changed.
				if selected != nil {
					_ = m.splitPane.UpdateTerminal(selected)
				}
				return m, nextTick
			}

			m.lastPreviewHash = currentHash
			m.lastPreviewTitle = currentTitle
		}

		cmd := m.instanceChanged()
		cmds := []tea.Cmd{cmd, nextTick}
		if inlineAttachExited {
			cmds = append(cmds, tea.WindowSize())
		}
		return m, tea.Batch(cmds...)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case tickUpdateMetadataMessage:
		// Collect instances from all workspace slots (focused uses m.list).
		var allInstances []*session.Instance
		if len(m.slots) > 0 {
			for i, slot := range m.slots {
				if i == m.focusedSlot {
					allInstances = append(allInstances, m.list.GetInstances()...)
				} else {
					allInstances = append(allInstances, slot.list.GetInstances()...)
				}
			}
		} else {
			allInstances = m.list.GetInstances()
		}

		// Filter to active instances.
		selected := m.list.GetSelectedInstance()
		var active []*session.Instance
		for _, inst := range allInstances {
			if inst.Started() && !inst.Paused() && inst.GetStatus() != session.Deleting {
				active = append(active, inst)
			}
		}

		// Fan out I/O off the update goroutine. A stalled tmux or git process
		// must not block the UI loop — gatherMetadataCmd runs wg.Wait() inside
		// a background Cmd and returns the results via metadataReadyMsg.
		return m, gatherMetadataCmd(active, selected)
	case metadataReadyMsg:
		// Apply results on main thread.
		for _, r := range msg.results {
			if !r.tmuxAlive {
				if r.instance.IsWorkspaceTerminal {
					log.For("app").Warn("workspace_terminal.tmux_died_restarting", "title", r.instance.Title)
					if err := r.instance.Start(true); err != nil {
						log.For("app").Error("workspace_terminal.restart_failed", "title", r.instance.Title, "err", err)
					}
					continue
				}
				log.For("app").Warn("tick.tmux_gone_marking_paused", "title", r.instance.Title)
				if err := r.instance.TransitionTo(session.Paused); err != nil {
					log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Paused", "err", err.Error())
				}
				continue
			}
			if r.updated {
				if err := r.instance.TransitionTo(session.Running); err != nil {
					log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Running", "err", err.Error())
				}
			} else {
				if r.hasPrompt {
					r.instance.TapEnter()
					if err := r.instance.TransitionTo(session.Prompting); err != nil {
						log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Prompting", "err", err.Error())
					}
				} else {
					if err := r.instance.TransitionTo(session.Ready); err != nil {
						log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Ready", "err", err.Error())
					}
				}
			}
			if r.captureErr != nil {
				log.WarnKV("app.tick.capture_failed", "instance", r.instance.Title, "err", r.captureErr.Error())
			}
			if r.diffErr != nil {
				log.For("app").Warn("diff_stats_update_failed", "err", r.diffErr)
			}
		}
		m.updateTabBarStatuses()
		return m, tickUpdateMetadataCmd
	case tea.MouseMsg:
		// Route mouse-wheel events by cursor position. One wheel tick
		// moves one line (terminal-emulator convention, not half-page).
		//
		// Precedence:
		//   1. Over the list panel (X < listWidth)  → list cursor.
		//   2. Diff overlay visible                 → diff viewport.
		//   3. Over the agent pane (Y <= agentBottomY) → agent.
		//   4. Otherwise                            → terminal.
		//
		// The list case also applies while the user is paused because
		// it only moves the cursor. The content-pane cases bail early
		// for paused/missing instances just like the old behavior.
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
				// List cursor — works regardless of session state.
				if m.listWidth > 0 && msg.X < m.listWidth {
					switch msg.Button {
					case tea.MouseButtonWheelUp:
						m.list.Up()
					case tea.MouseButtonWheelDown:
						m.list.Down()
					}
					return m, m.instanceChanged()
				}

				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.GetStatus() == session.Paused {
					return m, nil
				}

				switch {
				case m.splitPane.IsDiffVisible():
					switch msg.Button {
					case tea.MouseButtonWheelUp:
						m.splitPane.ScrollDiffUp()
					case tea.MouseButtonWheelDown:
						m.splitPane.ScrollDiffDown()
					}
				case msg.Y <= m.agentBottomY:
					switch msg.Button {
					case tea.MouseButtonWheelUp:
						m.splitPane.ScrollAgentUp()
					case tea.MouseButtonWheelDown:
						m.splitPane.ScrollAgentDown()
					}
				default:
					switch msg.Button {
					case tea.MouseButtonWheelUp:
						m.splitPane.ScrollTerminalUp()
					case tea.MouseButtonWheelDown:
						m.splitPane.ScrollTerminalDown()
					}
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		ti := m.textInput()
		if ti == nil {
			return m, nil
		}
		if msg.version != ti.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if ti := m.textInput(); ti != nil {
			ti.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case killInstanceMsg:
		// Terminal session was already closed inside killAction off the update
		// goroutine. Here we only do in-memory list bookkeeping.
		m.list.RemoveInstanceByTitleAndRepo(msg.title, msg.repoName)
		return m, m.instanceChanged()
	case transitionFailedMsg:
		// Revert instance status on failed background op (kill/pause/resume).
		// previousStatus came from this same instance, so the reverse
		// transition should always be allowed; if the state machine rejects
		// it, log and leave the status as-is rather than masking a real bug.
		for _, inst := range m.list.GetInstances() {
			if inst.Title == msg.title {
				if terr := inst.TransitionTo(msg.previousStatus); terr != nil {
					log.For("app").Warn("revert_transition_failed", "err", terr)
				}
				break
			}
		}
		log.For("app").Error("op_failed", "op", msg.op, "title", msg.title, "err", msg.err)
		return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
	case pauseInstanceMsg:
		// Terminal session was already closed inside pauseAction off the update
		// goroutine. Nothing I/O-blocking to do here.
		return m, m.instanceChanged()
	case backgroundCleanupDoneMsg:
		// Nothing to do; the instance was already popped and the cleanup
		// result was logged inside backgroundKillCmd.
		return m, nil
	case resumeDoneMsg:
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case startFullScreenAttachMsg:
		// Resolve the tmux session for the requested pane.
		var ts *tmux.TmuxSession
		switch msg.target {
		case attachTargetAgent:
			ts = msg.instance.TmuxSession()
		case attachTargetTerminal:
			ts = m.splitPane.TerminalTmuxSession()
		}
		if ts == nil {
			return m, m.handleError(fmt.Errorf("no tmux session available for attach"))
		}
		// Close the preview PTY so the foreground tmux attach owns the tty.
		if err := ts.PausePreview(); err != nil {
			return m, m.handleError(err)
		}
		inst := msg.instance
		return m, tea.ExecProcess(ts.FullScreenAttachCmd(), func(err error) tea.Msg {
			return attachDoneMsg{instance: inst, err: err}
		})
	case editorDoneMsg:
		// tea.ExecProcess has returned from $EDITOR. The overlay already
		// closed at key-dispatch time; force a window-size refresh so the
		// panes repaint cleanly after the editor released the tty.
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(msg.err))
		}
		cmds = append(cmds, tea.WindowSize(), m.instanceChanged())
		return m, tea.Batch(cmds...)
	case attachDoneMsg:
		// tea.ExecProcess has restored the terminal. Rebuild the preview PTYs
		// so live capture resumes. Errors here are logged — the next metadata
		// tick will notice a dead session and mark the instance paused.
		if ts := msg.instance.TmuxSession(); ts != nil {
			if err := ts.ResumePreview(); err != nil {
				log.For("app").Error("preview.resume_failed", "title", msg.instance.Title, "err", err)
			}
		}
		if ts := m.splitPane.TerminalTmuxSession(); ts != nil {
			if err := ts.ResumePreview(); err != nil {
				log.For("app").Error("terminal_preview.resume_failed", "title", msg.instance.Title, "err", err)
			}
		}
		m.state = stateDefault
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(msg.err))
		}
		cmds = append(cmds, tea.WindowSize(), m.instanceChanged())
		return m, tea.Batch(cmds...)
	case workspaceRegisteredMsg:
		ws := m.registry.FindByPath(msg.dir)
		if ws == nil {
			return m, m.handleError(fmt.Errorf("workspace not found after registration"))
		}
		if err := m.activateWorkspace(*ws); err != nil {
			return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
		}
		m.activeCtx = config.WorkspaceContextFor(ws)
		m.loadSlot(0)
		m.updateTabBarStatuses()
		if err := m.registry.UpdateLastUsed(ws.Name); err != nil {
			log.For("app").Debug("registry.update_last_used_failed", "workspace", ws.Name, "err", err)
		}
		return m, tea.WindowSize()
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			popped := m.list.PopSelectedForKill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged(), backgroundKillCmd(popped))
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.setOverlay(m.newPromptOverlay(), overlayTextInput)
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.For("app").Error("send_prompt_failed", "err", err)
				}
				msg.instance.Prompt = ""
			}
			// Auto-focus agent pane and capture input
			m.splitPane.SetFocusedPane(ui.FocusAgent)
			m.splitPane.SetInlineAttach(true)
			m.state = stateInlineAttach
			m.menu.SetState(ui.StateInlineAttach)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// persistableInstances filters out instances with transient Deleting status.
func persistableInstances(instances []*session.Instance) []*session.Instance {
	var result []*session.Instance
	for _, inst := range instances {
		if inst.GetStatus() != session.Deleting {
			result = append(result, inst)
		}
	}
	return result
}

// handleQuit persists session state and terminates the TUI. Policy:
// if SaveInstances fails for ANY slot (or for the storage in the
// single-slot path), we refuse to quit and surface the error via
// handleError. The user stays in the TUI so they can fix the underlying
// issue (disk full, read-only mount, etc.) and retry — silent data
// loss on exit is worse than a sticky quit. Both branches share this
// policy; the multi-slot branch used to log-and-quit, which is the
// bug this function comment now documents has been fixed.
func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if len(m.slots) > 0 {
		m.saveCurrentSlot()
		var firstErr error
		for _, slot := range m.slots {
			if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
				log.For("app").Error("workspace.save_failed", "name", slot.wsCtx.Name, "err", err)
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to save workspace %s: %w", slot.wsCtx.Name, err)
				}
			}
		}
		if firstErr != nil {
			return m, m.handleError(firstErr)
		}
		m.saveOpenWorkspaces()
	} else {
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			return m, m.handleError(err)
		}
		if m.registry != nil && len(m.registry.OpenWorkspaces) > 0 {
			if err := m.registry.SetOpenWorkspaces(nil); err != nil {
				log.For("app").Debug("registry.clear_open_failed", "err", err)
			}
		}
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateOrphanRecovery {
		return nil, false
	}
	// If it maps to a built-in binding, highlight the corresponding menu
	// option. Script-bound keys don't get menu highlighting — the menu
	// bar only shows built-in entries.
	name, ok := keys.KeyForString(msg.String())
	if !ok {
		return nil, false
	}

	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

// handleKeyPress dispatches key events to the per-state handler that
// matches m.state. The menu-highlighting protocol fires first: its
// first pass unconditionally swallows the event (keySent=true) and
// the second pass replays it, which tests rely on. State handlers
// live in state_*.go files; this function is deliberately a thin
// router so the wiring stays obvious.
func (m *home) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	switch m.state {
	case stateHelp:
		return m.handleHelpState(msg)
	case stateNew:
		return handleStateNewKey(m, msg)
	case statePrompt:
		return handleStatePromptKey(m, msg)
	case stateInlineAttach:
		return handleStateInlineAttachKey(m, msg)
	case stateQuickInteract:
		return handleStateQuickInteractKey(m, msg)
	case stateWorkspace:
		return handleStateWorkspaceKey(m, msg)
	case stateConfirm:
		return handleStateConfirmKey(m, msg)
	case stateFileExplorer:
		return handleStateFileExplorerKey(m, msg)
	case stateOrphanRecovery:
		return handleStateOrphanRecoveryKey(m, msg)
	default:
		return handleStateDefaultKey(m, msg)
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.splitPane.UpdateDiff(selected)
	m.splitPane.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	if err := m.splitPane.UpdateAgent(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.splitPane.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type tickUpdateMetadataMessage struct{}

// metadataReadyMsg carries the results of a parallel metadata gather back to
// the main update goroutine for application.
type metadataReadyMsg struct {
	results []metadataResult
}

type instanceChangedMsg struct{}

// killInstanceMsg is returned by the killAction goroutine after I/O cleanup
// (git checks, instance kill, storage deletion) is complete. The main event loop
// handles the list removal so it doesn't race with rendering.
//
// repoName is captured before Instance.Kill() zeroes out the git worktree —
// Instance.RepoName() refuses to answer once started=false, so post-hoc lookup
// from the list handler would log "cannot get repo name" and leak the repo
// counter. Empty string means the caller couldn't determine it (not started
// yet, or the pre-capture itself errored); in that case rmRepo is skipped.
type killInstanceMsg struct {
	title    string
	repoName string
}

// transitionFailedMsg is returned when a background status-transitioning
// operation (kill, pause, resume) fails. The main event loop reverts the
// instance to previousStatus so the user can retry. `op` identifies the
// operation for the error log.
type transitionFailedMsg struct {
	title          string
	op             string
	previousStatus session.Status
	err            error
}

// pauseInstanceMsg is returned by the pauseAction goroutine after the instance
// has been paused. Terminal cleanup happens in the main event loop.
type pauseInstanceMsg struct {
	title string
}

// backgroundCleanupDoneMsg is returned by backgroundKillCmd after a popped
// instance has been fully cleaned up. It carries no state — failures are
// already logged inside the Cmd and there's nothing for the main loop to do.
type backgroundCleanupDoneMsg struct{}

// resumeDoneMsg is returned by the Resume Cmd on success. Failures come
// through transitionFailedMsg.
type resumeDoneMsg struct{}

// fullScreenAttachTarget picks which tmux session (agent vs terminal) a
// full-screen attach should target for the selected instance.
type fullScreenAttachTarget int

const (
	attachTargetAgent fullScreenAttachTarget = iota
	attachTargetTerminal
)

// startFullScreenAttachMsg dispatches the actual tea.ExecProcess after the
// attach help overlay has been dismissed. We don't call tea.ExecProcess from
// inside the help-dismiss closure because that would run inside Update and
// we want the runtime to process the Cmd normally.
type startFullScreenAttachMsg struct {
	instance *session.Instance
	target   fullScreenAttachTarget
}

// attachDoneMsg is returned by tea.ExecProcess when the foreground tmux
// attach-session child exits (user hit C-q, or the session died).
type attachDoneMsg struct {
	instance *session.Instance
	err      error
}

// backgroundKillCmd runs the blocking Kill() of a popped instance in a tea.Cmd
// goroutine so the Bubble Tea update loop stays responsive. Used by the
// "abort unstarted instance" paths (ctrl-c / Esc during new-instance entry,
// Esc during prompt entry, failed instanceStartedMsg). The instance has
// already been removed from the list, so any failure here is silently logged.
func backgroundKillCmd(inst *session.Instance) tea.Cmd {
	if inst == nil {
		return nil
	}
	return func() tea.Msg {
		if err := inst.Kill(); err != nil {
			log.For("app").Error("background_instance_kill_failed", "err", err)
		}
		return backgroundCleanupDoneMsg{}
	}
}

// startAttachCmd returns a Cmd that emits startFullScreenAttachMsg so Update
// can hand off to tea.ExecProcess. It exists as a helper because the same
// payload is needed from both the "help skipped" and "help dismissed" paths.
func startAttachCmd(inst *session.Instance, target fullScreenAttachTarget) tea.Cmd {
	return func() tea.Msg {
		return startFullScreenAttachMsg{instance: inst, target: target}
	}
}

// workspaceRegisteredMsg is sent after a pending directory is registered as a workspace.
type workspaceRegisteredMsg struct {
	dir string
}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	repoDir := m.repoPath()
	return func() tea.Msg {
		branches, err := git.SearchBranches(repoDir, filter, nil)
		if err != nil {
			log.For("app").Warn("branch_search_failed", "err", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// tickUpdateMetadataCmd is the callback to update the metadata of the instances every 500ms. Note that we iterate
// overall the instances and capture their output. It's a pretty expensive operation. Let's do it 2x a second only.
var tickUpdateMetadataCmd = func() tea.Msg {
	time.Sleep(500 * time.Millisecond)
	return tickUpdateMetadataMessage{}
}

// gatherMetadataCmd fans out I/O (tmux checks, status captures, git diffs) across
// goroutines and waits for all of them before returning. Running inside a tea.Cmd
// keeps the wg.Wait off the update goroutine — a stalled tmux/git subprocess
// delays the next tick instead of freezing the UI.
//
// Diff refresh is gated on tmux content changes (see Instance.ShouldRefreshDiff):
// an idle instance with no pane output does not trigger a git subprocess on
// every tick. For N active instances with a single active agent, the git
// fan-out drops from ~N subprocesses per tick to ~1.
func gatherMetadataCmd(active []*session.Instance, selected *session.Instance) tea.Cmd {
	return func() tea.Msg {
		results := make([]metadataResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.instance = instance

				r.tmuxAlive = instance.TmuxAlive()
				if !r.tmuxAlive {
					return
				}

				r.updated, r.hasPrompt, r.captureErr = instance.CaptureAndProcessStatus()

				wantFull := instance == selected
				if !instance.ShouldRefreshDiff(r.updated, wantFull) {
					return
				}
				if wantFull {
					r.diffErr = instance.UpdateDiffStats()
				} else {
					r.diffErr = instance.UpdateDiffStatsShort()
				}
			}(i, inst)
		}
		wg.Wait()
		return metadataReadyMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.For("app").Error("handle_error", "err", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up unstarted instances.
func (m *home) cancelPromptOverlay() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	var killCmd tea.Cmd
	if selected != nil && !selected.Started() {
		killCmd = backgroundKillCmd(m.list.PopSelectedForKill())
	}
	m.dismissOverlay()
	m.state = stateDefault
	return tea.Batch(
		tea.Sequence(
			tea.WindowSize(),
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				return nil
			},
		),
		killCmd,
	)
}

// confirmTask shows a confirmation modal with the supplied task
// queued for execution on confirm. Sync fires before Async so
// state transitions (e.g., flipping to Deleting) take effect before
// the async worker even starts.
func (m *home) confirmTask(message string, task overlay.ConfirmationTask) tea.Cmd {
	m.state = stateConfirm
	m.pendingConfirmation = task

	co := overlay.NewConfirmationOverlay(message)
	co.SetWidth(50)
	co.OnCancel = func() {
		m.pendingConfirmation = overlay.ConfirmationTask{}
	}
	m.setOverlay(co, overlayConfirmation)

	return nil
}

// confirmAction is a thin wrapper around confirmTask for callers
// that only need an async body (no sync pre-step).
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	return m.confirmTask(message, overlay.ConfirmationTask{Async: action})
}

// repoPath returns the git repository path for the current context.
// When a workspace is active it returns the workspace's registered path;
// otherwise it falls back to the process working directory.
func (m *home) repoPath() string {
	if len(m.slots) > 0 && m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		if p := m.slots[m.focusedSlot].wsCtx.RepoPath; p != "" {
			return p
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

// configDir returns the config directory for the focused workspace slot.
// Mirrors repoPath() so both functions stay consistent if focusedSlot moves
// out of sync with activeCtx. Returns empty string when no workspace is
// active (triggers fallback to GetConfigDir).
func (m *home) configDir() string {
	if len(m.slots) > 0 && m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		return m.slots[m.focusedSlot].wsCtx.ConfigDir
	}
	if m.activeCtx != nil {
		return m.activeCtx.ConfigDir
	}
	return ""
}

// activateWorkspace loads a workspace's state, config, instances and UI
// components, appending a new slot to m.slots.
func (m *home) activateWorkspace(ws config.Workspace) error {
	wsCtx := config.WorkspaceContextFor(&ws)
	state := config.LoadStateFrom(wsCtx.ConfigDir)
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to create storage for workspace %s: %w", ws.Name, err)
	}

	cmdExec := cmd2.MakeExecutor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		log.For("app").Error("workspace_load_instances_failed", "workspace", ws.Name, "err", err)
	}
	// Note: orphan discovery is intentionally NOT done here. It only
	// runs during the startup paths (newHome's classic-mode load and
	// restoreSavedWorkspaces) so the recovery overlay has a single
	// fire-once trigger. Mid-session activations
	// (applyWorkspaceToggle, workspaceRegisteredMsg) would otherwise
	// queue orphans the user would never see — the overlay only opens
	// from newHome.

	list := ui.NewList(&m.spinner, m.autoYes)
	hasWorkspaceTerminal := false
	for _, inst := range instances {
		if inst.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		list.AddInstance(inst)()
		if m.autoYes {
			inst.AutoYes = true
		}
	}

	// Restart crash-recovered instances.
	for _, inst := range instances {
		if !inst.CrashRecovered {
			continue
		}
		if err := inst.CrashRestart(); err != nil {
			log.For("app").Error("crash_recovery.restart_failed", "instance", inst.Title, "err", err)
			if tErr := inst.TransitionTo(session.Paused); tErr != nil {
				log.For("app").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr)
			}
		}
		inst.CrashRecovered = false
	}

	// Auto-create workspace terminal if none exists
	if !hasWorkspaceTerminal && wsCtx.RepoPath != "" {
		wtTitle := ws.Name
		if wtTitle == "" {
			wtTitle = "Workspace Terminal"
		}

		// A prior non-clean exit may have left a tmux session named
		// claudesquad_<wtTitle> alive without persisting the instance.
		// Startup's CleanupOrphanedSessions is skipped in multi-tab
		// restore mode, so the orphan survives here and Start below
		// would fail with "session already exists", leaving an unusable
		// entry in the list with no branch and no agent.
		if err := session.KillTmuxSessionByTitle(wtTitle, cmdExec); err != nil {
			log.For("app").Debug("workspace_terminal.orphan_kill", "workspace", ws.Name, "err", err.Error())
		}

		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             appConfig.GetProgram(),
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
		if wtErr != nil {
			log.For("app").Error("workspace_terminal.create_failed", "workspace", ws.Name, "err", wtErr)
		} else {
			list.AddInstance(wtInstance)()
			if startErr := wtInstance.Start(true); startErr != nil {
				log.For("app").Error("workspace_terminal.start_failed", "workspace", ws.Name, "err", startErr)
			}
		}
	}

	list.SetWorkspaceName(ws.Name)

	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())

	// Pre-size components if terminal dimensions are known.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		list.SetSize(listWidth, contentHeight)
		splitPane.SetSize(paneWidth, contentHeight)
	}

	m.slots = append(m.slots, workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: appConfig,
		appState:  state,
		list:      list,
		splitPane: splitPane,
	})
	return nil
}

// deactivateWorkspace saves and removes a workspace slot by name.
func (m *home) deactivateWorkspace(name string) {
	idx := -1
	for i, slot := range m.slots {
		if slot.wsCtx.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	slot := m.slots[idx]
	_ = slot.storage.SaveInstances(slot.list.GetInstances())

	m.slots = append(m.slots[:idx], m.slots[idx+1:]...)

	if m.focusedSlot >= len(m.slots) && len(m.slots) > 0 {
		m.focusedSlot = len(m.slots) - 1
	} else if m.focusedSlot > idx {
		m.focusedSlot--
	}
}

// saveCurrentSlot writes the home's active UI fields back into the focused slot.
func (m *home) saveCurrentSlot() {
	if len(m.slots) == 0 {
		return
	}
	s := &m.slots[m.focusedSlot]
	s.list = m.list
	s.splitPane = m.splitPane
	s.storage = m.storage
	s.appConfig = m.appConfig
	s.appState = m.appState
}

// loadSlot copies a slot's fields onto home and updates the active workspace context.
func (m *home) loadSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) {
		return
	}
	slot := m.slots[idx]
	m.focusedSlot = idx
	m.activeCtx = slot.wsCtx
	m.list = slot.list
	m.splitPane = slot.splitPane
	m.storage = slot.storage
	m.appConfig = slot.appConfig
	m.appState = slot.appState
	m.list.SetWorkspaceName(slot.wsCtx.Name)
	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	// Resize immediately using the now-correct tab bar height. Without this,
	// the first View() after a workspace switch uses components pre-sized when
	// the tab bar had 0 names (height=0 instead of 3), producing 3 extra lines
	// that Bubble Tea clips from the top, cutting off the workspace tab bar.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		m.list.SetSize(listWidth, contentHeight)
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
}

// recordOrphans scans cfgDir's worktree directory for entries that
// aren't referenced by any of the supplied loaded instances and
// appends them to home.pendingOrphans. The cfgDir → configDir mapping
// is recorded too so applyOrphanRecovery knows which storage to write
// each recovered entry into.
//
// Failures during scan are logged but do not abort startup — orphan
// recovery is best-effort and the user can still launch loom into a
// degraded state if filesystem access is broken.
func (m *home) recordOrphans(cfgDir string, claimed []*session.Instance, cmdExec cmd2.Executor) {
	claimedPaths := make(map[string]bool, len(claimed))
	for _, inst := range claimed {
		wt, err := inst.GetGitWorktree()
		if err != nil || wt == nil {
			continue
		}
		if p := wt.GetWorktreePath(); p != "" {
			claimedPaths[p] = true
		}
	}
	orphans, err := session.DiscoverOrphans(cfgDir, claimedPaths, cmdExec)
	if err != nil {
		log.For("app").Warn("orphan_discovery_failed", "cfg_dir", cfgDir, "err", err)
		return
	}
	if len(orphans) == 0 {
		return
	}
	if m.orphanCfgDirs == nil {
		m.orphanCfgDirs = make(map[string]string)
	}
	for _, o := range orphans {
		m.orphanCfgDirs[o.WorktreePath] = cfgDir
	}
	m.pendingOrphans = append(m.pendingOrphans, orphans...)
}

// applyOrphanRecovery reconciles each selected orphan into a fresh
// Instance, appends it to the right slot's list, and persists. Returns
// a Cmd that surfaces any per-orphan errors via handleError so the
// user sees what didn't recover; partial success is fine — the
// already-recovered entries are saved before the error fires.
func (m *home) applyOrphanRecovery(selected []session.OrphanCandidate) tea.Cmd {
	if len(selected) == 0 {
		return nil
	}
	cmdExec := cmd2.MakeExecutor()
	now := time.Now()
	program := ""
	if m.appConfig != nil {
		program = m.appConfig.GetProgram()
	}

	// Track which lists (and therefore which storages) need a save
	// call after all candidates are reconciled.
	touched := make(map[string]*ui.List)

	var errs []string
	for _, cand := range selected {
		cfgDir := m.orphanCfgDirs[cand.WorktreePath]

		// Refuse to recover orphans whose cfgDir isn't backed by an
		// active list. The earlier alternative (writing to m.list +
		// the foreign storage) was a sharp edge: it would corrupt
		// the foreign workspace's state.json with the focused
		// workspace's instances. v1 only discovers orphans during
		// startup paths that always have an active slot, so this
		// should not fire in practice — but it's the safe behavior
		// if it ever does.
		list, ok := m.listForCfgDir(cfgDir)
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: no active list for %s", cand.Title, cfgDir))
			continue
		}

		data := session.InstanceData{
			SchemaVersion: session.CurrentSchemaVersion,
			Title:         cand.Title,
			Path:          cand.RepoPath,
			Branch:        cand.BranchName,
			Status:        session.Running, // ReconcileAndRestore adjusts based on tmux/wt state.
			CreatedAt:     now,
			UpdatedAt:     now,
			Program:       m.programForCfgDir(cfgDir, program),
			Worktree: session.GitWorktreeData{
				RepoPath:         cand.RepoPath,
				WorktreePath:     cand.WorktreePath,
				SessionName:      cand.Title,
				BranchName:       cand.BranchName,
				BaseCommitSHA:    cand.BaseCommitSHA,
				IsExistingBranch: true,
			},
		}

		inst, err := session.ReconcileAndRestore(data, cfgDir, cmdExec)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cand.Title, err))
			continue
		}
		list.AddInstance(inst)()
		touched[cfgDir] = list
	}

	// Persist every list that received a recovered instance.
	for cfgDir, list := range touched {
		storage, err := m.storageForCfgDir(cfgDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cfgDir, err))
			continue
		}
		if err := storage.SaveInstances(persistableInstances(list.GetInstances())); err != nil {
			errs = append(errs, fmt.Sprintf("save %s: %v", cfgDir, err))
		}
	}

	if len(errs) > 0 {
		return m.handleError(fmt.Errorf("orphan recovery: %s", strings.Join(errs, "; ")))
	}
	return nil
}

// listForCfgDir returns the live ui.List backing the workspace whose
// configDir matches cfgDir, or (m.list, true) for the focused/global
// case. Used by applyOrphanRecovery to surface the recovered instance
// in the right tab so the user sees it without switching workspaces.
func (m *home) listForCfgDir(cfgDir string) (*ui.List, bool) {
	if cfgDir == "" || cfgDir == m.configDir() {
		return m.list, true
	}
	for i, slot := range m.slots {
		if slot.wsCtx != nil && slot.wsCtx.ConfigDir == cfgDir {
			if i == m.focusedSlot {
				return m.list, true
			}
			return slot.list, true
		}
	}
	return nil, false
}

// storageForCfgDir returns the *session.Storage configured for cfgDir.
// Reuses the focused slot or m.storage when applicable; otherwise
// constructs a fresh Storage on demand. The fresh-construct path is
// the cold case for orphans discovered in non-focused workspaces.
func (m *home) storageForCfgDir(cfgDir string) (*session.Storage, error) {
	if cfgDir == "" || cfgDir == m.configDir() {
		return m.storage, nil
	}
	for _, slot := range m.slots {
		if slot.wsCtx != nil && slot.wsCtx.ConfigDir == cfgDir {
			return slot.storage, nil
		}
	}
	state := config.LoadStateFrom(cfgDir)
	return session.NewStorage(state, cfgDir)
}

// programForCfgDir resolves the agent program for an orphan in the
// workspace at cfgDir. Falls back to fallback (typically the focused
// workspace's program) when no slot matches — better than picking the
// wrong workspace's binary, since fallback is at least the user's
// current default and matches their PATH.
func (m *home) programForCfgDir(cfgDir, fallback string) string {
	if cfgDir == "" || cfgDir == m.configDir() {
		if m.appConfig != nil {
			return m.appConfig.GetProgram()
		}
		return fallback
	}
	for _, slot := range m.slots {
		if slot.wsCtx != nil && slot.wsCtx.ConfigDir == cfgDir && slot.appConfig != nil {
			return slot.appConfig.GetProgram()
		}
	}
	cfg := config.LoadConfigFrom(cfgDir)
	if cfg != nil {
		return cfg.GetProgram()
	}
	return fallback
}

// applyWorkspaceToggle diffs the current slots against the desired list,
// activating and deactivating workspaces as needed.
// Activates new workspaces first so that if activation fails, the old
// workspace is still available.
//
// Global-mode persistence: when entering this function with len(m.slots)
// == 0, m.list and m.storage are pointing at the global ~/.loom state.
// loadSlot would otherwise overwrite both without saving, dropping any
// in-flight changes the user hadn't quit-flushed yet. Persist before the
// transition so the reverse direction (enterGlobalMode) reads back what
// the user was just looking at.
func (m *home) applyWorkspaceToggle(desired []config.Workspace) tea.Cmd {
	if len(m.slots) == 0 {
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			return m.handleError(fmt.Errorf("failed to save global state before workspace transition: %w", err))
		}
	} else {
		m.saveCurrentSlot()
	}

	// Empty desired = explicit return to global mode (e.g. user picked
	// the Global row in the mid-session picker). Handled by a dedicated
	// helper because the inverse transition needs to reconstruct global
	// storage and clear OpenWorkspaces from the registry.
	if len(desired) == 0 {
		return m.enterGlobalMode()
	}

	desiredNames := make(map[string]bool, len(desired))
	for _, ws := range desired {
		desiredNames[ws.Name] = true
	}

	// 1. Activate new workspaces first (safe — adds to slots without removing).
	currentNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		currentNames[slot.wsCtx.Name] = true
	}
	var activationErrors []string
	for _, ws := range desired {
		if !currentNames[ws.Name] {
			if err := m.activateWorkspace(ws); err != nil {
				activationErrors = append(activationErrors,
					fmt.Sprintf("%s: %v", ws.Name, err))
			}
		}
	}

	// 2. Deactivate slots not in desired (reverse order to keep indices stable).
	for i := len(m.slots) - 1; i >= 0; i-- {
		if !desiredNames[m.slots[i].wsCtx.Name] {
			m.deactivateWorkspace(m.slots[i].wsCtx.Name)
		}
	}

	// 3. Load focused slot (or first available).
	if len(m.slots) > 0 {
		if m.focusedSlot >= len(m.slots) {
			m.focusedSlot = 0
		}
		m.loadSlot(m.focusedSlot)
	}

	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	m.saveOpenWorkspaces()

	// 4. Surface activation errors to the user.
	if len(activationErrors) > 0 {
		return tea.Batch(tea.WindowSize(),
			m.handleError(fmt.Errorf("failed to activate: %s",
				strings.Join(activationErrors, "; "))))
	}
	return tea.WindowSize()
}

// enterGlobalMode transitions from workspace-tab mode back to global
// (no-workspace) mode. Reconstructs the global storage/state/list from
// scratch via the same path as newHome — caching the originals would
// require shadow fields on home for every value loadSlot reassigns.
//
// Tmux note: deactivateWorkspace doesn't kill workspace-tab tmux
// sessions, and global instances live in a tmux-name namespace disjoint
// from any tab's, so calling LoadAndReconcile here cannot double-attach
// PTYs that are already attached elsewhere — the safety constraint
// documented at the classic-mode-load comment higher up doesn't apply.
func (m *home) enterGlobalMode() tea.Cmd {
	// Deactivate every workspace tab. Each slot persists its own
	// instances via deactivateWorkspace before being dropped.
	for i := len(m.slots) - 1; i >= 0; i-- {
		m.deactivateWorkspace(m.slots[i].wsCtx.Name)
	}

	// Reconstruct global storage. cfgDir="" is interpreted as ~/.loom
	// by config.LoadStateFrom / session.NewStorage — same as newHome.
	appState := config.LoadStateFrom("")
	appConfig := config.LoadConfigFrom("")
	storage, err := session.NewStorage(appState, "")
	if err != nil {
		return m.handleError(fmt.Errorf("failed to construct global storage: %w", err))
	}

	cmdExec := cmd2.MakeExecutor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		log.For("app").Error("global_load_reconcile_failed", "err", err)
	}

	m.storage = storage
	m.appState = appState
	m.appConfig = appConfig
	m.activeCtx = nil

	m.list = ui.NewList(&m.spinner, m.autoYes)
	for _, inst := range instances {
		m.list.AddInstance(inst)()
		if m.autoYes {
			inst.AutoYes = true
		}
	}

	// Clear registry's open-tab list so the next launch lands in
	// global mode rather than auto-restoring tabs the user just closed.
	if m.registry != nil {
		if err := m.registry.SetOpenWorkspaces(nil); err != nil {
			log.For("app").Warn("clear_open_workspaces_failed", "err", err)
		}
	}

	m.tabBar.SetWorkspaces(nil, 0)

	// Resize components for the now-zero-height tab bar.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		m.list.SetSize(listWidth, contentHeight)
		m.splitPane.SetSize(paneWidth, contentHeight)
	}

	return tea.WindowSize()
}

// sessionToTabStatus maps a session.Status to the corresponding ui.TabStatus.
func sessionToTabStatus(s session.Status) ui.TabStatus {
	switch s {
	case session.Prompting:
		return ui.TabStatusPrompting
	case session.Running:
		return ui.TabStatusRunning
	case session.Ready:
		return ui.TabStatusReady
	case session.Loading:
		return ui.TabStatusLoading
	case session.Paused:
		return ui.TabStatusPaused
	default:
		return ui.TabStatusNone
	}
}

// updateTabBarStatuses checks each slot for instances and updates the tab bar's
// status indicators. The highest-priority status across all instances in a slot wins.
// Precedence (high→low): Prompting > Running > Ready > Loading > Paused > None.
func (m *home) updateTabBarStatuses() {
	if len(m.slots) == 0 {
		return
	}
	statuses := make([]ui.TabStatus, len(m.slots))
	for i, slot := range m.slots {
		var instances []*session.Instance
		if i == m.focusedSlot {
			instances = m.list.GetInstances()
		} else {
			instances = slot.list.GetInstances()
		}
		for _, inst := range instances {
			if !inst.Started() {
				continue
			}
			ts := sessionToTabStatus(inst.GetStatus())
			if ts > statuses[i] {
				statuses[i] = ts
			}
		}
	}
	m.tabBar.SetStatuses(statuses)
}

// saveOpenWorkspaces persists the current ordered list of open workspace tabs
// to the registry so they can be restored on next launch.
func (m *home) saveOpenWorkspaces() {
	if m.registry == nil {
		return
	}
	if err := m.registry.SetOpenWorkspaces(m.slotNames()); err != nil {
		log.For("app").Error("persist_open_workspaces_failed", "err", err)
	}
}

// persistFocusedWorkspace writes the currently focused slot's name to
// LastUsed so the next launch focuses the same tab.
func (m *home) persistFocusedWorkspace() {
	if m.registry == nil || m.focusedSlot < 0 || m.focusedSlot >= len(m.slots) {
		return
	}
	name := m.slots[m.focusedSlot].wsCtx.Name
	if name == "" {
		return
	}
	if err := m.registry.UpdateLastUsed(name); err != nil {
		log.For("app").Error("persist_focused_workspace_failed", "err", err)
	}
}

// slotNames returns the names of all active workspace slots.
func (m *home) slotNames() []string {
	names := make([]string, len(m.slots))
	for i, slot := range m.slots {
		names[i] = slot.wsCtx.Name
	}
	return names
}

// View implements tea.Model.
func (m *home) View() string {
	listView := m.list.String()
	// The file explorer is the only overlay that wholly replaces the
	// right pane rather than floating on top of it. Keeping the list
	// visible is the whole point of this state, so it renders
	// inline via JoinHorizontal instead of via PlaceOverlay below.
	var rightContent string
	if m.state == stateFileExplorer && m.activeOverlay != nil {
		rightContent = m.activeOverlay.View()
	} else {
		rightContent = m.splitPane.String()
	}
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, listView, rightContent)

	sections := []string{}
	if tabBarStr := m.tabBar.String(); tabBarStr != "" {
		sections = append(sections, tabBarStr)
	}

	// Bottom bar: quick input or inline attach hint replaces the status line
	if m.state == stateQuickInteract && m.quickInputBar != nil {
		// Quick input is 2 lines, replaces both status line and error box so panes don't shift.
		sections = append(sections, listAndPreview, m.quickInputBar.View())
	} else if m.state == stateInlineAttach {
		hint := inlineAttachHintStyle.Render("▶ CAPTURING INPUT  ·  ctrl+q to detach, then alt+a/alt+t for fullscreen")
		sections = append(sections, listAndPreview, hint, m.errBox.String())
	} else {
		statusLine := statusLineStyle.Render("? help · q quit")
		sections = append(sections, listAndPreview, statusLine, m.errBox.String())
	}

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		sections...,
	)

	// Overlay render dispatch: all overlay states share the unified
	// activeOverlay pointer. The activeOverlayKind tag distinguishes
	// the cases that need full-screen placement (startup workspace
	// picker and orphan recovery — both fire before mainView is
	// meaningful and should center on the empty terminal).
	if m.activeOverlay != nil && m.state != stateDefault {
		if m.activeOverlayKind == overlayWorkspacePickerStartup ||
			m.activeOverlayKind == overlayOrphanRecovery {
			return lipgloss.Place(m.lastWidth, m.lastHeight,
				lipgloss.Center, lipgloss.Center,
				m.activeOverlay.View())
		}
		switch m.state {
		case statePrompt, stateHelp, stateConfirm, stateWorkspace:
			return overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true, true)
		}
	}

	return mainView
}
