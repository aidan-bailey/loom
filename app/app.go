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
	"github.com/aidan-bailey/loom/session/vt"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// GlobalInstanceLimit caps the number of simultaneously-tracked
// instances per workspace slot. Once reached, New/Prompt flows short-
// circuit with an error-bar message instead of allocating another
// worktree. The cap is a soft guardrail — tmux server memory and git
// worktree overhead are the real upper bound; raise deliberately.
const GlobalInstanceLimit = 10

var (
	inlineAttachHintStyle, statusLineStyle lipgloss.Style
)

func init() { ui.RegisterThemeHook(rebuildAppStyles) }

func rebuildAppStyles() {
	inlineAttachHintStyle = lipgloss.NewStyle().
		Foreground(ui.Accent).
		Bold(true)
	statusLineStyle = lipgloss.NewStyle().
		Foreground(ui.Rule)
}

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

// metadataResult holds I/O results for one instance from the parallel
// metadata tick. Written by goroutine; status updates applied on main thread.
type metadataResult struct {
	instance   *session.Instance
	tmuxAlive  bool
	ptmxAlive  bool
	updated    bool
	hasPrompt  bool
	captureErr error
	diffErr    error
	// emulatorDriven marks instances whose status rides pane events (quiet
	// detection); the tick must not run the status ladder for them.
	emulatorDriven bool
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
	// stateSettings is the state when the settings overlay is displayed.
	stateSettings
	// stateMergePicker is the state when the merge-session picker
	// overlay is displayed (opened by the 'm' key).
	stateMergePicker
	// stateLaunchOptions is the state when the Session Launch Options
	// modal is displayed, between title/prompt entry and actually
	// starting a new instance.
	stateLaunchOptions
)

// viewMode selects the top-level presentation: focus (rail + panes) or
// overview (fleet card grid). Orthogonal to the state machine — only
// stateDefault key routing and View branch on it.
type viewMode int

const (
	viewFocus viewMode = iota
	viewOverview
	// viewWorkbench is the single-session deep-dive: agent split left
	// (terminal force-hidden), tabbed content panel right. Never
	// persisted — quit/restart lands in focus.
	viewWorkbench
)

// overviewCursor is the fleet overview's selection in domain coordinates.
type overviewCursor struct {
	slot int // index into home.slots
	inst int // index into that slot's list
}

