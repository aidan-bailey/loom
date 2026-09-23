package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/keys"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	reviewui "github.com/aidan-bailey/loom/ui/review"
	"os"
	"path/filepath"
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

// metadataResult holds I/O results for one instance from the parallel
// metadata tick. Written by goroutine; status updates applied on main thread.
type metadataResult struct {
	instance   *session.Instance
	tmuxLive   tmux.Liveness
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
	// stateIssuePicker is the state when the GitHub issue picker overlay
	// is displayed (opened by the 'I' key).
	stateIssuePicker
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

type home struct {
	ctx context.Context

	// *workspaceSlot is the focused workspace slot, embedded so its
	// per-workspace state (m.wsCtx, m.storage, m.appConfig, m.appState,
	// m.list, m.splitPane, m.workbench) reads and writes straight through
	// to the one slot that owns it — there is no copy on home to keep in
	// sync. Invariant (checkSlotInvariant): with workspace tabs open
	// (len(m.slots) > 0) it IS m.slots[m.focusedSlot]; in classic/global
	// mode (no tabs) it is the classic slot, which is not in m.slots.
	// Never nil after newHome. Only loadSlot and enterGlobalMode
	// reassign it, and every m.slots mutation restores the invariant
	// before returning (activateWorkspace focuses the first tab opened
	// from classic mode; deactivateWorkspace refocuses when it closes the
	// focused tab and never closes the last one).
	*workspaceSlot

	// -- Storage and Configuration --

	program string

	// cmdExec, when non-nil, replaces cmd2.MakeExecutor() on the workspace
	// load paths (activateWorkspace, enterGlobalMode, and the restore-time
	// orphan sweep) — a test seam so those paths can run without touching
	// a real tmux server. Always nil in production; read via executor().
	cmdExec cmd2.Executor
	// restoreFellBack is set when restoreSavedWorkspaces opened no
	// workspace and fell back to the startup storage. Those workspaces
	// were not closed by the user, so handleQuit keeps the registry's
	// open list for the next launch to retry; enterGlobalMode (an
	// explicit choice of global mode) clears it.
	restoreFellBack bool

	// -- State --

	// state is the current discrete state of the application
	state state
	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool
	// baseBranchName is the ref new sessions are cut from, resolved in the
	// background when the prompt flow starts (see git.ResolveBaseCommit) and
	// used only to label the branch picker's "New branch" row. Empty until
	// resolved, or when resolution failed — the picker then makes no claim.
	baseBranchName string

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

	// pendingNew is the not-yet-started instance an open creation flow
	// (naming, prompt, launch options, the remote-control confirm) has
	// appended to the focused list. The flow edits it, and its cancel
	// paths remove and kill it (dropPendingNew) by identity: the
	// selection and the list's last row are not reliable, since a
	// completion or removal can land mid-flow. Set when a flow appends
	// or re-opens it (openLaunchOptionsForNew), cleared when its start is
	// dispatched or the flow is cancelled. nil outside creation flows.
	pendingNew *session.Instance

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

	// menu displays the bottom menu
	menu *ui.Menu
	// viewMode selects focus (rail + panes) or overview (fleet card
	// grid). Restored from config.UIPrefs.ViewMode at startup.
	viewMode viewMode
	// overview renders the fleet-triage card grid when viewMode is
	// viewOverview.
	overview *ui.Overview
	// wbPrevTerminalHidden is the user's split-terminal setting from
	// before workbench entry (the workbench force-hides it); restored on
	// exit by cleanupWorkbench.
	wbPrevTerminalHidden bool
	// wbLeftWidth is the workbench's agent-column width in screen
	// cells, cached for mouse-wheel routing (like listWidth).
	wbLeftWidth int
	// wbRatio is the in-memory agent share for the current session
	// (0 = default). Flushed to UIPrefs.WorkbenchRatios on exit/quit.
	wbRatio float64
	// wbReview is the concrete review pane; the workbench itself only
	// holds the ui.ReviewPane render interface (import-cycle rule).
	// Invariant: nil iff m.workbench.Review() is nil.
	wbReview *reviewui.Pane
	// wbReviewPrevTab is the panel tab the active review was opened
	// from; closeReview returns there (markdown when unset).
	wbReviewPrevTab ui.WorkbenchTab
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

	// registry is the loaded workspace registry, retained for the picker flow.
	registry *config.WorkspaceRegistry
	// slots holds per-workspace state for every open workspace tab, in
	// tab order. Empty in classic/global mode. The focused one is also
	// embedded as m.workspaceSlot (see the invariant there).
	slots []*workspaceSlot
	// focusedSlot is the index into slots for the currently displayed
	// workspace; meaningless (0) when slots is empty.
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

	// gates throttle the background jobs riding the health tick (roster
	// query, subagent scan, GitHub poll) and dedupe the split-ratio flush
	// tick, one pollGate per gateKind (see pollgate.go; resolve with
	// m.gate). The zero value is ready to use: intervals come from
	// gateIntervals. Update-goroutine only.
	gates [numGateKinds]pollGate

	// ghAvailable caches gh's install/auth check, resolved by the first
	// poll. Until checked, polls proceed (the poll itself checks).
	ghAvailable ghAvailability
	// ghState is the latest GitHub snapshot per open repo path. Replaced
	// wholesale on every ghReadyMsg; a repo whose query failed is absent.
	ghState map[string]github.Snapshot
	// ghErrs is the last poll error per open repo, replaced wholesale
	// alongside ghState. A repo can fail every poll forever while
	// ghAvailable stays ok — CheckCLI is not repo-scoped, so a repo with
	// no GitHub remote never flips availability — and without this the
	// picker would sit on "loading…" with nothing to show for it.
	ghErrs map[string]error
	// ghBases is the resolved base ref name per repo ("origin/main"),
	// refreshed by the poll and read by gatherMetadataCmd for parity.
	ghBases map[string]string

	// roster is Claude's own view of its live sessions, keyed by working
	// directory, refreshed once per health tick (see rosterQueryCmd). It is
	// authoritative where the pane scraper is inferential, so status events
	// consult it first and fall back when it has no entry for a session.
	// Update-goroutine only.
	roster map[string]session.RosterEntry

	// pendingRatioSaves buffers title→ratio pairs recorded by resizeSplit
	// until the throttled ratioSaveMsg flushes them into one mutateUIPrefs
	// write — key-repeat resize would otherwise fsync state.json per
	// keystroke. applyStoredRatio reads it first (pending is newest
	// truth); leaveFocusedSlot/handleQuit flush it synchronously.
	// The gateRatioSave gate dedupes the flush tick (see
	// maybeArmRatioSave). Update-goroutine only.
	pendingRatioSaves map[string]float64

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
// Callers: the throttled ratioSaveMsg tick, leaveFocusedSlot (pending
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
	case gatedMsg:
		return m.deliverGated(msg)
	case ratioSaveMsg:
		// Throttled flush of resizeSplit's pending ratios — one persisted
		// write per 750ms window instead of one per keystroke. A flush
		// that already ran (slot switch, quit) leaves the map empty, so
		// this is a no-op; the gatedMsg wrapper has disarmed the tick.
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
	case subagentScanMsg:
		m.handleSubagentScan(msg)
		return m, nil
	case rosterReadyMsg:
		if msg.err != nil {
			// Debug, not warn: a missing daemon or an older CLI without
			// `agents --json` is a supported configuration, not a fault —
			// detection simply falls back to pane content. Dropping the
			// previous roster is deliberate; a stale snapshot would keep
			// driving transitions long after it stopped being true.
			log.DebugKV("app.roster.query_failed", "err", msg.err.Error())
			m.roster = nil
			return m, nil
		}
		m.roster = msg.entries
		return m, nil
	case ghReadyMsg:
		m.handleGHReady(msg)
		return m, nil
	case ghRefreshMsg:
		m.gate(gateGH).expedite()
		return m, nil
	case issuePickedMsg:
		return m.handleIssuePicked(msg)
	case issueExpandedMsg:
		return m.handleIssueExpanded(msg)
	case statusDetectedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		if msg.err != nil {
			log.WarnKV("app.event.capture_failed", "instance", msg.instance.Title, "err", msg.err.Error())
			return m, nil
		}
		// Claude publishes its own status, so prefer it over the pane
		// ladder below, which can only infer one from screen text. An
		// authoritative answer also retires the re-detection chain: the
		// ladder re-samples because one content hash cannot distinguish
		// "still working" from "just finished", but the roster says which
		// it is, and the next health tick refreshes it.
		target, authoritative := m.adoptRosterStatus(msg.instance)
		if !authoritative {
			// Same transition ladder as the old metadata tick: still-changing →
			// Running; settled with a prompt → Prompting; settled → Ready.
			target = session.Ready
			if msg.updated {
				target = session.Running
			} else if msg.hasPrompt {
				target = session.Prompting
			}
		}
		if err := msg.instance.TransitionTo(target); err != nil {
			log.For("app").Warn("event.transition_failed", "instance", msg.instance.Title, "to", target.String(), "err", err.Error())
		}
		m.updateTabBarStatuses()
		if !authoritative && msg.updated {
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
		_ = m.applyLiveness(msg.instance, msg.tmuxLive, msg.ptmxAlive)
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
		// Sweep expired error/info toasts. This tick is a self-perpetuating
		// heartbeat that runs unconditionally every ~3s regardless of state
		// or view mode, which is what makes it a reliable place to expire
		// ErrBox messages: SetInfo/SetError only arm a deadline, they don't
		// schedule anything to act on it, and the event-driven render loop
		// (see CLAUDE.md gotchas) won't otherwise redraw on its own just
		// because time passed with no other activity.
		m.errBox.ExpireIfDue(time.Now())

		// Collect instances from every loaded workspace slot.
		allInstances := m.allInstances()

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
		cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty(), m.ghBases))

		// One `claude agents --json` for the whole fleet (~380ms, off the
		// Update goroutine), on its OWN cadence rather than the tick's —
		// this tick runs at 500ms on the snapshot path, which would keep a
		// claude process alive most of the time. Claude reports its own
		// busy/idle/waiting state, which beats inferring it from pane text
		// (see rosterStatusFor). nil when not due, already in flight, or no
		// Claude agent is running.
		if roster := m.maybeRosterQuery(active); roster != nil {
			cmds = append(cmds, roster)
		}

		// Subagent hook events, throttled like the roster (see
		// maybeSubagentScan). nil when not due, in flight, or no Claude
		// agent is live.
		if scan := m.maybeSubagentScan(active); scan != nil {
			cmds = append(cmds, scan)
		}

		// GitHub PR/issue state + base-branch fetch, on the poller's own
		// 60s cadence (see maybeGHQuery). nil when not due, in flight, or
		// gh is known unavailable.
		if poll := m.maybeGHQuery(); poll != nil {
			cmds = append(cmds, poll)
		}

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
			if !m.applyLiveness(r.instance, r.tmuxLive, r.ptmxAlive) {
				continue
			}
			// The roster applies on BOTH paths. The exclusion below is
			// specifically about r.updated/r.hasPrompt, which are zero for
			// emulator instances (no capture ran) and would fight the event
			// pipeline; the roster is a real freshly-queried value, so it is
			// safe here — and it is the only thing that corrects a session
			// that changes state while emitting no output at all (a long
			// silent tool call fires no quiet event to sample). It may be up
			// to one tick stale: rosterQueryCmd is dispatched in the same
			// batch as gatherMetadataCmd, so this reads the previous tick's
			// answer. TransitionTo still validates, so an illegal transition
			// is rejected rather than forced.
			if target, authoritative := m.adoptRosterStatus(r.instance); authoritative {
				if err := r.instance.TransitionTo(target); err != nil {
					log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", target.String(), "err", err.Error())
				}
			} else if !r.emulatorDriven {
				// Event-mode instances get their status ladder from quiet
				// events (statusDetectedMsg); running it here too would fight
				// that pipeline with stale zero-valued results.
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
		// A user parked on the workbench's diff tab generates none of the
		// nav traffic that refreshes the diff in focus mode, so ride the
		// metadata tick: re-render from the just-updated diff stats so the
		// tab tracks the agent's work live.
		if m.viewMode == viewWorkbench && m.workbench != nil && m.workbench.Tab() == ui.WbTabDiff {
			if selected := m.list.GetSelectedInstance(); selected != nil {
				m.workbench.Diff().SetDiff(selected)
			}
		}
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
		if m.workbench.Markdown.Editing() {
			// Never clobber an open editor — checked before the error
			// branch so a failed load can't clear the pane under it.
			return m, nil
		}
		if msg.follow && !m.workbench.Markdown.Following() {
			// Stale in-flight follow load: the user pinned a document
			// after this dispatch; applying it would clobber and un-pin.
			return m, nil
		}
		if msg.err != nil {
			// File vanished between scan and read (agent moved it):
			// clear and let the next tick's scan re-resolve.
			m.workbench.Markdown.Clear()
			return m, nil
		}
		m.workbench.Markdown.SetDocument(msg.path, msg.raw, msg.mtime)
		m.workbench.Markdown.SetFollowing(msg.follow)
		return m, nil
	case wbSaveMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title {
			return m, nil
		}
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		if msg.conflict {
			if m.state == stateConfirm {
				// Never replace an open confirmation — e.g. a pending
				// discard-edit confirm — with the save conflict: the
				// user's next "yes" must not silently turn into a force
				// overwrite of a different action. Drop it; the user can
				// re-save once the current confirm resolves.
				return m, nil
			}
			return m, m.confirmTask(
				filepath.Base(msg.path)+" changed on disk — overwrite?",
				overlay.ConfirmationTask{
					// force=true skips the mtime check, so the zero
					// loadedMtime baseline is irrelevant here.
					Async: saveMarkdownCmd(title, msg.path, msg.content, time.Time{}, true),
				})
		}
		if m.workbench.Markdown.Path() == msg.path {
			m.workbench.Markdown.ApplySaved(msg.content, msg.mtime)
		}
		// Else stale: the user switched documents mid-save — the write
		// landed on disk, but the pane no longer shows that file.
		return m, nil
	case wbFilesMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title || msg.err != nil {
			return m, nil
		}
		m.workbench.SetFiles(msg.root, msg.paths)
		return m, nil
	case reviewui.LoadedMsg:
		if t, ok := m.wbCurrentTitle(); ok && t == msg.Title && m.wbReview != nil {
			return m, m.wbReview.HandleMsg(msg)
		}
		return m, nil
	case reviewui.SavedMsg:
		// Two paths on purpose: the pane renders a non-fatal save error
		// in its own footer (only if the msg reaches it), while
		// handleError surfaces the failure even when the user has
		// navigated away from the review — a dropped persistence error
		// is never acceptable.
		var cmds []tea.Cmd
		if t, ok := m.wbCurrentTitle(); ok && t == msg.Title && m.wbReview != nil {
			cmds = append(cmds, m.wbReview.HandleMsg(msg))
		}
		if msg.Err != nil {
			cmds = append(cmds, m.handleError(msg.Err))
		}
		return m, tea.Batch(cmds...)
	case tea.MouseWheelMsg:
		// v1 simplification: the wheel hit-tests below (listWidth /
		// agentBottomY) describe the focus layout and are meaningless
		// over the overview grid, so overview ignores the mouse entirely.
		if m.viewMode == viewOverview {
			return m, nil
		}
		if m.viewMode == viewWorkbench {
			mouse := msg.Mouse()
			if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
				return m, nil
			}
			if mouse.X >= m.wbLeftWidth {
				// Right panel: scroll the active tab (terminal tab
				// no-ops in v1 — its scroll state is shared with the
				// hidden focus split).
				if m.workbench.Tab() != ui.WbTabTerminal {
					if mouse.Button == tea.MouseWheelUp {
						m.workbenchScrollUp()
					} else {
						m.workbenchScrollDown()
					}
				}
				return m, nil
			}
			// Left half: agent scroll via the existing split machinery —
			// same paused/nil bail as the focus-mode wheel path below.
			// (The right-half panel scrolls above are local pane state
			// and deliberately stay unguarded.)
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.GetStatus() == session.Paused {
				return m, nil
			}
			if mouse.Button == tea.MouseWheelUp {
				m.splitPane.ScrollAgentUp()
			} else {
				m.splitPane.ScrollAgentDown()
			}
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
	case baseBranchResolvedMsg:
		m.baseBranchName = msg.name
		// The overlay may already be open (resolution is racing the user
		// typing a title), so push the label through as well as caching it
		// for the next newPromptOverlay.
		if ti := m.textInput(); ti != nil {
			ti.SetBaseBranchName(msg.name)
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
		// goroutine. Here we only do in-memory list bookkeeping. The kill ran
		// for seconds off the update goroutine, so the focused m.list may no
		// longer be the list that owns the instance (workspace tab switch,
		// global-mode transition) — remove it from whichever list holds it,
		// by identity, or the row stays Deleting until restart.
		m.removeInstanceEverywhere(msg.inst)
		return m, m.instanceChanged()
	case transitionFailedMsg:
		// Revert instance status on failed background op (kill/pause/resume).
		// previousStatus came from this same instance, so the reverse
		// transition should always be allowed; if the state machine rejects
		// it, log and leave the status as-is rather than masking a real bug.
		// The message carries the instance pointer: like killInstanceMsg, the
		// focused m.list may have been swapped since the op started.
		if msg.inst != nil {
			if terr := msg.inst.TransitionTo(msg.previousStatus); terr != nil {
				log.For("app").Warn("revert_transition_failed", "err", terr)
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
		return m, m.handleRecoverDone(msg)
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
		release, err := m.activateWorkspace(*ws)
		if err != nil {
			return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
		}
		if err := m.registry.UpdateLastUsed(ws.Name); err != nil {
			log.For("app").Debug("registry.update_last_used_failed", "workspace", ws.Name, "err", err)
		}

		// Focus the just-registered slot so the user sees its
		// instances immediately. activateWorkspace appends to the
		// end, so the new slot is at len-1 (not 0 — the prior
		// loadSlot(0) would have surfaced an unrelated tab). loadSlot
		// flushes the outgoing slot's pending split-ratio saves.
		m.loadSlot(len(m.slots) - 1)
		m.updateTabBarStatuses()
		m.showRecoverySummary(m.recovery)

		// instanceChanged repoints the panes and menu at the new slot's
		// selection; release drops the classic slot's attach clients
		// when this was the first tab.
		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged(), release)
	case instanceStartedMsg:
		return m, m.handleInstanceStarted(msg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// executor returns the command executor for the workspace load paths:
// the injected test seam when set, the production executor otherwise.
func (m *home) executor() cmd2.Executor {
	if m.cmdExec != nil {
		return m.cmdExec
	}
	return cmd2.MakeExecutor()
}

// recoverySummary tallies what a reconcileOrphans pass did, for the
// non-blocking one-line summary shown to the user.
type recoverySummary struct {
	cleaned int // stale worktrees auto-removed
	review  int // Recoverable entries added to the list
	failed  int // records that failed reconcile (storage unrecovered cache)
	// undecodable counts records this binary cannot decode (corrupt, or
	// written by a newer loom); storage preserves them verbatim.
	undecodable int
}

func (s recoverySummary) empty() bool {
	return s.cleaned == 0 && s.review == 0 && s.failed == 0 && s.undecodable == 0
}

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
	if s.undecodable > 0 {
		// Typically left by a newer loom after a downgrade. Saves write
		// them back untouched, so the newer binary finds them intact.
		verb := "were"
		if s.undecodable == 1 {
			verb = "was"
		}
		parts = append(parts, fmt.Sprintf("%s could not be read by this version of loom and %s preserved unchanged",
			plural(s.undecodable, "session record", "session records"), verb))
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
// for: live instances plus the records storage preserves outside the live
// list (reconcile failures and undecodable records, both still tracked in
// state.json). Orphan discovery skips these.
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
		for p := range storage.PreservedWorktreePaths() {
			paths[p] = true
		}
	}
	return paths
}

// claimTitles adds to claimed every session title one workspace owns, for
// the title-keyed sweeps: the server-wide orphan tmux sweep
// (CleanupOrphanedSessions) and the subagent hooks sweep. That is each
// instance in list (Recoverable orphans included) plus each record storage
// preserves on disk outside the list (Storage.PreservedTitles: reconcile
// failures and undecodable records, e.g. a newer loom's after a
// downgrade) — sparing those keeps a preserved record's agent alive for
// the binary that can load it. storage may be nil.
func claimTitles(claimed map[string]bool, list *ui.List, storage *session.Storage) {
	for _, inst := range list.GetInstances() {
		claimed[inst.Title] = true
	}
	if storage == nil {
		return
	}
	for _, title := range storage.PreservedTitles() {
		claimed[title] = true
	}
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
	// cache, and undecodable ones only on disk — surface their counts so
	// they don't read as lost sessions.
	if storage != nil {
		summary.failed = len(storage.UnrecoveredTitles())
		summary.undecodable = storage.UndecodableCount()
	}
	// Preserved records may come back on a later load (or under a newer
	// loom); claimTitles keeps their hooks folders.
	claimed := make(map[string]bool)
	claimTitles(claimed, list, storage)
	session.SweepSubagentHooks(cfgDir, claimed, cmdExec)
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
	// tick may still be in flight; covers the classic path too, which
	// runs no leaveFocusedSlot). The workbench ratio flushes the same
	// way — it is only written on workbench exit otherwise.
	m.flushPendingRatioSaves()
	m.flushWorkbenchRatio()
	if len(m.slots) > 0 {
		m.leaveFocusedSlot()
		var firstErr error
		for _, slot := range m.slots {
			if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
				if quitSkipsSave(err) {
					log.For("app").Warn("quit.save_skipped", "name", slot.wsCtx.Name, "reason", "storage_load_failed", "err", err)
					continue
				}
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
			if !quitSkipsSave(err) {
				return m, m.handleError(err)
			}
			log.For("app").Warn("quit.save_skipped", "reason", "storage_load_failed", "err", err)
		}
		if m.registry != nil && len(m.registry.OpenWorkspaces) > 0 && !m.restoreFellBack {
			if err := m.registry.SetOpenWorkspaces(nil); err != nil {
				log.For("app").Debug("registry.clear_open_failed", "err", err)
			}
		}
	}
	return m, tea.Quit
}

// quitSkipsSave reports whether a save error on quit is the storage's write
// latch (ErrStorageLoadFailed). The sticky-quit policy exists so the user can
// fix the cause and retry, but a latched storage is never reloaded by the
// TUI, so no retry could succeed; its list is also empty by construction
// (latchedStorageErr), and the unreadable file is left untouched. Quit.
func quitSkipsSave(err error) bool {
	return errors.Is(err, session.ErrStorageLoadFailed)
}

func (m *home) handleMenuHighlighting(msg tea.KeyPressMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateNew || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateMergePicker || m.state == stateLaunchOptions || m.state == stateIssuePicker {
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
	case stateIssuePicker:
		return handleStateIssuePickerKey(m, msg)
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
			// SetSession dropped the workbench's half of the review-pane
			// invariant; drop ours too, or keys would keep routing into an
			// invisible pane and `S` would compose the previous session's
			// comments and send them to this one.
			m.dropReviewPane()
			if m.workbench.Tab() == ui.WbTabReview {
				m.workbench.SetTab(ui.WbTabMarkdown)
			}
			m.wbReviewPrevTab = ui.WbTabMarkdown
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
	// inst is the killed instance itself. The handler removes it by
	// identity from whichever slot list owns it — the focused m.list may
	// have been swapped (workspace tab switch, global mode) between kill
	// start and completion, so a title lookup against m.list can miss.
	inst  *session.Instance
	title string
}

// transitionFailedMsg is returned when a background status-transitioning
// operation (kill, pause, resume) fails. The main event loop reverts the
// instance to previousStatus so the user can retry. `op` identifies the
// operation for the error log.
type transitionFailedMsg struct {
	// inst is the instance whose background op failed. Reverted directly
	// by pointer — see killInstanceMsg.inst for why a title search against
	// the focused m.list is not enough.
	inst           *session.Instance
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
// placeholder for the recovered instance and persists. placeholder and
// slot are stamped at dispatch: the recover runs for seconds with the UI
// live, so by delivery the focused slot may be another workspace, which
// can even hold a same-titled row.
type recoverDoneMsg struct {
	oldTitle    string
	recovered   *session.Instance
	err         error
	placeholder *session.Instance
	slot        *workspaceSlot
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

// instanceStartedMsg reports an async Start. slot is the slot that owns
// instance, stamped at dispatch: the start runs for seconds with the UI
// live, so by delivery the focused slot may be another workspace (or the
// owner may be closed).
type instanceStartedMsg struct {
	instance       *session.Instance
	err            error
	selectedBranch string
	slot           *workspaceSlot
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// baseBranchResolvedMsg carries the resolved base branch name back to
// Update. Resolution runs off the main goroutine because it shells out to
// git; the name is display-only, so a failure just leaves it empty.
type baseBranchResolvedMsg struct {
	name string
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
func gatherMetadataCmd(active []*session.Instance, selected *session.Instance, dirty map[string]bool, bases map[string]string) tea.Cmd {
	return func() tea.Msg {
		results := make([]metadataResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.instance = instance

				r.tmuxLive = instance.TmuxLiveness()
				if r.tmuxLive != tmux.LivenessAlive {
					return
				}
				r.ptmxAlive = instance.PtmxAlive()

				// Event-mode instances get status from quiet events; the
				// subprocess scan only remains for the snapshot path.
				r.emulatorDriven = instance.HasEmulator()
				if !r.emulatorDriven {
					r.updated, r.hasPrompt, r.captureErr = instance.CaptureAndProcessStatus()
				}

				// Parity must not sit behind ShouldRefreshDiff: that gate
				// is about session output, but the base branch moves
				// without any session activity at all — "you are now N
				// behind main" is exactly the case where tmuxUpdated is
				// false. One local rev-list, no network.
				instance.UpdateParity(bases[instance.Path])

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
// as running), or is no longer in any loaded slot. Must run on the Update
// goroutine.
func (m *home) applyLiveness(inst *session.Instance, tmuxLive tmux.Liveness, ptmxAlive bool) (alive bool) {
	if m.slotHolding(inst) == nil {
		// The probe was taken before inst's slot was dropped. Its attach
		// client has been (or is being) released by releaseSlotCmd, which
		// reads as a dead PTY: RepairPtmx here would re-attach an
		// instance nothing displays, and a workspace-terminal restart
		// would relaunch one. Drop the result.
		return false
	}
	if tmuxLive == tmux.LivenessUnknown {
		// The probe never got an answer, which says nothing about the
		// session — under load it is simply what a starved subprocess
		// looks like. Acting on it would pause a healthy agent, and
		// because that same load starves every instance's probe at once,
		// it would do so across the whole fleet simultaneously. Leave
		// the instance untouched; the next tick re-probes.
		log.For("app").Debug("tick.tmux_probe_inconclusive", "title", inst.Title)
		return true
	}
	if tmuxLive != tmux.LivenessAlive {
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
	ti := overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
	ti.SetBaseBranchName(m.baseBranchName)
	return ti
}

// resolveBaseBranchCmd looks up the ref new sessions will be cut from, for
// the branch picker's label. The configured value is read here, on the main
// goroutine, rather than inside the returned Cmd — appConfig is mutable at
// runtime and Cmd bodies run concurrently with Update.
func (m *home) resolveBaseBranchCmd() tea.Cmd {
	repoDir := m.repoPath()
	configured := m.appConfig.GetBaseBranch()
	return func() tea.Msg {
		_, name, err := git.ResolveBaseCommit(repoDir, configured, nil)
		if err != nil {
			// Display-only: session creation surfaces the real error later.
			log.For("app").Debug("base_branch_resolve_failed", "err", err.Error())
			return nil
		}
		return baseBranchResolvedMsg{name: name}
	}
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up the pending
// instance of a creation flow (none when the overlay was prompting a
// running session).
func (m *home) cancelPromptOverlay() tea.Cmd {
	killCmd := m.dropPendingNew()
	m.dismissOverlay()
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)
	return tea.Batch(tea.RequestWindowSize, killCmd)
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
// When a workspace tab is focused it returns the workspace's registered
// path; otherwise (classic/global mode, even with a startup workspace
// context) it falls back to the process working directory.
func (m *home) repoPath() string {
	if len(m.slots) > 0 && m.wsCtx.RepoPath != "" {
		return m.wsCtx.RepoPath
	}
	cwd, _ := os.Getwd()
	return cwd
}

// configDir returns the config directory for the focused slot. Returns
// empty string when it has no workspace context (global mode; triggers
// fallback to GetConfigDir).
func (m *home) configDir() string {
	if m.wsCtx != nil {
		return m.wsCtx.ConfigDir
	}
	return ""
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
			hint = "esc focus · 1-5 panel · e edit · f follow · i attach · ] next waiting · q quit"
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