// workspaceSlot bundles per-workspace state so multiple workspaces can be
// loaded in memory simultaneously.
type workspaceSlot struct {
	wsCtx     *config.WorkspaceContext
	storage   *session.Storage
	appConfig *config.Config
	appState  config.AppState
	list      *ui.List
	splitPane *ui.SplitPane
	// workbench pairs with this slot's splitPane (its terminal tab
	// shows the slot's shared TerminalPane), swapped onto home by
	// loadSlot exactly like splitPane.
	workbench *ui.Workbench
	// recovery holds the orphan-reconcile summary from this slot's last
	// activation, surfaced once the slot becomes focused.
	recovery recoverySummary
}

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// pendingLaunchOptions holds the compose-and-start closure for a
	// not-yet-started instance while stateLaunchOptions is active.
	// state_new.go/state_prompt.go stash it (capturing the instance and
	// any prompt-flow-specific data like selectedBranch) right before
	// opening the Session Launch Options modal; handleStateLaunchOptionsKey
	// invokes it with the user's chosen overlay.LaunchOptions on confirm,
	// then clears it. nil outside that window.
	pendingLaunchOptions func(overlay.LaunchOptions) (tea.Model, tea.Cmd)

	// pendingLaunchOptionsCancel runs when the Session Launch Options
	// modal is dismissed without confirming (Esc/ctrl+c). The creation
	// flow (state_new.go/state_prompt.go) sets this to pop-and-kill the
	// pending, not-yet-started instance. The restart flow
	// (runRestartWithOptionsSelected) sets it to a no-op dismiss, since
	// the instance being edited already exists and must survive a
	// cancel untouched. nil outside the stateLaunchOptions window.
	pendingLaunchOptionsCancel func() (tea.Model, tea.Cmd)

	// keySent is used to manage underlining menu items
	keySent bool

	// attachingInstance is set for the duration of a full-screen attach
	// (PausePreview -> tea.ExecProcess -> ResumePreview; see
	// startFullScreenAttachMsg/attachDoneMsg) and nil otherwise. The metadata
	// tick's ptmx self-heal (metadataReadyMsg) must not call RepairPtmx on
	// this instance while it is set — PtmxAlive is expected to read false
	// during that window, and racing a Restore against the in-flight
	// ExecProcess would fight over the same tmux session's attach.
	attachingInstance *session.Instance

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// splitPane displays the agent and terminal panes with diff overlay
	splitPane *ui.SplitPane
	// viewMode selects focus (rail + panes) or overview (fleet card
	// grid). Restored from config.UIPrefs.ViewMode at startup.
	viewMode viewMode
	// overview renders the fleet-triage card grid when viewMode is
	// viewOverview.
	overview *ui.Overview
	// workbench renders the right content panel when viewMode is
	// viewWorkbench; the left half is m.splitPane with its terminal
	// hidden (wbPrevTerminalHidden restores the user's setting on exit).
	workbench            *ui.Workbench
	wbPrevTerminalHidden bool
	// wbLeftWidth is the workbench's agent-column width in screen
	// cells, cached for mouse-wheel routing (like listWidth).
	wbLeftWidth int
	// wbRatio is the in-memory agent share for the current session
	// (0 = default). Flushed to UIPrefs.WorkbenchRatios on exit/quit.
	wbRatio float64
	// quickInputBar displays the inline input bar for quick interactions
	quickInputBar *ui.QuickInputBar
	// errBox displays error messages
	errBox *ui.ErrBox
	// rcAuth caches whether the current Claude authentication can drive
	// --remote-control, detected once at startup (see remote_control.go).
	// Global to the machine's login, so one probe covers every workspace.
	rcAuth session.RemoteControlAuth
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
	// pendingMergeTarget and pendingMergeSourceItems capture the merge
	// target instance and a snapshot of the eligible source list at the
	// moment the merge picker opens. A background message unrelated to
	// key input (e.g. recoverDoneMsg reassigning m.list's selection, or
	// a kill/resume completing) can still land while stateMergePicker is
	// active — m.state only gates key-press routing, not arbitrary
	// tea.Msg handling in Update(). Re-querying m.list live when Enter
	// is pressed would let such a background change silently swap which
	// instances the merge acts on. Both fields are cleared once the
	// picker closes.
	pendingMergeTarget      *session.Instance
	pendingMergeSourceItems []*session.Instance

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

	// overviewCursor is the domain-space overview selection: a slot index
	// into m.slots and an instance index into that slot's list. Distinct
	// from the render-space ui.OverviewCursor overviewData() translates to.
	overviewCursor overviewCursor

	// listWidth is the current rendered width of the left list panel
	// (= int(lastWidth * ListWidthPercent)). Cached for mouse-wheel
	// hit-testing in the tea.MouseMsg branch.
	listWidth int
	// railHidden hides the left session-list rail, giving the split
	// pane the full width. Persisted via config.UIPrefs.RailHidden.
	railHidden bool
	// agentBottomY is the screen Y (inclusive) of the last row of the
	// agent pane's bottom border. Mouse events with Y <= agentBottomY
	// route to the agent pane; anything greater routes to the terminal
	// pane. Recomputed on every WindowSizeMsg so the formula stays in
	// sync with SplitPane.SetSize.
	agentBottomY int

	// dragging is true between a left MouseClickMsg and its MouseReleaseMsg while
	// a text selection is being drawn; dragPane is the FocusAgent/FocusTerminal
	// target captured at drag start so motion stays within the originating pane.
	dragging bool
	dragPane int

	// lastEscAt timestamps the last Esc press in interact mode, so a quick
	// double-Esc exits while a single Esc still forwards to the agent.
	lastEscAt time.Time

	// interactLeftDown tracks a left-button press in interact mode whose intent
	// (drag-select vs. click-into-agent) isn't yet known; interactAnchorRow/Col
	// is the pane-local cell where it started.
	interactLeftDown                     bool
	interactAnchorRow, interactAnchorCol int

	// lastPreviewHash caches the content hash of the selected instance
	// to skip preview ticks when nothing has changed.
	lastPreviewHash []byte
	// lastPreviewTitle tracks which instance the hash belongs to.
	lastPreviewTitle string

	// dirtySessions records tmux session names that emitted output since the
	// last health tick (event mode only). Consumed by takeDirty to gate
	// diff-stat refreshes. Update-goroutine only.
	dirtySessions map[string]bool

	// redetectPending tracks sessions with an armed delayed re-detection
	// (see maybeRedetect), so inconclusive detections cannot stack parallel
	// re-detect chains. Update-goroutine only.
	redetectPending map[string]bool

	// pendingRatioSaves buffers title→ratio pairs recorded by resizeSplit
	// until the throttled ratioSaveMsg flushes them into one mutateUIPrefs
	// write — key-repeat resize would otherwise fsync state.json per
	// keystroke. applyStoredRatio reads it first (pending is newest
	// truth); saveCurrentSlot/handleQuit flush it synchronously.
	// ratioSaveArmed dedupes the flush tick (see maybeArmRatioSave).
	// Update-goroutine only.
	pendingRatioSaves map[string]float64
	ratioSaveArmed    bool

	// hostFocused mirrors the host terminal's focus state (via tea.FocusMsg/
	// BlurMsg with ReportFocus on). Assumed focused at startup; used to
	// synthesize correct focus events when panes/sessions switch.
	hostFocused bool

	// lastFocusTitle is the title of the instance that last received a
	// synthesized focus-in on pane FocusAgent, so instanceChanged can
	// synthesize the matching focus-out when the selection moves on.
	lastFocusTitle string

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

		// Clean up orphaned tmux sessions from previous crashes
		claimedTitles := make(map[string]bool)
		for _, inst := range h.list.GetInstances() {
			claimedTitles[inst.Title] = true
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

	// Sweep orphan tmux sessions left by prior crashes. The classic
	// startup path does this inline in activateWorkspace's caller; the
	// multi-tab restore path historically did not, so stale
	// loom_*/claudesquad_* sessions accumulated across restarts. Each
	// slot's activateWorkspace call above already ran reconcileOrphans,
	// which adds recovered-but-undecided orphans as Recoverable rows
	// directly into slot.list — so the claimed set here (built from every
	// slot's live instances, Recoverable included) is complete without a
	// separate pending-orphans accumulator.
	claimedTitles := make(map[string]bool)
	for _, slot := range m.slots {
		for _, inst := range slot.list.GetInstances() {
			claimedTitles[inst.Title] = true
		}
	}
	if err := session.CleanupOrphanedSessions(claimedTitles, cmd2.MakeExecutor()); err != nil {
		log.For("app").Error("orphan_cleanup_failed", "err", err)
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

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	m.lastWidth = msg.Width
	m.lastHeight = msg.Height
	m.tabBar.SetWidth(msg.Width)

	// Workbench mode has no rail: zero the list width (mirroring the
	// railHidden path) so the cached m.listWidth mouse anchor is correct.
	inWorkbench := m.viewMode == viewWorkbench && m.workbench != nil
	listWidth := int(float32(msg.Width) * ui.ListWidthPercent)
	if m.railHidden || inWorkbench {
		listWidth = 0
	}
	paneWidth := msg.Width - listWidth

	// Content gets all height minus tab bar, status line (1), and error box (1).
	contentHeight := msg.Height - m.tabBar.Height() - 2

	m.errBox.SetSize(int(float32(msg.Width)*ui.PreviewWidthPercent), 1)

	if m.state == stateQuickInteract && m.quickInputBar != nil {
		m.quickInputBar.SetWidth(int(float32(msg.Width) * 0.5))
	}
	if inWorkbench {
		// ORDERING CONTRACT: SplitPane.SetSize zeroes the hidden
		// terminal; Workbench.SetSize afterwards re-sizes it for the
		// panel when its tab is active.
		leftW := int(float64(msg.Width) * m.workbenchRatio())
		m.splitPane.SetSize(leftW, contentHeight)
		m.workbench.SetSize(msg.Width-leftW, contentHeight)
		m.wbLeftWidth = leftW
	} else {
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
	m.list.SetSize(listWidth, contentHeight)
	if m.overview != nil { // bare test homes may not construct one
		m.overview.SetSize(msg.Width, contentHeight)
	}

	// Cache mouse-wheel hit-test anchors. The agent pane's inner height
	// comes straight from the SplitPane via AgentContentHeight() —
	// SetSize ran above, so the accessor reflects the current
	// ratio/hidden-terminal layout.
	m.listWidth = listWidth
	// Screen-Y inclusive end of the agent's bottom border:
	//   tabBar + 1 (agent top border) + content + 1 (agent bottom border) - 1
	m.agentBottomY = m.tabBar.Height() + 1 + m.splitPane.AgentContentHeight()

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

// applyUIPrefs pushes persisted layout prefs onto the components.
// Called at the end of newHome (classic startup), after a slot is
// loaded/focused (loadSlot), and on entering global mode.
func (m *home) applyUIPrefs() {
	if m.appState == nil {
		// Bare test homes construct no app state; nothing to apply.
		return
	}
	p := m.appState.GetUIPrefs()
	if p.ViewMode == "overview" {
		m.enterOverview()
	} else {
		m.viewMode = viewFocus
	}
	m.railHidden = p.RailHidden
	m.splitPane.SetTerminalHidden(p.TerminalHidden)
	m.applyStoredRatio(m.list.GetSelectedInstance())
	if m.lastWidth > 0 {
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	}
}

// applyStoredRatio applies inst's persisted split ratio, or resets to
// the default when none is stored. The else-reset keeps the layout
// deterministic: switching to an instance always shows the same split
// a fresh restart would, instead of inheriting whatever ratio the
// previously selected instance left behind.
func (m *home) applyStoredRatio(inst *session.Instance) {
	if m.appState == nil || inst == nil {
		return
	}
	// A pending (not-yet-flushed) resize is the newest truth and must win
	// over persisted state: handleScriptDone runs instanceChanged — and
	// therefore this function — unconditionally right after applying the
	// deferred resizeSplit action, so consulting only the persisted
	// SplitRatios would revert the fresh adjustment to the stale/default
	// ratio before View ever renders it.
	if r, ok := m.pendingRatioSaves[inst.Title]; ok {
		m.splitPane.SetAgentRatio(r)
		return
	}
	if r, ok := m.appState.GetUIPrefs().SplitRatios[inst.Title]; ok {
		m.splitPane.SetAgentRatio(r)
		return
	}
	m.splitPane.SetAgentRatio(ui.SplitAgentPercent)
}

// jumpWaiting moves selection to the next/prev agent needing attention
// (Prompting or bell), across all open workspaces, wrapping. When the
// target is in another slot it saves the current slot, focuses the
// target's, and selects there. No-op when none wait. Main-goroutine
// only.
func (m *home) jumpWaiting(dir int) {
	// Overview mode with slots: `]`/`[` move the overview cursor to the
	// next waiting card — no focus switch, no OpenWorkspaces write.
	// Committing is enter/D/r's job (focusCursorSlot). Collapsed groups
	// are skipped, matching cursor motion (the cursor cannot sit on an
	// unrendered card). Classic overview falls through: m.list's
	// selection IS the cursor there.
	if m.viewMode == viewOverview && len(m.slots) > 0 {
		order := m.fleetOrder()
		n := len(order)
		if n == 0 {
			return
		}
		start := 0
		for i, p := range order {
			if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
				start = i
				break
			}
		}
		for step := 1; step <= n; step++ {
			p := order[((start+dir*step)%n+n)%n]
			inst := m.slotList(p.slot).GetInstances()[p.inst]
			if inst.GetStatus() == session.Prompting || inst.BellPending() {
				m.overviewCursor = overviewCursor{slot: p.slot, inst: p.inst}
				return
			}
		}
		return
	}
	// Build fleet display order over ALL slots (not just loaded/expanded
	// — waiting agents in collapsed groups are still reachable). Unlike
	// fleetOrder (used by overview cursor motion), this does NOT skip
	// collapsed groups.
	var order []fleetPos
	if len(m.slots) == 0 {
		// Classic/global mode: no slots, walk m.list directly.
		items := m.list.GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			order = append(order, fleetPos{slot: 0, inst: idx})
		}
	} else {
		for _, si := range m.fleetSlotOrder() {
			items := m.slotList(si).GetInstances()
			for _, idx := range ui.SortForOverview(items) {
				if items[idx].GetStatus() == session.Deleting {
					continue
				}
				order = append(order, fleetPos{slot: si, inst: idx})
			}
		}
	}
	n := len(order)
	if n == 0 {
		return
	}
	// Current position: focused slot's current selection (or -1).
	start := -1
	selIdx := m.list.SelectedIdx()
	for i, p := range order {
		if p.slot == m.focusedSlot && p.inst == selIdx {
			start = i
			break
		}
	}
	if start < 0 {
		start = 0
	}
	for step := 1; step <= n; step++ {
		i := ((start+dir*step)%n + n) % n
		p := order[i]
		var list *ui.List
		if len(m.slots) == 0 {
			list = m.list
		} else {
			list = m.slotList(p.slot)
		}
		inst := list.GetInstances()[p.inst]
		if inst.GetStatus() == session.Prompting || inst.BellPending() {
			if len(m.slots) != 0 && p.slot != m.focusedSlot {
				m.saveCurrentSlot()
				m.loadSlot(p.slot)
			}
			m.list.SetSelectedInstance(p.inst)
			return
		}
	}
}

// resizeSplit adjusts the agent/terminal ratio in-memory immediately and
// records the new ratio for the selected instance in pendingRatioSaves.
// Persistence is deliberately NOT synchronous: this runs per keystroke
// (key-repeat ≈30/s) via a deferred script action, and mutateUIPrefs
// write-through would block the Update goroutine on an fsync each time.
// The deferred action can't return a tea.Cmd, so handleScriptDone arms
// the throttled flush tick via maybeArmRatioSave after applying us.
func (m *home) resizeSplit(delta float64) {
	r := m.splitPane.AdjustAgentRatio(delta)
	if sel := m.list.GetSelectedInstance(); sel != nil {
		if m.pendingRatioSaves == nil {
			m.pendingRatioSaves = make(map[string]float64)
		}
		m.pendingRatioSaves[sel.Title] = r
	}
	// Guard like applyUIPrefs: before the first WindowSizeMsg (or in bare
	// test homes) there is no size to re-lay-out against.
	if m.lastWidth > 0 {
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	}
}

// flushPendingRatioSaves synchronously drains resizeSplit's pending
// title→ratio map into one persisted mutateUIPrefs write, pruning
// SplitRatios entries whose instances are no longer in the
// (per-workspace) list so killed sessions don't leak entries forever.
// Callers: the throttled ratioSaveMsg tick, saveCurrentSlot (pending
// entries must land in the CURRENT slot's state.json before a slot
// swap — workspaces can share instance titles like "main"), and
// handleQuit (so the last resize survives exit). No-op when nothing is
// pending. Update-goroutine only.
func (m *home) flushPendingRatioSaves() {
	if len(m.pendingRatioSaves) == 0 {
		return
	}
	pend := m.pendingRatioSaves
	m.pendingRatioSaves = nil
	live := make(map[string]bool)
	for _, inst := range m.list.GetInstances() {
		live[inst.Title] = true
	}
	m.mutateUIPrefs(func(p *config.UIPrefs) {
		if p.SplitRatios == nil {
			p.SplitRatios = make(map[string]float64)
		}
		for title, r := range pend {
			p.SplitRatios[title] = r
		}
		for title := range p.SplitRatios {
			if !live[title] {
				delete(p.SplitRatios, title)
			}
		}
	})
}

// mutateUIPrefs applies fn to a copy of the prefs and persists; save
// errors are logged, not surfaced (layout prefs are best-effort).
// Persistence is a synchronous write-through to state.json — fine for
// rare toggles; debounce burst callers (e.g. key-repeat ratio changes).
func (m *home) mutateUIPrefs(fn func(*config.UIPrefs)) {
	if m.appState == nil {
		// Bare test homes construct no app state; nothing to persist.
		return
	}
	p := m.appState.GetUIPrefs()
	fn(&p)
	if err := m.appState.SetUIPrefs(p); err != nil {
		log.For("app").Warn("ui_prefs_save_failed", "err", err)
	}
}

// Init implements tea.Model. It starts the spinner and kicks off the
// preview and metadata tick loops — those loops re-arm themselves by
// returning the same tick message, so Init fires exactly once per Run.
func (m *home) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
		tickUpdateMetadataCmd,
	}
	// Event mode renders on paneDirtyMsg; the timer poll only survives for
	// the snapshot/Windows path, which has no emulator to emit events.
	if !tmux.EmulatorEnabled() {
		cmds = append(cmds, func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		})
	}
	return tea.Batch(cmds...)
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
		// Event mode renders on paneDirtyMsg — a stray tick neither renders
		// nor re-arms. This case is the snapshot/Windows path only.
		if tmux.EmulatorEnabled() {
			return m, nil
		}
		// Check if the inline-attached pane's own session is still alive
		// (see focusedPaneAlive: the agent and terminal panes have
		// independent tmux sessions, so this must track whichever one is
		// actually focused, not always the agent's).
		inlineAttachExited := false
		if m.state == stateInlineAttach {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				inlineAttachExited = true
			}
		}

		// Use a faster tick while interacting (keys/mouse forwarded to the agent)
		// so typed echo and the agent's redraw feel responsive. The per-tick work
		// is render-only (emulator Render, no subprocess), so ~60fps is cheap;
		// background panes stay at 100ms to save CPU.
		tickDuration := 100 * time.Millisecond
		if m.state == stateInlineAttach {
			tickDuration = 16 * time.Millisecond
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
					// When the agent pane is scrolled back, re-render it too:
					// scrolling changes the window (and the new-lines counter)
					// without changing the live content hash, so this
					// short-circuit would otherwise freeze the scroll.
					if m.splitPane.IsAgentInScrollMode() {
						_ = m.splitPane.UpdateAgent(selected)
					}
				}
				return m, nextTick
			}

			m.lastPreviewHash = currentHash
			m.lastPreviewTitle = currentTitle
		}

		cmd := m.instanceChanged()
		cmds := []tea.Cmd{cmd, nextTick}
		if inlineAttachExited {
			cmds = append(cmds, tea.RequestWindowSize)
		}
		return m, tea.Batch(cmds...)
	case paneDirtyMsg:
		m.markDirty(msg.session)
		selected := m.list.GetSelectedInstance()

		if inst := m.instanceForSession(msg.session); inst != nil {
			// Output arrived → the agent is doing something. Mirrors the old
			// tick's updated→Running transition; Ready re-derives on the
			// quiet event once the burst settles. Prompting is exempt:
			// focus-in/out forwarding (host focus, selection changes) makes
			// agents repaint, and that output must not relabel a waiting
			// prompt as Running — quiet-time detection owns leaving
			// Prompting once the prompt is actually gone.
			st := inst.GetStatus()
			if st == session.Ready {
				if err := inst.TransitionTo(session.Running); err != nil {
					log.For("app").Warn("event.transition_failed", "instance", inst.Title, "to", "Running", "err", err.Error())
				}
				m.updateTabBarStatuses()
			}
			if selected != nil && inst == selected {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, nil
		}
		// Not an agent session — the terminal pane's current session renders;
		// dirty events from cached-but-hidden terminal sessions are dropped.
		if selected != nil && msg.session == m.splitPane.CurrentTerminalSessionName() {
			if err := m.splitPane.UpdateTerminal(selected); err != nil {
				return m, m.handleError(err)
			}
		}
		return m, nil
	case paneQuietMsg:
		inst := m.instanceForSession(msg.session)
		if !statusEligible(inst) {
			// A quiet that lands mid-Start (Loading) is this burst's only
			// settle signal — quiet never re-fires without new output, so
			// dropping it would leave the unconditional Running set by
			// Start/Resume uncorrected. Re-check after the start resolves.
			if inst != nil && inst.GetStatus() == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(inst)
	case ratioSaveMsg:
		// Throttled flush of resizeSplit's pending ratios — one persisted
		// write per 750ms window instead of one per keystroke. A flush
		// that already ran (slot switch, quit) leaves the map empty and
		// this tick simply disarms.
		m.ratioSaveArmed = false
		m.flushPendingRatioSaves()
		return m, nil
	case redetectMsg:
		delete(m.redetectPending, msg.session)
		inst := m.instanceForSession(msg.session)
		if !statusEligible(inst) {
			if inst != nil && inst.GetStatus() == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(inst)
	case statusDetectedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		if msg.err != nil {
			log.WarnKV("app.event.capture_failed", "instance", msg.instance.Title, "err", msg.err.Error())
			return m, nil
		}
		// Same transition ladder as the old metadata tick: still-changing →
		// Running; settled with a prompt → Prompting; settled → Ready.
		target := session.Ready
		if msg.updated {
			target = session.Running
		} else if msg.hasPrompt {
			target = session.Prompting
		}
		if err := msg.instance.TransitionTo(target); err != nil {
			log.For("app").Warn("event.transition_failed", "instance", msg.instance.Title, "to", target.String(), "err", err.Error())
		}
		m.updateTabBarStatuses()
		if msg.updated {
			// One sample of changed content cannot distinguish "still
			// working" from "finished a burst and idled" — under the
			// emulator this was the only sample per burst, so Running
			// latched on idle agents (and masked visible prompts, since
			// updated wins over hasPrompt). Re-sample until a detection
			// sees unchanged content and settles to Ready/Prompting.
			return m, m.maybeRedetect(msg.instance.TmuxSessionName())
		}
		return m, nil
	case ptyDeadMsg:
		var cmds []tea.Cmd
		// If the dead session backs the inline-attached pane, exit attach
		// immediately (the fast path for what the preview tick used to poll).
		if m.state == stateInlineAttach {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				cmds = append(cmds, tea.RequestWindowSize)
			}
		}
		inst := m.instanceForSession(msg.session)
		if inst == nil || inst == m.attachingInstance || !statusEligible(inst) {
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		cmds = append(cmds, verifyDeadCmd(inst))
		return m, tea.Batch(cmds...)
	case deadVerifiedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		_ = m.applyLiveness(msg.instance, msg.tmuxAlive, msg.ptmxAlive)
		m.updateTabBarStatuses()
		return m, m.instanceChanged()
	case bellMsg:
		if inst := m.instanceForSession(msg.session); inst != nil && inst != m.list.GetSelectedInstance() {
			inst.SetBellPending(true)
			// A bell from a background workspace is the canonical
			// peer-attention signal — refresh the rail's peer summaries
			// immediately instead of waiting for the next status event.
			// (Tab statuses don't consume bells, so no full
			// updateTabBarStatuses here.)
			m.refreshPeerSections()
		}
		return m, nil
	case tea.FocusMsg:
		m.hostFocused = true
		m.forwardFocus(true)
		return m, nil
	case tea.BlurMsg:
		m.hostFocused = false
		m.forwardFocus(false)
		return m, nil
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
			status := inst.GetStatus()
			// Recoverable placeholders are ephemeral orphan-review rows:
			// they report Started() (so recover/discard can reach their
			// handles) but must never be driven by the tick — RepairPtmx
			// would attach a PTY and TransitionTo(Running) would promote a
			// never-confirmed orphan past the explicit recover flow.
			// Loading rows are likewise owned by an in-flight
			// Start/Resume/Recover: probing them mid-setup reads a dead
			// tmux session and force-flips them to Paused under the op.
			if inst.Started() && !inst.Paused() && status != session.Deleting && status != session.Recoverable && status != session.Loading {
				active = append(active, inst)
			}
		}

		// Inline-attach liveness backstop (the preview tick used to check
		// this every 100ms in event mode; ptyDeadMsg is the fast path now,
		// this tick is the safety net for deaths that never EOF'd the PTY).
		var cmds []tea.Cmd
		if m.state == stateInlineAttach {
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				cmds = append(cmds, tea.RequestWindowSize)
			}
		}

		// Fan out I/O off the update goroutine. A stalled tmux or git process
		// must not block the UI loop — gatherMetadataCmd runs wg.Wait() inside
		// a background Cmd and returns the results via metadataReadyMsg.
		cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty()))

		// Workbench follow scan rides the health tick: cheap stat-walk
		// of the selected worktree, guarded stale on delivery.
		if m.viewMode == viewWorkbench {
			if scan := m.workbenchScanCmd(); scan != nil {
				cmds = append(cmds, scan)
			}
		}
		return m, tea.Batch(cmds...)
	case metadataReadyMsg:
		// Apply results on main thread.
		for _, r := range msg.results {
			if !m.applyLiveness(r.instance, r.tmuxAlive, r.ptmxAlive) {
				continue
			}
			// Event-mode instances get their status ladder from quiet
			// events (statusDetectedMsg); running it here too would fight
			// that pipeline with stale zero-valued results.
			if !r.emulatorDriven {
				if r.updated {
					if err := r.instance.TransitionTo(session.Running); err != nil {
						log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Running", "err", err.Error())
					}
				} else {
					if r.hasPrompt {
						if err := r.instance.TransitionTo(session.Prompting); err != nil {
							log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Prompting", "err", err.Error())
						}
					} else {
						if err := r.instance.TransitionTo(session.Ready); err != nil {
							log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Ready", "err", err.Error())
						}
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
	case wbScanMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title || msg.err != nil {
			return m, nil
		}
		md := m.workbench.Markdown
		if !md.Following() || md.Editing() {
			return m, nil
		}
		if msg.path == "" {
			md.Clear()
			return m, nil
		}
		if msg.path == md.Path() && !msg.mtime.After(md.Mtime()) {
			return m, nil
		}
		return m, loadMarkdownCmd(title, msg.path, true)
	case wbLoadMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title {
			return m, nil
		}
		if msg.err != nil {
			// File vanished between scan and read (agent moved it):
			// clear and let the next tick's scan re-resolve.
			m.workbench.Markdown.Clear()
			return m, nil
		}
		if m.workbench.Markdown.Editing() {
			return m, nil // never clobber an open editor
		}
		m.workbench.Markdown.SetDocument(msg.path, msg.raw, msg.mtime)
		m.workbench.Markdown.SetFollowing(msg.follow)
		return m, nil
	case wbFilesMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title || msg.err != nil {
			return m, nil
		}
		m.workbench.SetFiles(msg.root, msg.paths)
		return m, nil
	case tea.MouseWheelMsg:
		// v1 simplification: the wheel hit-tests below (listWidth /
		// agentBottomY) describe the focus layout and are meaningless
		// over the overview grid, so overview ignores the mouse entirely.
		if m.viewMode == viewOverview {
			return m, nil
		}
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
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown {
			// List cursor — works regardless of session state.
			if m.listWidth > 0 && mouse.X < m.listWidth {
				switch mouse.Button {
				case tea.MouseWheelUp:
					m.list.Up()
				case tea.MouseWheelDown:
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
				switch mouse.Button {
				case tea.MouseWheelUp:
					m.splitPane.ScrollDiffUp()
				case tea.MouseWheelDown:
					m.splitPane.ScrollDiffDown()
				}
			// With the terminal pane hidden the agent owns the whole
			// right-hand column, so wheel events below the anchor
			// (bottom border/status rows) go to the agent instead of
			// scrolling an invisible terminal.
			case mouse.Y <= m.agentBottomY || m.splitPane.IsTerminalHidden():
				switch mouse.Button {
				case tea.MouseWheelUp:
					m.splitPane.ScrollAgentUp()
				case tea.MouseWheelDown:
					m.splitPane.ScrollAgentDown()
				}
			default:
				switch mouse.Button {
				case tea.MouseWheelUp:
					m.splitPane.ScrollTerminalUp()
				case tea.MouseWheelDown:
					m.splitPane.ScrollTerminalDown()
				}
			}
		}
		return m, nil
	case tea.MouseClickMsg:
		// v1 simplification: no mouse in overview — a drag here would
		// BeginSelection over the hidden focus layout.
		if m.viewMode == viewOverview {
			return m, nil
		}
		mouse := msg.Mouse()
		// Interact mode: defer the left button — a drag becomes a Loom selection,
		// a plain click is forwarded into the agent.
		if m.state == stateInlineAttach {
			m.interactMouseClick(mouse)
			return m, nil
		}
		// Nav: left-click focuses the pane and anchors a drag-selection.
		if m.state != stateDefault || mouse.Button != tea.MouseLeft {
			return m, nil
		}
		m.splitPane.ClearSelections()
		m.dragging = false
		if m.listWidth > 0 && mouse.X < m.listWidth {
			return m, nil // left list panel — not a content selection
		}
		if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.tabBar.Height()); ok {
			m.setPaneFocus(pane)
			m.splitPane.BeginSelection(pane, row, col)
			m.dragging = true
			m.dragPane = pane
		}
		return m, nil
	case tea.MouseMotionMsg:
		// v1 simplification: no mouse in overview (see MouseClickMsg).
		if m.viewMode == viewOverview {
			return m, nil
		}
		mouse := msg.Mouse()
		// Interact mode: a left-drag becomes a Loom selection (never forwarded,
		// so it can't trigger tmux's copy-mode).
		if m.state == stateInlineAttach {
			m.interactMouseMotion(mouse)
			return m, nil
		}
		// Nav: extend the active drag-selection, clamped to its originating pane.
		if !m.dragging {
			return m, nil
		}
		if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.tabBar.Height()); ok && pane == m.dragPane {
			m.splitPane.ExtendSelection(m.dragPane, row, col)
		}
		return m, nil
	case tea.MouseReleaseMsg:
		// v1 simplification: no mouse in overview (see MouseClickMsg).
		// A drag can straddle the toggle (tab pressed with the button
		// held), so drop any in-flight drag state instead of letting a
		// stale selection resume after returning to focus mode.
		if m.viewMode == viewOverview {
			m.dragging = false
			m.splitPane.ClearSelections()
			return m, nil
		}
		// Interact mode: finalize a drag-selection (copy) or forward a plain click.
		if m.state == stateInlineAttach {
			return m, m.interactMouseRelease()
		}
		// Nav: end the drag — copy a non-empty selection; a plain click clears.
		if !m.dragging {
			return m, nil
		}
		m.dragging = false
		text := m.splitPane.SelectedText(m.dragPane)
		if text == "" {
			m.splitPane.ClearSelections()
			return m, nil
		}
		return m, copyToClipboard(text)
	case tea.PasteMsg:
		// Interact mode: bracketed-paste into the focused pane's agent/terminal.
		// In other states, textinput overlays consume their own paste.
		if m.state == stateInlineAttach {
			m.pasteToFocused(msg.Content)
		}
		return m, nil
	case clipboardCopiedMsg:
		if msg.err != nil {
			log.For("clipboard").Info("clipboard.fallback_failed", "err", msg.err)
		} else {
			log.For("clipboard").Debug("clipboard.copied", "runes", msg.n)
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
	case tea.KeyPressMsg:
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
		m.list.RemoveInstanceByTitle(msg.title)
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
		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged())
	case showHelpScreenMsg:
		m.menu.SetState(ui.StateDefault)
		return m.showHelpScreen(msg.helpType, nil)
	case recoverDoneMsg:
		if msg.err != nil {
			// Put the row back into Recoverable so the user can retry r
			// (runRecoverSelected flipped it to Loading for the spinner).
			for _, inst := range m.list.GetInstances() {
				if inst.Title == msg.oldTitle {
					if terr := inst.TransitionTo(session.Recoverable); terr != nil {
						log.For("app").Warn("recover.revert_failed", "title", msg.oldTitle, "err", terr)
					}
					break
				}
			}
			return m, m.handleError(fmt.Errorf("recover %s: %w", msg.oldTitle, msg.err))
		}
		m.list.RemoveInstanceByTitle(msg.oldTitle)
		m.list.AddInstance(msg.recovered)
		m.list.SelectInstance(msg.recovered)
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			log.For("app").Error("recover.save_failed", "title", msg.recovered.Title, "err", err)
		}
		// Recovery is otherwise invisible when fast — confirm it, and be
		// explicit about the degraded case where both the tmux session and
		// worktree were already gone and adoption could only mark the
		// record Paused (resume rebuilds the worktree from the branch).
		if msg.recovered.GetStatus() == session.Paused {
			m.errBox.SetInfo(fmt.Sprintf("Recovered '%s' as paused — its session and worktree were gone; branch preserved, press r to resume", msg.recovered.Title))
		} else {
			m.errBox.SetInfo(fmt.Sprintf("Recovered session '%s'", msg.recovered.Title))
		}
		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged())
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
		m.attachingInstance = inst
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
		cmds = append(cmds, tea.RequestWindowSize, m.instanceChanged())
		return m, tea.Batch(cmds...)
	case attachDoneMsg:
		// tea.ExecProcess has restored the terminal. Rebuild the preview PTYs
		// so live capture resumes. Errors here are logged — the session
		// itself is untouched (only our attach client failed to reopen), so
		// the metadata tick's ptmx self-heal (metadataReadyMsg) will retry
		// this on the next tick now that attachingInstance is cleared below.
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
		if m.attachingInstance == msg.instance {
			m.attachingInstance = nil
		}
		m.state = stateDefault
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(msg.err))
		}
		cmds = append(cmds, tea.RequestWindowSize, m.instanceChanged())
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

		if err := m.registry.UpdateLastUsed(ws.Name); err != nil {
			log.For("app").Debug("registry.update_last_used_failed", "workspace", ws.Name, "err", err)
		}

		// Focus the just-registered slot so the user sees its
		// instances immediately. activateWorkspace appends to the
		// end, so the new slot is at len-1 (not 0 — the prior
		// loadSlot(0) would have surfaced an unrelated tab).
		// Flush any pending split-ratio save for the outgoing slot
		// before loadSlot swaps the active workspace context.
		m.flushPendingRatioSaves()
		m.focusedSlot = len(m.slots) - 1
		m.loadSlot(m.focusedSlot)
		m.updateTabBarStatuses()
		m.showRecoverySummary(m.slots[m.focusedSlot].recovery)

		return m, tea.RequestWindowSize
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			popped := m.list.PopSelectedForKill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged(), backgroundKillCmd(popped))
		}

		// Save after successful start
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			return m, m.handleError(err)
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
			m.setPaneFocus(ui.FocusAgent)
			m.splitPane.SetInlineAttach(true)
			m.state = stateInlineAttach
			m.menu.SetState(ui.StateInlineAttach)
		}

		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// recoverySummary tallies what a reconcileOrphans pass did, for the
// non-blocking one-line summary shown to the user.
type recoverySummary struct {
	cleaned int // stale worktrees auto-removed
	review  int // Recoverable entries added to the list
	failed  int // records that failed reconcile (storage unrecovered cache)
}

func (s recoverySummary) empty() bool { return s.cleaned == 0 && s.review == 0 && s.failed == 0 }

func (s recoverySummary) String() string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var parts []string
	if s.cleaned > 0 {
		parts = append(parts, "cleaned "+plural(s.cleaned, "stale worktree", "stale worktrees"))
	}
	if s.review > 0 {
		verb := "need"
		if s.review == 1 {
			verb = "needs"
		}
		parts = append(parts, fmt.Sprintf("%s %s review (in list)", plural(s.review, "session", "sessions"), verb))
	}
	if s.failed > 0 {
		// These records are preserved on disk and retried next launch,
		// but never appear in the list — without this line they would
		// look like silently lost sessions.
		parts = append(parts, fmt.Sprintf("%s failed to load (kept; see loom.log)", plural(s.failed, "session", "sessions")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Recovery: " + strings.Join(parts, " · ")
}

// persistableInstances filters out instances whose state should not reach disk:
// Ready (mid-creation), Deleting (kill in progress, about to be removed via
// DeleteInstance), and Recoverable (an orphan surfaced inline; it is re-derived
// from disk each load and adopted only on explicit recovery, so persisting it
// would resurrect a never-confirmed entry). All other statuses — Loading,
// Running, Paused — are persisted so that a crash or quit during the kill
// window cannot orphan a live worktree from its JSON record.
func persistableInstances(instances []*session.Instance) []*session.Instance {
	var result []*session.Instance
	for _, inst := range instances {
		status := inst.GetStatus()
		if status == session.Ready || status == session.Deleting || status == session.Recoverable {
			continue
		}
		result = append(result, inst)
	}
	return result
}

// claimedWorktreePaths returns the set of worktree paths already accounted
// for: live instances plus storage's unrecovered cache (records that failed
// reconcile but remain tracked in state.json). Orphan discovery skips these.
func claimedWorktreePaths(claimed []*session.Instance, storage *session.Storage) map[string]bool {
	paths := make(map[string]bool, len(claimed))
	for _, inst := range claimed {
		wt, err := inst.GetGitWorktree()
		if err != nil || wt == nil {
			continue
		}
		if p := wt.GetWorktreePath(); p != "" {
			paths[p] = true
		}
	}
	if storage != nil {
		for p := range storage.UnrecoveredWorktreePaths() {
			paths[p] = true
		}
	}
	return paths
}

// reconcileOrphans discovers orphaned worktrees for one workspace, auto-cleans
// stale leftovers, and adds inline Recoverable entries for orphans that need a
// human decision. It mutates list (adds Recoverable instances) and returns a
// summary for the caller to surface. Safe to run on any workspace-load path.
func (m *home) reconcileOrphans(cfgDir, program string, list *ui.List, storage *session.Storage, cmdExec cmd2.Executor) recoverySummary {
	var summary recoverySummary
	orphans, err := session.DiscoverOrphans(cfgDir, claimedWorktreePaths(list.GetInstances(), storage), cmdExec)
	if err != nil {
		log.For("app").Warn("orphan_discovery_failed", "cfg_dir", cfgDir, "err", err)
		return summary
	}
	for _, cand := range orphans {
		switch cand.Disposition() {
		case session.DisposeClean:
			if err := session.RemoveOrphanWorktree(cand.RepoPath, cand.WorktreePath); err != nil {
				log.For("app").Warn("orphan_autoclean_failed", "worktree", cand.WorktreePath, "err", err)
				continue
			}
			summary.cleaned++
		case session.DisposeReview:
			data := session.InstanceDataFromOrphan(cand, program)
			data.Status = session.Recoverable
			inst, err := session.FromInstanceData(data, cfgDir)
			if err != nil {
				log.For("app").Warn("orphan_placeholder_failed", "title", cand.Title, "err", err)
				continue
			}
			list.AddInstance(inst)
			summary.review++
		}
	}
	// Records that failed reconcile at load time live only in the storage
	// cache — surface their count so they don't read as lost sessions.
	if storage != nil {
		summary.failed = len(storage.UnrecoveredTitles())
	}
	return summary
}

// showRecoverySummary surfaces a reconcile summary on the error bar as a
// non-alarming info line. No-op when nothing happened.
func (m *home) showRecoverySummary(s recoverySummary) {
	if s.empty() {
		return
	}
	m.errBox.SetInfo(s.String())
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
	// Persist any not-yet-flushed split resize before exit (the throttle
	// tick may still be in flight; covers the single-slot path too, where
	// saveCurrentSlot below is a no-op). The workbench ratio flushes the
	// same way — it is only written on workbench exit otherwise.
	m.flushPendingRatioSaves()
	m.flushWorkbenchRatio()
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

func (m *home) handleMenuHighlighting(msg tea.KeyPressMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateNew || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateMergePicker || m.state == stateLaunchOptions {
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
func (m *home) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
	case stateSettings:
		return handleStateSettingsKey(m, msg)
	case stateConfirm:
		return handleStateConfirmKey(m, msg)
	case stateFileExplorer:
		return handleStateFileExplorerKey(m, msg)
	case stateMergePicker:
		return handleStateMergePickerKey(m, msg)
	case stateLaunchOptions:
		return handleStateLaunchOptionsKey(m, msg)
	default:
		return handleStateDefaultKey(m, msg)
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	// Workbench heal: the deep-dive lost its instance (last one
	// killed) — drop back to focus rather than render a dead panel.
	if m.viewMode == viewWorkbench && selected == nil {
		m.cleanupWorkbench()
	}

	// Seen in the grid ≠ attended: in overview the cursor walks the
	// attention-sorted grid, and clearing the bell on landing would
	// reshuffle the sort order under the cursor mid-walk. The bell
	// clears when the user actually drops into focus on the card —
	// the enter/esc handlers flip viewMode to viewFocus before calling
	// instanceChanged, so the landing itself performs the clear.
	if selected != nil && m.viewMode != viewOverview {
		selected.SetBellPending(false)
	}

	// Re-apply the newly selected instance's persisted split ratio (or
	// the default) so switching sessions restores its layout.
	m.applyStoredRatio(selected)

	newFocusTitle := ""
	if selected != nil {
		newFocusTitle = selected.Title
	}
	if newFocusTitle != m.lastFocusTitle {
		// The user's attention moved to a different instance: the old pane
		// loses focus, the new one gains it (host focus permitting).
		if m.hostFocused && m.splitPane.GetFocusedPane() == ui.FocusAgent {
			if prev := m.list.GetInstanceByTitle(m.lastFocusTitle); prev != nil {
				prev.ForwardFocus(false)
			}
		}
		m.lastFocusTitle = newFocusTitle
		if m.hostFocused && selected != nil && m.splitPane.GetFocusedPane() == ui.FocusAgent {
			selected.ForwardFocus(true)
		}
	}

	m.splitPane.UpdateDiff(selected)
	m.splitPane.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// Workbench retarget: selection moved while deep-diving (]/[ jump,
	// wheel over the rail) — point the panel at the new session and, on
	// an actual title change, kick a fresh scan + files load.
	// DiffPane.SetDiff carries its own nil/unstarted fallbacks, so no
	// extra guard is needed (mirrors SplitPane.UpdateDiff's blind call).
	var wbRefresh tea.Cmd
	if m.viewMode == viewWorkbench && selected != nil {
		prevTitle := m.workbench.SessionTitle()
		m.workbench.SetSession(selected.Title, selected.GetWorktreePath())
		m.workbench.Diff().SetDiff(selected)
		if prevTitle != selected.Title {
			wbRefresh = m.workbenchRefresh()
		}
	}

	if err := m.splitPane.UpdateAgent(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.splitPane.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return wbRefresh
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
type killInstanceMsg struct {
	title string
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

// showHelpScreenMsg asks Update to open a help overlay. Emitted from
// tea.Cmd closures, which run off the main goroutine and therefore must
// not call showHelpScreen (it mutates m.state/overlay and writes app
// state to disk) directly.
type showHelpScreenMsg struct {
	helpType helpText
}

// recoverDoneMsg is returned after a Recoverable orphan is adopted into a
// live instance off the UI goroutine. The handler swaps the inline
// placeholder for the recovered instance and persists.
type recoverDoneMsg struct {
	oldTitle  string
	recovered *session.Instance
	err       error
}

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

// maxWorkspaceTerminalRestartFailures bounds how many consecutive metadata
// ticks the workspace-terminal auto-restart path (metadataReadyMsg) will
// retry a dead tmux session before giving up and marking it Paused instead.
// Without this, a permanently broken Program (e.g. a stale command left
// over from a since-changed launch mechanism) restart-loops forever at
// tick cadence — 500ms tickUpdateMetadataCmd below, so ~1.5s of thrash
// before this trips. Restart's own Start(true) blocks until the session is
// confirmed up before returning, so a genuinely successful restart should
// never even reach 2 consecutive misses; this is slack for one flaky
// blip, not a real recovery window.
const maxWorkspaceTerminalRestartFailures = 3

// tickUpdateMetadataCmd drives the health tick. In event mode (emulator
// path) it is a slow belt-and-braces sweep — liveness, ptmx self-heal, and
// diff stats — because status detection rides pane events instead. On the
// snapshot path it keeps the legacy 500ms cadence and does everything.
var tickUpdateMetadataCmd = func() tea.Msg {
	if tmux.EmulatorEnabled() {
		time.Sleep(3 * time.Second)
	} else {
		time.Sleep(500 * time.Millisecond)
	}
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
func gatherMetadataCmd(active []*session.Instance, selected *session.Instance, dirty map[string]bool) tea.Cmd {
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
				r.ptmxAlive = instance.PtmxAlive()

				// Event-mode instances get status from quiet events; the
				// subprocess scan only remains for the snapshot path.
				r.emulatorDriven = instance.HasEmulator()
				if !r.emulatorDriven {
					r.updated, r.hasPrompt, r.captureErr = instance.CaptureAndProcessStatus()
				}

				wantFull := instance == selected
				tmuxUpdated := r.updated || dirty[instance.TmuxSessionName()]
				if !instance.ShouldRefreshDiff(tmuxUpdated, wantFull) {
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

// applyLiveness reacts to one instance's health-probe result: dead tmux →
// pause (or restart a workspace terminal, with the existing circuit
// breaker); live tmux but dead attach PTY → RepairPtmx self-heal. Returns
// false when the instance was found dead (so callers can stop treating it
// as running). Must run on the Update goroutine.
func (m *home) applyLiveness(inst *session.Instance, tmuxAlive, ptmxAlive bool) (alive bool) {
	if !tmuxAlive {
		if inst.IsWorkspaceTerminal {
			if failures := inst.RecordRestartFailure(); failures >= maxWorkspaceTerminalRestartFailures {
				// The session died again immediately after every
				// recent Restart (e.g. a permanently broken Program
				// string) — restarting further would just loop
				// forever at tick cadence. Give up like a regular
				// instance would. RestartWithOptions/Resume are both
				// gated off for workspace terminals (see
				// selectedPausedNotWorkspace/selectedResumableNotWorkspace
				// in intents.go), so recovering today means killing
				// this instance (a fresh one is auto-created from
				// current config on next workspace activation) or
				// fixing Program on disk and relaunching Loom.
				log.For("app").Error("workspace_terminal.restart_circuit_tripped", "title", inst.Title, "consecutive_failures", failures)
				if err := inst.TransitionTo(session.Paused); err != nil {
					log.For("app").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
				}
				return false
			}
			log.For("app").Warn("workspace_terminal.tmux_died_restarting", "title", inst.Title)
			if err := inst.Restart(); err != nil {
				log.For("app").Error("workspace_terminal.restart_failed", "title", inst.Title, "err", err)
			}
			return false
		}
		log.For("app").Warn("tick.tmux_gone_marking_paused", "title", inst.Title)
		if err := inst.TransitionTo(session.Paused); err != nil {
			log.For("app").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
		}
		return false
	}
	inst.ResetRestartFailures()
	if !ptmxAlive && inst != m.attachingInstance {
		// Session exists but Loom's own attach client is gone (e.g. a
		// reattach failed after full-screen attach returned). Nothing
		// else ever retries this, so self-heal here — same shape as
		// the workspace-terminal restart above, but at the PTY layer.
		log.For("app").Warn("tick.ptmx_dead_repairing", "title", inst.Title)
		if err := inst.RepairPtmx(); err != nil {
			log.For("app").Error("tick.ptmx_repair_failed", "title", inst.Title, "err", err)
		}
	}
	return true
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.For("app").Error("handle_error", "err", err)
	m.errBox.SetError(err)
	// Scale visibility with message length: a multi-line git error can't
	// be read in the flat 3 seconds that suits a one-liner. Every error
	// is also in loom.log, but the toast is the only surface most users
	// see.
	duration := 3*time.Second + time.Duration(len(err.Error())/40)*time.Second
	if duration > 10*time.Second {
		duration = 10 * time.Second
	}
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(duration):
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
			tea.RequestWindowSize,
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
	// Loom-context injection: keep the config-dir prompt files current and
	// sync the global enabled flag on every workspace load, before any
	// Claude session (workspace terminal, crash-restart, resume) launches.
	session.SetLoomContextEnabled(appConfig.LoomContextEnabled())
	if err := session.WriteLoomContextFiles(wsCtx.ConfigDir); err != nil {
		log.For("app").Warn("loom_context.write_failed", "err", err.Error())
	}
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to create storage for workspace %s: %w", ws.Name, err)
	}

	cmdExec := cmd2.MakeExecutor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		// Fail closed: do NOT proceed to build an empty slot. Continuing
		// here would append a slot with zero instances, and the next
		// SaveInstances for it would overwrite a possibly-recoverable
		// (e.g. transiently unreadable or corrupt) state.json with only
		// the survivors — silent per-workspace data loss. The classic
		// startup path already fails closed this way; mirror it. The slot
		// is simply not opened, leaving state.json on disk untouched.
		return fmt.Errorf("load instances for workspace %s: %w", ws.Name, err)
	}
	// Orphan discovery runs here so every workspace-load path (startup
	// picker, mid-session toggle, restore, registration) surfaces
	// recovered sessions identically — no restart required.

	list := ui.NewList(&m.spinner)
	hasWorkspaceTerminal := false
	for _, inst := range instances {
		if inst.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		list.AddInstance(inst)
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
		// loom_<wtTitle> alive without persisting the instance. The
		// multi-tab restore sweep (CleanupOrphanedSessions in
		// restoreSavedWorkspaces) only runs AFTER every slot has
		// activated — but the workspace-terminal Start below happens now,
		// during activation, and would fail with "session already exists"
		// against that orphan. Kill it here first so Start gets a clean
		// name; the later sweep handles any other stragglers.
		if err := session.KillTmuxSessionByTitle(wtTitle, cmdExec); err != nil {
			log.For("app").Debug("workspace_terminal.orphan_kill", "workspace", ws.Name, "err", err.Error())
		}

		wtOpts := launchOptionsFromConfig(appConfig)
		if m.remoteControlBlocked(effectiveRemoteControl(wtOpts), appConfig.GetProgram()) {
			m.errBox.SetInfo("remote control off: " + m.rcAuth.Reason)
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             applyLaunchOptions(wtOpts, m.rcAuth, appConfig.GetProgram(), wtTitle),
			HeadroomProxy:       wtOpts.HeadroomProxy,
			CacheTTL1h:          wtOpts.CacheTTL1h,
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
		if wtErr != nil {
			log.For("app").Error("workspace_terminal.create_failed", "workspace", ws.Name, "err", wtErr)
		} else {
			list.AddInstance(wtInstance)
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

	recovery := m.reconcileOrphans(wsCtx.ConfigDir, appConfig.GetProgram(), list, storage, cmdExec)
	m.slots = append(m.slots, workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: appConfig,
		appState:  state,
		list:      list,
		splitPane: splitPane,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), splitPane.Terminal()),
		recovery:  recovery,
	})
	return nil
}

// deactivateWorkspace saves and removes a workspace slot by name.
// Returns an error if SaveInstances fails; in that case the slot is
// kept in memory so the user can retry rather than losing access to
// unpersisted session state. This matches handleQuit's policy that
// silent data loss on slot teardown is worse than a sticky tab.
func (m *home) deactivateWorkspace(name string) error {
	idx := -1
	for i, slot := range m.slots {
		if slot.wsCtx.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}

	slot := m.slots[idx]
	if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
		log.For("app").Error("workspace.save_failed", "name", name, "err", err)
		return fmt.Errorf("failed to save workspace %s: %w", name, err)
	}

	m.slots = append(m.slots[:idx], m.slots[idx+1:]...)

	if m.focusedSlot >= len(m.slots) && len(m.slots) > 0 {
		m.focusedSlot = len(m.slots) - 1
	} else if m.focusedSlot > idx {
		m.focusedSlot--
	}
	return nil
}

// saveCurrentSlot writes the home's active UI fields back into the focused slot.
func (m *home) saveCurrentSlot() {
	// Workbench mode does not survive a slot switch: tear it down while
	// the departing slot's splitPane/appState are still active, so the
	// terminal-hidden restore and ratio flush land on the right slot.
	m.cleanupWorkbench()
	// Flush pending split-ratio saves into THIS slot's state.json before
	// the slot swap: the pending map would otherwise survive the switch
	// and the armed throttle tick would drain slot A's title→ratio into
	// slot B's prefs — a durable wrong value when workspaces share a
	// title. One synchronous write per slot switch is mutateUIPrefs'
	// blessed rare-toggle case; the in-flight tick then finds the map
	// empty and simply disarms.
	m.flushPendingRatioSaves()
	if len(m.slots) == 0 {
		return
	}
	s := &m.slots[m.focusedSlot]
	s.list = m.list
	s.splitPane = m.splitPane
	s.workbench = m.workbench
	s.storage = m.storage
	s.appConfig = m.appConfig
	s.appState = m.appState
}

// loadSlot copies a slot's fields onto home and updates the active workspace context.
func (m *home) loadSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) {
		return
	}
	// Second workbench choke point (idempotent — saveCurrentSlot already
	// ran it on most paths): the workspace-registration flow reaches
	// loadSlot without a preceding saveCurrentSlot, and cleanup here
	// still sees the departing slot's fields (the swap is below).
	m.cleanupWorkbench()
	slot := m.slots[idx]
	m.focusedSlot = idx
	m.activeCtx = slot.wsCtx
	m.list = slot.list
	m.splitPane = slot.splitPane
	// The workbench travels with its splitPane (its terminal tab shows
	// the slot's shared TerminalPane). Nil-guarded for test slots built
	// without one.
	if slot.workbench != nil {
		m.workbench = slot.workbench
	}
	m.storage = slot.storage
	m.appConfig = slot.appConfig
	m.appState = slot.appState
	m.list.SetWorkspaceName(slot.wsCtx.Name)
	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	// Resize immediately using the now-correct tab bar height. Without this,
	// the first View() after a workspace switch uses components pre-sized when
	// the tab bar had 0 names (height=0 instead of 3), producing 3 extra lines
	// that Bubble Tea clips from the top, cutting off the workspace tab bar.
	// applyUIPrefs below runs the full layout whenever appState exists, so
	// this interim resize survives only for bare test homes — a redundant
	// SetSize here would loop tmux SetDetachedSize subprocess calls twice
	// per workspace switch.
	if m.appState == nil && m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		m.list.SetSize(listWidth, contentHeight)
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
	m.refreshPeerSections()
	// The freshly-loaded slot's components carry no layout prefs yet;
	// apply the persisted rail/terminal/ratio state (re-runs the
	// window-size layout when sized).
	m.applyUIPrefs()
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

	var activationErrors []string
	var deactivationErrors []string

	// 1. Activate new workspaces first (safe — adds to slots without removing).
	currentNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		currentNames[slot.wsCtx.Name] = true
	}
	for _, ws := range desired {
		if !currentNames[ws.Name] {
			if err := m.activateWorkspace(ws); err != nil {
				activationErrors = append(activationErrors,
					fmt.Sprintf("%s: %v", ws.Name, err))
			}
		}
	}

	// 2. Deactivate slots not in desired (reverse order to keep indices stable).
	// Slots whose save fails stay in m.slots; the user is told via handleError below.
	for i := len(m.slots) - 1; i >= 0; i-- {
		if !desiredNames[m.slots[i].wsCtx.Name] {
			if err := m.deactivateWorkspace(m.slots[i].wsCtx.Name); err != nil {
				deactivationErrors = append(deactivationErrors,
					fmt.Sprintf("%s: %v", m.slots[i].wsCtx.Name, err))
			}
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
	if len(m.slots) > 0 {
		m.showRecoverySummary(m.slots[m.focusedSlot].recovery)
	}

	// 4. Surface activation/deactivation errors to the user.
	var msgs []string
	if len(activationErrors) > 0 {
		msgs = append(msgs, fmt.Sprintf("failed to activate: %s",
			strings.Join(activationErrors, "; ")))
	}
	if len(deactivationErrors) > 0 {
		msgs = append(msgs, fmt.Sprintf("failed to deactivate: %s",
			strings.Join(deactivationErrors, "; ")))
	}
	if len(msgs) > 0 {
		return tea.Batch(tea.RequestWindowSize,
			m.handleError(fmt.Errorf("%s", strings.Join(msgs, "; "))))
	}
	return tea.RequestWindowSize
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

	m.list = ui.NewList(&m.spinner)
	for _, inst := range instances {
		m.list.AddInstance(inst)
	}

	// Clear registry's open-tab list so the next launch lands in
	// global mode rather than auto-restoring tabs the user just closed.
	if m.registry != nil {
		if err := m.registry.SetOpenWorkspaces(nil); err != nil {
			log.For("app").Warn("clear_open_workspaces_failed", "err", err)
		}
	}

	m.tabBar.SetWorkspaces(nil, 0)

	// Apply the global state's persisted layout prefs so global mode
	// renders with its own rail/terminal/ratio settings — not the
	// previous workspace's — and the display agrees with where
	// mutateUIPrefs will write. When sized, this re-runs the full
	// window-size layout, which also resizes the components for the
	// now-zero-height tab bar.
	m.applyUIPrefs()

	return tea.RequestWindowSize
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
	m.refreshPeerSections()
}

// refreshPeerSections rebuilds the rail's peer-workspace summaries from
// the non-focused slots' live lists. Main-goroutine only (reads slot
// lists, which are only mutated there).
func (m *home) refreshPeerSections() {
	if len(m.slots) <= 1 {
		m.list.SetPeerSections(nil)
		return
	}
	peers := make([]ui.PeerSection, 0, len(m.slots)-1)
	for i, slot := range m.slots {
		if i == m.focusedSlot {
			continue
		}
		peers = append(peers, m.peerSectionFor(slot))
	}
	m.list.SetPeerSections(peers)
}

// enterOverview switches to overview mode and seeds the cursor.
// Main-goroutine only.
func (m *home) enterOverview() {
	m.viewMode = viewOverview
	m.seedOverviewCursor()
}

// fleetPos is one selectable card in fleet display order.
type fleetPos struct{ slot, inst int }

// slotList resolves the live list for a slot index: the focused slot's
// list is hoisted onto m.list, every other slot keeps its own.
func (m *home) slotList(si int) *ui.List {
	if si == m.focusedSlot {
		return m.list
	}
	return m.slots[si].list
}

// fleetOrder flattens all loaded, non-collapsed slots into a single
// display-ordered, attention-sorted list of selectable positions,
// skipping Deleting instances. Mirrors overviewData's grouping so cursor
// motion matches what's on screen.
func (m *home) fleetOrder() []fleetPos {
	var out []fleetPos
	for _, si := range m.fleetSlotOrder() {
		if m.overview.IsCollapsed(m.slotGroupName(m.slots[si])) {
			continue
		}
		items := m.slotList(si).GetInstances()
		for _, idx := range ui.SortForOverview(items) {
			if items[idx].GetStatus() == session.Deleting {
				continue
			}
			out = append(out, fleetPos{slot: si, inst: idx})
		}
	}
	return out
}

// normalizeOverviewCursor re-anchors a cursor that no longer maps to a
// visible card (instance killed, group collapsed, slots changed) to the
// first selectable position in order. Reports whether it moved. The
// single staleness choke point: called from render (overviewData) and
// nav (moveCursor) so every event that invalidates the cursor is healed
// without per-event-site bookkeeping. No-op in classic mode, where the
// cursor is m.list's selection.
func (m *home) normalizeOverviewCursor(order []fleetPos) bool {
	for _, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			return false
		}
	}
	if len(order) == 0 {
		return false
	}
	m.overviewCursor = overviewCursor{slot: order[0].slot, inst: order[0].inst}
	return true
}

// seedOverviewCursor points the cursor at the focused slot's selection,
// or the first selectable fleet position if that isn't selectable.
func (m *home) seedOverviewCursor() {
	m.overviewCursor = overviewCursor{slot: m.focusedSlot, inst: m.list.SelectedIdx()}
	m.normalizeOverviewCursor(m.fleetOrder())
}

// focusCursorSlot makes the overview cursor's slot the focused slot and
// selects the cursor's instance. No-op fast path when already focused.
// The single primitive every cursor-committing overview action routes
// through, so they reuse focus-mode intents unchanged. Main-goroutine
// only.
func (m *home) focusCursorSlot() {
	c := m.overviewCursor
	if c.slot < 0 || c.slot >= len(m.slots) {
		return
	}
	if c.slot != m.focusedSlot {
		m.saveCurrentSlot()
		m.loadSlot(c.slot)
	}
	if c.inst >= 0 && c.inst < len(m.list.GetInstances()) {
		m.list.SetSelectedInstance(c.inst)
	}
}

// peerSectionFor summarizes one non-focused slot's instance statuses
// into a PeerSection for refreshPeerSections (rail). Main-goroutine
// only.
func (m *home) peerSectionFor(slot workspaceSlot) ui.PeerSection {
	p := ui.PeerSection{Name: slot.wsCtx.Name}
	for _, inst := range slot.list.GetInstances() {
		st := inst.GetStatus()
		switch {
		case st == session.Prompting || inst.BellPending():
			p.Attention++
		case st == session.Running || st == session.Loading:
			p.Running++
		default:
			// Paused/Deleting/unstarted intentionally count as idle.
			p.Idle++
		}
	}
	return p
}

// overviewGroupName is the label for the active group in overview mode:
// the workspace name, or "global" in classic/global mode (activeCtx nil
// or unnamed) so the header never renders empty and `z` still has a
// stable collapse key.
func (m *home) overviewGroupName() string {
	if m.activeCtx != nil && m.activeCtx.Name != "" {
		return m.activeCtx.Name
	}
	return "global"
}

// fleetSlotOrder returns slot indices in overview display order: focused
// slot first, then the rest alphabetical by workspace name.
func (m *home) fleetSlotOrder() []int {
	order := make([]int, 0, len(m.slots))
	if m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		order = append(order, m.focusedSlot)
	}
	rest := make([]int, 0, len(m.slots))
	for i := range m.slots {
		if i != m.focusedSlot {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		return m.slots[rest[a]].wsCtx.Name < m.slots[rest[b]].wsCtx.Name
	})
	return append(order, rest...)
}

// slotGroupName is the display name for a slot's overview group,
// falling back to "global" for the unnamed classic slot.
func (m *home) slotGroupName(slot workspaceSlot) string {
	if slot.wsCtx != nil && slot.wsCtx.Name != "" {
		return slot.wsCtx.Name
	}
	return "global"
}

// overviewGroupFor builds one OverviewGroup from a list's instances,
// deriving GroupEmpty/Order from the item count.
func overviewGroupFor(name string, items []*session.Instance) ui.OverviewGroup {
	g := ui.OverviewGroup{Name: name, Items: items, State: ui.GroupLoaded}
	if len(items) == 0 {
		g.State = ui.GroupEmpty
	} else {
		g.Order = ui.SortForOverview(items)
	}
	return g
}

// cursorFor translates the domain cursor position (an instance index)
// into the render cursor for group gi, defaulting to item 0 when the
// index is not found in the group's sorted order.
func cursorFor(gi, inst int, g ui.OverviewGroup) ui.OverviewCursor {
	for pos, idx := range g.Order {
		if idx == inst {
			return ui.OverviewCursor{Group: gi, Item: pos}
		}
	}
	return ui.OverviewCursor{Group: gi, Item: 0}
}

// overviewData assembles the multi-group overview over the open
// workspace slots: the focused slot first, then the remaining slots
// alphabetical by workspace name. It translates the domain cursor
// (slot,inst) into render coordinates (group,item). In classic/global
// mode (no slots) it renders the focused m.list as a single "global"
// group. Update-goroutine only.
func (m *home) overviewData() ui.OverviewData {
	cursor := ui.OverviewCursor{}

	// Classic/global mode: no slots loaded, render the focused m.list as
	// a single "global" group (matching the pre-fleet overview behavior).
	if len(m.slots) == 0 {
		items := m.list.GetInstances()
		g := overviewGroupFor(m.overviewGroupName(), items)
		cursor = cursorFor(0, m.list.SelectedIdx(), g)
		return ui.OverviewData{
			Groups:  []ui.OverviewGroup{g},
			Cursor:  cursor,
			Spinner: m.spinner.View(),
		}
	}

	// Heal a cursor invalidated since the last frame (instance killed,
	// group collapsed, slots changed) before translating it — a cursor
	// pointing into a collapsed group would window to a bogus offset.
	m.normalizeOverviewCursor(m.fleetOrder())

	slotOrder := m.fleetSlotOrder()
	groups := make([]ui.OverviewGroup, 0, len(slotOrder))
	for _, si := range slotOrder {
		slot := m.slots[si]
		g := overviewGroupFor(m.slotGroupName(slot), m.slotList(si).GetInstances())
		// Translate the domain cursor (slot,inst) → render cursor
		// (group,item) when this is the cursor's slot.
		if si == m.overviewCursor.slot {
			cursor = cursorFor(len(groups), m.overviewCursor.inst, g)
		}
		groups = append(groups, g)
	}

	return ui.OverviewData{Groups: groups, Cursor: cursor, Spinner: m.spinner.View()}
}

// moveCursor advances selection: list order in focus mode, fleet display
// order (across all groups) in overview mode. No wrap in the grid.
func (m *home) moveCursor(dir int) {
	if m.viewMode != viewOverview {
		if dir < 0 {
			m.list.Up()
		} else {
			m.list.Down()
		}
		return
	}
	// Classic/global mode (no slots): fleetSlotOrder is empty, so fleetOrder
	// yields nothing. Walk m.list in sorted overview order directly, mirroring
	// overviewData's classic fallback (which renders the cursor from m.list).
	if len(m.slots) == 0 {
		items := m.list.GetInstances()
		if len(items) == 0 {
			return
		}
		order := ui.SortForOverview(items)
		pos := 0
		for p, idx := range order {
			if idx == m.list.SelectedIdx() {
				pos = p
				break
			}
		}
		for i := 1; i <= len(order); i++ {
			np := pos + dir*i
			if np < 0 || np >= len(order) {
				return // no wrap in the grid
			}
			if items[order[np]].GetStatus() != session.Deleting {
				m.list.SetSelectedInstance(order[np])
				return
			}
		}
		return
	}
	order := m.fleetOrder()
	if len(order) == 0 {
		return
	}
	// A stale cursor re-anchors to the first visible card on this
	// keypress; stepping from a phantom position would skip a card.
	if m.normalizeOverviewCursor(order) {
		return
	}
	cur := 0
	for i, p := range order {
		if p.slot == m.overviewCursor.slot && p.inst == m.overviewCursor.inst {
			cur = i
			break
		}
	}
	np := cur + dir
	if np < 0 || np >= len(order) {
		return
	}
	m.overviewCursor = overviewCursor{slot: order[np].slot, inst: order[np].inst}
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

// slotNames returns the names of all active workspace slots — the set
// the tab bar shows and saveOpenWorkspaces persists.
func (m *home) slotNames() []string {
	names := make([]string, len(m.slots))
	for i, slot := range m.slots {
		names[i] = slot.wsCtx.Name
	}
	return names
}

// View implements tea.Model.
func (m *home) View() tea.View {
	// asView funnels every render path through one tea.View so alt-screen
	// and mouse cell-motion (previously tea.NewProgram options) are set
	// consistently on every frame.
	asView := func(content string) tea.View {
		v := tea.NewView(content)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		v.ReportFocus = true
		return v
	}
	// Overview replaces the whole rail+panes assembly. The file-explorer
	// state keeps the focus layout (its overlay replaces the right pane),
	// though it is currently unreachable from overview — 'f' is not in
	// overviewKeyAllowed — so the guard is belt-and-braces.
	var mainContent string
	if m.viewMode == viewOverview && m.state != stateFileExplorer {
		mainContent = m.overview.Render(m.overviewData())
	} else if m.viewMode == viewWorkbench && m.state != stateFileExplorer {
		// Workbench: agent split (terminal hidden) left, tabbed content
		// panel right. The rail is not rendered — listWidth is zeroed by
		// the sizing branch.
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, m.splitPane.String(), m.workbench.String())
	} else {
		listView := ""
		if !m.railHidden {
			listView = m.list.String()
		}
		// The file explorer is the only overlay that wholly replaces the
		// right pane rather than floating on top of it. It renders inline
		// via JoinHorizontal — instead of via PlaceOverlay below — so the
		// list stays visible alongside it (unless the rail is hidden).
		var rightContent string
		if m.state == stateFileExplorer && m.activeOverlay != nil {
			rightContent = m.activeOverlay.View()
		} else {
			rightContent = m.splitPane.String()
		}
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, listView, rightContent)
	}

	sections := []string{}
	if tabBarStr := m.tabBar.String(); tabBarStr != "" {
		sections = append(sections, tabBarStr)
	}

	// Bottom bar: quick input or inline attach hint replaces the status line
	if m.state == stateQuickInteract && m.quickInputBar != nil {
		// Quick input is 2 lines, replaces both status line and error box so panes don't shift.
		sections = append(sections, mainContent, m.quickInputBar.View())
	} else if m.state == stateInlineAttach {
		hint := inlineAttachHintStyle.Render("▶ CAPTURING INPUT  ·  ctrl+q to detach, then alt+a/alt+t for fullscreen")
		sections = append(sections, mainContent, hint, m.errBox.String())
	} else {
		hint := "tab overview · ] next waiting · \\ rail · ? help · q quit"
		if m.viewMode == viewOverview {
			hint = "enter focus · ] next waiting · z collapse · n new · tab/esc focus · q quit"
		} else if m.viewMode == viewWorkbench {
			hint = "esc focus · 1-4 panel · e edit · f follow · i attach · ] next waiting · q quit"
		}
		statusLine := statusLineStyle.Render(hint)
		sections = append(sections, mainContent, statusLine, m.errBox.String())
	}

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		sections...,
	)

	// Overlay render dispatch: all overlay states share the unified
	// activeOverlay pointer. The activeOverlayKind tag distinguishes the
	// startup workspace picker, which needs full-screen placement (it
	// fires before mainView is meaningful and should center on the empty
	// terminal).
	if m.activeOverlay != nil && m.state != stateDefault {
		if m.activeOverlayKind == overlayWorkspacePickerStartup {
			return asView(lipgloss.Place(m.lastWidth, m.lastHeight,
				lipgloss.Center, lipgloss.Center,
				m.activeOverlay.View()))
		}
		switch m.state {
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings, stateMergePicker, stateLaunchOptions:
			return asView(overlay.PlaceOverlay(0, 0, m.activeOverlay.View(), mainView, true))
		}
	}

	view := asView(mainView)
	m.attachCursor(&view)
	view.WindowTitle = m.windowTitle()
	return view
}

// forwardFocus sends the host's focus state to whichever pane currently has
// focus. PTY writes from the Update goroutine are established practice here
// (inline attach does the same via SendKeysRaw).
func (m *home) forwardFocus(in bool) {
	selected := m.list.GetSelectedInstance()
	if selected == nil {
		return
	}
	switch m.splitPane.GetFocusedPane() {
	case ui.FocusAgent:
		selected.ForwardFocus(in)
	case ui.FocusTerminal:
		m.splitPane.ForwardTerminalFocus(in)
	}
}

// setPaneFocus switches the focused pane, synthesizing focus-out to the old
// pane's app and focus-in to the new one (only while the host itself is
// focused) — so an agent that watches focus (e.g. Claude Code idle
// notifications) sees Loom's pane focus like a real terminal's.
func (m *home) setPaneFocus(pane int) {
	if pane == m.splitPane.GetFocusedPane() {
		return
	}
	if m.hostFocused {
		m.forwardFocus(false)
	}
	m.splitPane.SetFocusedPane(pane)
	if m.hostFocused {
		m.forwardFocus(true)
	}
}

// windowTitle passes the selected agent's OSC title through to the host
// terminal, suffixed so window lists stay identifiable; falls back to the
// instance title when the inner app never set one.
func (m *home) windowTitle() string {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return "loom"
	}
	if t, ok := sel.PaneTitle(); ok {
		return t + " — loom"
	}
	return "loom — " + sel.Title
}

// attachCursor positions the REAL hardware cursor over the focused pane's
// cursor cell — the host terminal then renders its own native cursor
// (user-configured color, blink) there. Only on the plain main-view path:
// overlays, pickers, and non-default states keep the cursor hidden
// (Bubble Tea's default when View.Cursor is nil).
func (m *home) attachCursor(v *tea.View) {
	// The overview grid has no pane content on screen — a hardware
	// cursor positioned from the hidden split layout would float over
	// arbitrary card cells.
	if m.viewMode == viewOverview {
		return
	}
	// Workbench: only the agent split (left column, x-offset 0) shows a
	// live pane cursor. The terminal pane is force-hidden inside the
	// split, so a non-agent pane focus has no on-screen cursor cell.
	xOff := m.listWidth
	if m.viewMode == viewWorkbench {
		if m.splitPane.GetFocusedPane() != ui.FocusAgent {
			return
		}
		xOff = 0
	}
	if m.state != stateDefault && m.state != stateInlineAttach {
		return
	}
	if m.activeOverlay != nil {
		return
	}
	lx, ly, cur, ok := m.splitPane.CursorScreenPosition(m.list.GetSelectedInstance())
	if !ok {
		return
	}
	// Same screen↔split mapping the mouse path uses:
	// HitTest(mouse.X - m.listWidth, mouse.Y - m.tabBar.Height()).
	c := tea.NewCursor(xOff+lx, m.tabBar.Height()+ly)
	c.Blink = cur.Blink
	switch cur.Shape {
	case vt.CursorShapeUnderline:
		c.Shape = tea.CursorUnderline
	case vt.CursorShapeBar:
		c.Shape = tea.CursorBar
	default:
		c.Shape = tea.CursorBlock
	}
	v.Cursor = c
}
