package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/keys"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
	reviewui "github.com/aidan-bailey/loom/ui/review"
	"os"
	"os/exec"
	"path/filepath"
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
	// per-workspace state (m.id, m.info and its accessors, m.list,
	// m.splitPane, m.workbench) reads and writes straight through
	// to the one slot that owns it — there is no copy on home to keep in
	// sync. Invariant (checkSlotInvariant): with workspace tabs open
	// (len(m.slots) > 0) it IS m.slots[m.focusedSlot]; in classic/global
	// mode (no tabs) it is the classic slot, which is not in m.slots.
	// Never nil after startHome. Only loadSlot and enterGlobalMode
	// reassign it, and every m.slots mutation restores the invariant
	// before returning (openTab focuses the first tab opened from
	// classic mode; deactivateWorkspace refocuses when it closes the
	// focused tab and never closes the last one).
	*workspaceSlot

	// -- Storage and Configuration --

	// core is the session model (package core): the loaded workspaces,
	// their instances and everything lifecycle, through the methods the
	// TUI calls (core.Core; *rpc.Client, connected to the loom daemon, is
	// its implementation: it keeps a replica of the state the model
	// publishes). Never nil after startHome.
	core core.Core
	// conn is the daemon's client core is, for the link's state
	// (rpc.Client.Stopping, Err) and its wakes, which Run forwards into
	// the program (forwardWakes); a rejoin replaces both (resync). nil in
	// bare test homes, which skip every link check. stopCore closes the
	// client (a test stack's stop, in fixtures).
	conn     *rpc.Client
	stopCore func()
	// link is the TUI's link to the daemon: connected, stopping, waiting
	// or reconnecting (link.go). Offline (not connected), a banner names
	// it, keys that need the model are refused, and a rejoin is scheduled.
	link link
	// rejoin joins a daemon again once this one is lost (Run's; nil in
	// tests that set none, whose TUI then stays offline).
	rejoin Rejoin
	// send is the program's Send (Run sets it before the program runs; nil
	// in tests): a rejoin's progress and a new client's wakes go through
	// it, from goroutines of their own.
	send func(tea.Msg)
	// daemonTmux is the tmux server the daemon named at the last join: a
	// rejoined daemon on another server (the old one died) has its pane
	// clients replaced (resync).
	daemonTmux string
	// exitErr is why the TUI quit on its own (a rejoin found a newer
	// daemon: ErrDaemonNewer), which Run returns once the terminal is
	// restored.
	exitErr error
	// program is the agent program this TUI's drafts and scripts default
	// to: the one the process started with (the -p flag, else the startup
	// workspace's), until this TUI saves another default program
	// (handleStateSettingsKey).
	program string
	// sentSelected is the selection last published to the model
	// (publishSelection).
	sentSelected core.InstanceID
	// initCmd holds the Cmds startHome drained from the model before the
	// program ran (an error notice's hide timer); Init returns them.
	initCmd tea.Cmd
	// panes holds the TUI's attach clients, one per live agent tmux session
	// (ui.PaneClients). Everything that renders an agent pane, scrolls it,
	// forwards input to it or scrapes its screen for status goes through
	// it. It is shared by every slot's list and split pane, and is never
	// nil after startHome.
	panes *ui.PaneClients

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
	// draft while stateLaunchOptions is active.
	// state_new.go/state_prompt.go stash it (capturing the draft and
	// any prompt-flow-specific data like selectedBranch) right before
	// opening the Session Launch Options modal; handleStateLaunchOptionsKey
	// invokes it with the user's chosen overlay.LaunchOptions on confirm,
	// then clears it. nil outside that window.
	pendingLaunchOptions func(overlay.LaunchOptions) (tea.Model, tea.Cmd)

	// pendingLaunchOptionsCancel runs when the Session Launch Options
	// modal is dismissed without confirming (Esc/ctrl+c). The creation
	// flow (state_new.go/state_prompt.go) sets this to discard the
	// draft. The restart flow
	// (runRestartWithOptionsSelected) sets it to a no-op dismiss, since
	// the instance being edited already exists and must survive a
	// cancel untouched. nil outside the stateLaunchOptions window.
	pendingLaunchOptionsCancel func() (tea.Model, tea.Cmd)

	// draft is the session-to-be an open creation flow (naming, prompt,
	// launch options, the remote-control confirm, a #n expansion in
	// flight) edits: TUI state the model never sees until the flow
	// confirms (Create), shown as an ID-0 row at the end of its own slot's
	// rail (rowsOf). The flow holds it by identity, never by the selection
	// or the list's last row, which a completion or removal can move
	// mid-flow; its cancel paths discard it (discardDraft), with nothing
	// to kill. Set when a flow opens it (newDraft), cleared when its
	// Create is sent or the flow is cancelled. nil outside creation flows.
	draft *draft

	// keySent is used to manage underlining menu items
	keySent bool

	// attachingID is set for the duration of a full-screen attach
	// (PausePreview -> tea.ExecProcess -> ensurePane; see
	// startFullScreenAttachMsg/attachDoneMsg) and 0 otherwise. The health
	// tick's ptmx self-heal (the core.Alive applier) must not re-attach this
	// instance's pane client while it is set — PtmxAlive is expected to
	// read false during that window, and racing a Restore against the
	// in-flight ExecProcess would fight over the same tmux session's attach.
	attachingID core.InstanceID

	// bells holds the instances whose pane rang a bell since they were last
	// focused (TUI state; laid over rows as InstanceView.Bell).
	bells map[core.InstanceID]bool
	// ladder holds the status the pane ladder scraped for each instance
	// (TUI state; laid over an active, unreported row's status, see
	// overlay). Pruned with the views (pruneLadder).
	ladder map[core.InstanceID]ladderState

	// pending holds the requests the TUI made with a ReqID, by ReqID, until
	// their Reply (handleReply); nextReq is the last ReqID it chose.
	pending map[core.ReqID]pendingReq
	nextReq core.ReqID
	// sending holds the instances a prompt send is in flight to (sendPrompt,
	// until its Reply): input to them is held meanwhile (sendingTo).
	sending map[core.InstanceID]bool

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
	// accountStrip is the usage strip above the tab bar; empty (height 0)
	// until an extra account exists.
	accountStrip *ui.AccountStrip
	// runningAsShown: this client has shown the model's warning that loom
	// runs as an extra account (showRunningAsAccount).
	runningAsShown bool
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
	// pendingMergeTarget and pendingMergeSourceItems capture the merge
	// target instance and a snapshot of the eligible source list at the
	// moment the merge picker opens. A background message unrelated to
	// key input (e.g. a recover completion reassigning m.list's selection, or
	// a kill/resume completing) can still land while stateMergePicker is
	// active — m.state only gates key-press routing, not arbitrary
	// tea.Msg handling in Update(). Re-querying m.list live when Enter
	// is pressed would let such a background change silently swap which
	// instances the merge acts on. Both fields are cleared once the
	// picker closes.
	pendingMergeTarget      *core.InstanceView
	pendingMergeSourceItems []core.InstanceView

	// -- Workspace slots --

	// slots holds per-workspace state for every open workspace tab, in
	// tab order. Empty in classic/global mode. The focused one is also
	// embedded as m.workspaceSlot (see the invariant there).
	slots []*workspaceSlot
	// focusedSlot is the index into slots for the currently displayed
	// workspace; meaningless (0) when slots is empty.
	focusedSlot int
	// startupName names the workspace this TUI started on, which its
	// classic slot shows until a tab opens: "" for the global workspace, a
	// registered name for `loom <dir>` or --workspace. The startup restore
	// opens it beside the saved tabs and focuses it
	// (restoreSavedWorkspaces).
	startupName string
	// failedOpen names the workspaces of the registry's open list whose
	// Open failed at startup (restoreSavedWorkspaces). They stay in the
	// open list this TUI persists (openList), and the picker shows them
	// checked and labelled "(failed to load)", until one opens
	// (openTab) or the user deselects it (applyWorkspaceToggle);
	// global mode (enterGlobalMode, stayInGlobalMode) closes them all.
	failedOpen []string
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

	// aliveProbe replaces the tmux has-session probe behind sessionAlive in
	// tests; nil in production.
	aliveProbe func(name string) bool

	// redetectPending tracks sessions with an armed delayed re-detection
	// (see maybeRedetect), so inconclusive detections cannot stack parallel
	// re-detect chains. Update-goroutine only.
	redetectPending map[string]bool

	// snapshotScanning is set while a snapshot scan is in flight; cleared
	// when its result lands. Update-goroutine only.
	snapshotScanning bool

	// pendingRatioSaves buffers title→ratio pairs recorded by resizeSplit
	// until the throttled ratioSaveMsg flushes them into one mutateUIPrefs
	// write — key-repeat resize would otherwise fsync state.json per
	// keystroke. applyStoredRatio reads it first (pending is newest
	// truth); leaveFocusedSlot/handleQuit flush it synchronously.
	// ratioTickArmed dedupes the flush tick (see maybeArmRatioSave).
	// Update-goroutine only.
	pendingRatioSaves map[string]float64
	// ratioTickArmed is set while the split-ratio flush tick is in flight;
	// cleared when it lands. Update-goroutine only.
	ratioTickArmed bool

	// settingsEdit is the config the open settings overlay edits: the
	// TUI's own, built from the focused workspace's published settings
	// (config.FromSettings), never the model's. Each change is sent back
	// whole (core.Model.SaveSettings). nil while no settings overlay is
	// open.
	settingsEdit *config.Config

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
	if m.accountStrip != nil {
		m.accountStrip.SetWidth(msg.Width)
	}

	// Workbench mode has no rail: zero the list width (mirroring the
	// railHidden path) so the cached m.listWidth mouse anchor is correct.
	inWorkbench := m.viewMode == viewWorkbench && m.workbench != nil
	listWidth := int(float32(msg.Width) * ui.ListWidthPercent)
	if m.railHidden || inWorkbench {
		listWidth = 0
	}
	paneWidth := msg.Width - listWidth

	// Content gets all height minus the top chrome (account strip + tab
	// bar), status line (1), and error box (1).
	contentHeight := msg.Height - m.topChromeHeight() - 2

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
	//   top chrome + 1 (agent top border) + content + 1 (agent bottom border) - 1
	m.agentBottomY = m.topChromeHeight() + 1 + m.splitPane.AgentContentHeight()

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
// Called at the end of startHome (classic startup), after a slot is
// loaded/focused (loadSlot), and on entering global mode.
func (m *home) applyUIPrefs() {
	if m.id == 0 {
		// Bare test homes construct no app state; nothing to apply.
		return
	}
	p := m.uiPrefs()
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
func (m *home) applyStoredRatio(inst *core.InstanceView) {
	if m.id == 0 || inst == nil {
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
	if r, ok := m.uiPrefs().SplitRatios[inst.Title]; ok {
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

// mutateUIPrefs applies fn to a copy of the focused workspace's prefs and
// asks the model to persist it; on success the slot keeps the result, so
// a read later in the same Update sees it. Save errors are logged, not
// surfaced (layout prefs are best-effort). Persistence is a synchronous
// write-through to state.json — fine for rare toggles; debounce burst
// callers (e.g. key-repeat ratio changes).
//
// Offline (or when the request is refused as unavailable: the link not yet
// noticed), the change applies locally and is kept as unsent
// (workspaceSlot.unsentPrefs), and resync sends it to the next daemon: the
// TUI's own value wins.
func (m *home) mutateUIPrefs(fn func(*config.UIPrefs)) {
	if m.id == 0 {
		// Bare test homes load no workspace; nothing to persist.
		return
	}
	p := m.uiPrefs()
	fn(&p)
	if m.offline() {
		m.keepUnsentPrefs(p)
		return
	}
	if err := m.core.SetUIPrefs(m.id, p); err != nil {
		if errors.Is(err, core.ErrUnavailable) {
			m.keepUnsentPrefs(p)
			return
		}
		log.For("app").Warn("ui_prefs_save_failed", "err", err)
		return
	}
	m.info.UIPrefs = p
}

// Init implements tea.Model. It starts the spinner and kicks off the
// preview and metadata tick loops — those loops re-arm themselves by
// returning the same tick message, so Init fires exactly once per Run.
func (m *home) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, tickUpdateMetadataCmd, m.initCmd, m.drainCore()}
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

// Update implements tea.Model: the message's handler (update), then
// whatever the model produced meanwhile (drainCore), then the link to the
// daemon (checkLink: a bye, or the model lost, brings the TUI offline;
// link.go), then the selection, if it moved (publishSelection). The link
// is checked after the drain, so the replies that arrived before the
// connection closed are applied, not failed as stranded. Nothing panics
// offline: the lost client answers local reads, View's included, from its
// last replica. Once the TUI quits on its own (exitErr), nothing more is
// handled.
func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.exitErr != nil {
		return m, nil
	}
	model, cmd := m.update(msg)
	cmd = tea.Batch(cmd, m.drainCore(), m.checkLink())
	m.publishSelection()
	return model, cmd
}

// update is Update's message handler.
func (m *home) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case coreWakeMsg:
		// The model's loop woke the TUI: Update's drain does the rest, and
		// its link check (a bye, or the model lost).
		return m, nil
	case rejoinTickMsg:
		return m, m.rejoinTick(msg)
	case rejoinedMsg:
		return m, m.rejoined(msg)
	case rejoinNoteMsg:
		m.rejoinNote(msg)
		return m, nil
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
				currentHash = m.panes.For(selected).GetContentHash()
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
		m.core.MarkOutput(msg.session)
		selected := m.list.GetSelectedInstance()

		if v, _ := m.viewBySession(msg.session); v != nil {
			m.core.PaneOutput(v.ID)
			// Output arrived → the agent is doing something. Mirrors the old
			// tick's updated→Running transition; Ready re-derives on the
			// quiet event once the burst settles. Prompting is exempt:
			// focus-in/out forwarding (host focus, selection changes) makes
			// agents repaint, and that output must not relabel a waiting
			// prompt as Running — quiet-time detection owns leaving
			// Prompting once the prompt is actually gone.
			// A Claude session whose hooks or roster reported a status is
			// exempt too: the report owns the status, and output alone
			// says nothing new.
			// The promotion is the ladder's, a display overlay (setLadder),
			// read from the row as the user sees it (v, overlaid): the
			// status promoted is the one shown.
			if v.Status == session.Ready && !v.StatusReported {
				m.setLadder(v.ID, session.Running)
				m.updateTabBarStatuses()
			}
			if selected != nil && v.ID == selected.ID {
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
		v, _ := m.viewBySession(msg.session)
		if v != nil {
			m.core.PaneQuiet(v.ID)
		}
		if v == nil || !v.Active() {
			// A quiet that lands mid-Start (Loading) is this burst's only
			// settle signal — quiet never re-fires without new output, so
			// dropping it would leave the unconditional Running set by
			// Start/Resume uncorrected. Re-check after the start resolves.
			if v != nil && v.Status == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(*v, m.panes.For(v))
	case ratioSaveMsg:
		// Throttled flush of resizeSplit's pending ratios — one persisted
		// write per 750ms window instead of one per keystroke. A flush
		// that already ran (slot switch, quit) leaves the map empty, so
		// this is a no-op; clearing ratioTickArmed first lets the next
		// resize arm a fresh tick.
		m.ratioTickArmed = false
		m.flushPendingRatioSaves()
		return m, nil
	case redetectMsg:
		delete(m.redetectPending, msg.session)
		v, _ := m.viewBySession(msg.session)
		if v == nil || !v.Active() {
			if v != nil && v.Status == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(*v, m.panes.For(v))
	case accountLoginDoneMsg:
		// tea.ExecProcess has returned the terminal. Re-read the auth of the
		// account that just logged in (and the default's, which the
		// refresh covers when that is the one) and probe its usage.
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(fmt.Errorf("claude auth login for %s: %w", msg.name, msg.err)))
		}
		m.core.RequestAccountsRefresh(msg.name == account.DefaultName)
		m.core.RequestUsageProbe()
		cmds = append(cmds, tea.RequestWindowSize)
		return m, tea.Batch(cmds...)
	case issuePickedMsg:
		return m.handleIssuePicked(msg)
	case issueExpandedMsg:
		return m.handleIssueExpanded(msg)
	case statusDetectedMsg:
		// The ladder is a display overlay (setLadder): it never writes the
		// model, and gates on the row. The overlay only swaps one active
		// status for another, so Active and StatusReported read the same on
		// the row as on the model's view.
		v, _ := m.viewByID(msg.id)
		if v == nil || !v.Active() {
			return m, nil
		}
		if msg.err != nil {
			log.WarnKV("app.event.capture_failed", "instance", msg.title, "err", msg.err.Error())
			return m, nil
		}
		// Claude reports its own status, through its hooks and the roster,
		// so prefer it over the pane ladder below, which can only infer one
		// from screen text: the model applies it (its hook scans, roster
		// answers and health tick, through core's adoptClaudeStatus). A
		// reported status also retires the re-detection chain: the ladder
		// re-samples because one content hash cannot distinguish "still
		// working" from "just finished", but the report says which it is.
		if v.StatusReported {
			m.updateTabBarStatuses()
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
		m.setLadder(msg.id, target)
		m.updateTabBarStatuses()
		if msg.updated {
			// One sample of changed content cannot distinguish "still
			// working" from "finished a burst and idled" — under the
			// emulator this was the only sample per burst, so Running
			// latched on idle agents (and masked visible prompts, since
			// updated wins over hasPrompt). Re-sample until a detection
			// sees unchanged content and settles to Ready/Prompting.
			return m, m.maybeRedetect(v.TmuxSession)
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
		v, _ := m.viewBySession(msg.session)
		if v == nil || v.ID == m.attachingID || !v.Active() {
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		m.core.VerifyDead(v.ID)
		return m, tea.Batch(cmds...)
	case bellMsg:
		sel := m.list.GetSelectedInstance()
		if v, _ := m.viewBySession(msg.session); v != nil && (sel == nil || v.ID != sel.ID) {
			if m.bells == nil {
				m.bells = make(map[core.InstanceID]bool)
			}
			m.bells[v.ID] = true
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

		// The TUI's half of the health tick re-arms itself. The model's half
		// runs on its own loop, at the same cadence (core.Loop). Close the
		// clients of sessions that stopped being active since the last tick
		// (paused, killed, exited, or their slot closed).
		cmds := []tea.Cmd{tickUpdateMetadataCmd, m.prunePanes()}

		selected := m.list.GetSelectedInstance()
		// Inline-attach liveness backstop (the preview tick used to check
		// this every 100ms in event mode; ptyDeadMsg is the fast path now,
		// this tick is the safety net for deaths that never EOF'd the PTY).
		if m.state == stateInlineAttach {
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				cmds = append(cmds, tea.RequestWindowSize)
			}
		}

		// The status ladder on the snapshot path reads each pane's screen,
		// which only the TUI's clients have.
		if scan := m.snapshotScan(); scan != nil {
			cmds = append(cmds, scan)
		}

		// Workbench follow scan rides the health tick: cheap stat-walk
		// of the selected worktree, guarded stale on delivery.
		if m.viewMode == viewWorkbench {
			if scan := m.workbenchScanCmd(); scan != nil {
				cmds = append(cmds, scan)
			}
		}
		return m, tea.Batch(cmds...)
	case snapshotStatusMsg:
		m.snapshotScanning = false
		for _, r := range msg.results {
			// The ladder is a display overlay (setLadder), gated on the
			// row, as on the event path.
			v, _ := m.viewByID(r.id)
			if v == nil || !v.Active() {
				continue
			}
			// A failed capture is no opinion, as on the event path. Its
			// zero result reads as "settled, no prompt": run through the
			// ladder, it moved a dead session to Ready before the probe
			// paused it.
			if r.err != nil {
				log.WarnKV("app.tick.capture_failed", "instance", r.title, "err", r.err.Error())
				continue
			}
			// Output: the next tick refreshes the diff, whoever reports the
			// status, as a pane event's output does (paneDirtyMsg).
			if r.updated {
				m.core.MarkOutput(v.TmuxSession)
			}
			// A reported Claude status is the model's: its tick applies it.
			if v.StatusReported {
				continue
			}
			// Same transition ladder as the event path: still-changing →
			// Running; settled with a prompt → Prompting; settled → Ready.
			target := session.Ready
			if r.updated {
				target = session.Running
			} else if r.hasPrompt {
				target = session.Prompting
			}
			m.setLadder(r.id, target)
		}
		m.updateTabBarStatuses()
		return m, nil
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
			if selected == nil || selected.Status == session.Paused {
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
			if selected == nil || selected.Status == session.Paused {
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
		if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.topChromeHeight()); ok {
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
		if pane, row, col, ok := m.splitPane.HitTest(mouse.X-m.listWidth, mouse.Y-m.topChromeHeight()); ok && pane == m.dragPane {
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
	case showHelpScreenMsg:
		m.menu.SetState(ui.StateDefault)
		return m.showHelpScreen(msg.helpType, nil)
	case startFullScreenAttachMsg:
		// Resolve the session to attach in the foreground, and the client
		// whose preview PTY must let go of it for the duration.
		var attach *exec.Cmd
		var preview *tmux.TmuxSession
		switch msg.target {
		case attachTargetAgent:
			// The tmux session, by name (the TUI holds no instance), of the
			// row as it is now: none when it left every open slot.
			var v *core.InstanceView
			if msg.instance != nil {
				v, _ = m.viewByID(msg.instance.ID)
			}
			if v != nil && v.TmuxSession != "" {
				attach = tmux.NewSessionNamed(v.TmuxSession, v.SessionProgram).FullScreenAttachCmd()
				preview = m.panes.For(v).Client()
			}
		case attachTargetTerminal:
			if ts := m.splitPane.TerminalTmuxSession(); ts != nil {
				attach, preview = ts.FullScreenAttachCmd(), ts
			}
		}
		if attach == nil {
			return m, m.handleError(fmt.Errorf("no tmux session available for attach"))
		}
		// Close the preview PTY so the foreground tmux attach owns the tty.
		if preview != nil {
			if err := preview.PausePreview(); err != nil {
				return m, m.handleError(err)
			}
		}
		inst := msg.instance
		m.attachingID = 0
		if inst != nil {
			m.attachingID = inst.ID
		}
		return m, tea.ExecProcess(attach, func(err error) tea.Msg {
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
		// tea.ExecProcess has restored the terminal. Re-attach the agent's
		// client so live capture resumes. A failure is logged inside
		// ensurePane, and the metadata tick's repair retries it once
		// attachingID is cleared below.
		if msg.instance != nil {
			// The attach held the event loop for its whole run, so the
			// stores may lag what the model's jobs did meanwhile: reread
			// them, and repair the row as it is now.
			m.syncViews()
			if v, _ := m.viewByID(msg.instance.ID); v != nil {
				m.ensurePane(v)
			}
		}
		if ts := m.splitPane.TerminalTmuxSession(); ts != nil {
			if err := ts.ResumePreview(); err != nil {
				title := ""
				if msg.instance != nil {
					title = msg.instance.Title
				}
				log.For("app").Error("terminal_preview.resume_failed", "title", title, "err", err)
			}
		}
		if msg.instance != nil && m.attachingID == msg.instance.ID {
			m.attachingID = 0
		}
		m.state = stateDefault
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.handleError(msg.err))
		}
		cmds = append(cmds, tea.RequestWindowSize, m.instanceChanged())
		return m, tea.Batch(cmds...)
	case registerWorkspaceMsg:
		// The registry has no lock, so the Add runs here on Update, never
		// in the confirmation's Cmd. The model serves the workspace from
		// then on, and this TUI opens it as a tab.
		v, err := m.core.Register(msg.name, msg.dir)
		if err != nil {
			return m, m.handleError(err)
		}
		if v.Name == "" {
			// A workspace at $HOME has the global config dir, which the
			// model serves once, as the global workspace: no tab. Show it
			// the way global mode does.
			var cmd tea.Cmd
			if !m.inGlobalMode() {
				cmd = m.enterGlobalMode()
			}
			if m.inGlobalMode() {
				m.errBox.SetInfo(twinNote(msg.name, ""))
			}
			return m, tea.Batch(tea.RequestWindowSize, cmd)
		}
		release, err := m.openTab(v.ID)
		if err != nil {
			return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
		}
		if err := m.core.SetLastUsed(v.Name); err != nil {
			log.For("app").Debug("registry.update_last_used_failed", "workspace", v.Name, "err", err)
		}

		// Focus the just-registered slot so the user sees its
		// instances immediately. openTab appends to the
		// end, so the new slot is at len-1 (not 0 — the prior
		// loadSlot(0) would have surfaced an unrelated tab). loadSlot
		// flushes the outgoing slot's pending split-ratio saves.
		m.loadSlot(len(m.slots) - 1)
		m.updateTabBarStatuses()
		m.showRecoverySummary(m.recovery())

		// instanceChanged repoints the panes and menu at the new slot's
		// selection; release drops the classic slot's attach clients
		// when this was the first tab.
		return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged(), release)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// showRecoverySummary surfaces a reconcile summary on the error bar as a
// non-alarming info line. No-op when nothing happened.
func (m *home) showRecoverySummary(s core.RecoverySummary) {
	if s.Empty() {
		return
	}
	m.errBox.SetInfo(s.String())
}

// handleQuit ends the TUI. It saves no session: the daemon serves them
// on and saves them itself, after each request that changes them and each
// change it makes on its own (an agent found exited, a conversation
// named: core's saveUnprompted), and all of them when it stops. It writes
// only the TUI's own state (the split ratios, the workbench's, the open
// list); Run closes the client once the program has stopped.
func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	// Persist any not-yet-flushed split resize before exit (the throttle
	// tick may still be in flight; covers the classic path too, which
	// runs no leaveFocusedSlot). The workbench ratio flushes the same
	// way — it is only written on workbench exit otherwise.
	m.flushPendingRatioSaves()
	m.flushWorkbenchRatio()
	if len(m.slots) > 0 {
		m.leaveFocusedSlot()
	}
	if m.offline() {
		// The open list and the ratios are the daemon's to write, and
		// there is none to write them: they are lost, on record.
		log.For("app").Warn("quit.offline_unsaved", "link", m.link.state.String(),
			"open", m.openList(), "unsent_prefs", m.unsentPrefSlots())
		return m, tea.Quit
	}
	// With no tab open the open list is written only if the registry holds
	// one: it then keeps just the workspaces that failed to restore.
	if len(m.slots) > 0 || len(m.core.Registry().Open) > 0 {
		m.persistOpenList()
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
	if m.state == statePrompt || m.state == stateNew || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach || m.state == stateFileExplorer || m.state == stateMergePicker || m.state == stateLaunchOptions || m.state == stateIssuePicker || m.state == stateSettings {
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
		delete(m.bells, selected.ID)
		selected.Bell = false // the copy the panes and menu get below
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
				m.panes.For(prev).ForwardFocus(false)
			}
		}
		m.lastFocusTitle = newFocusTitle
		if m.hostFocused && selected != nil && m.splitPane.GetFocusedPane() == ui.FocusAgent {
			m.panes.For(selected).ForwardFocus(true)
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
		m.workbench.SetSession(selected.Title, selected.WorktreePath)
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

type instanceChangedMsg struct{}

// showHelpScreenMsg asks Update to open a help overlay. Emitted from
// tea.Cmd closures, which run off the main goroutine and therefore must
// not call showHelpScreen (it mutates m.state/overlay and writes app
// state to disk) directly.
type showHelpScreenMsg struct {
	helpType helpText
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
	instance *core.InstanceView
	target   fullScreenAttachTarget
}

// attachDoneMsg is returned by tea.ExecProcess when the foreground tmux
// attach-session child exits (user hit C-q, or the session died).
type attachDoneMsg struct {
	instance *core.InstanceView
	err      error
}

// startAttachCmd returns a Cmd that emits startFullScreenAttachMsg so Update
// can hand off to tea.ExecProcess. It exists as a helper because the same
// payload is needed from both the "help skipped" and "help dismissed" paths.
func startAttachCmd(inst *core.InstanceView, target fullScreenAttachTarget) tea.Cmd {
	return func() tea.Msg {
		return startFullScreenAttachMsg{instance: inst, target: target}
	}
}

// registerWorkspaceMsg asks Update to register a pending directory as
// workspace name and open it. The startup confirmation's Cmd returns it
// instead of calling registry.Add itself: the registry has no lock, and
// Update reads and writes it.
type registerWorkspaceMsg struct {
	name string
	dir  string
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

// tickUpdateMetadataCmd drives the TUI's half of the health tick: toast
// expiry, pane-client prune and repair backstops, and the snapshot and
// workbench scans. It re-arms itself; the model's half (liveness, parity,
// diff stats, background jobs) runs on its loop's own timer at the same
// cadence (core.tickInterval). In event mode (emulator path) it is a slow
// belt-and-braces sweep, because status detection rides pane events
// instead; on the snapshot path it keeps the legacy 500ms cadence.
var tickUpdateMetadataCmd = func() tea.Msg {
	if tmux.EmulatorEnabled() {
		time.Sleep(3 * time.Second)
	} else {
		time.Sleep(500 * time.Millisecond)
	}
	return tickUpdateMetadataMessage{}
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
	ti := overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.settings().GetProfiles())
	ti.SetBaseBranchName(m.baseBranchName)
	return ti
}

// resolveBaseBranchCmd looks up the ref new sessions will be cut from, for
// the branch picker's label. The configured value is read here, on the main
// goroutine, rather than inside the returned Cmd — appConfig is mutable at
// runtime and Cmd bodies run concurrently with Update.
func (m *home) resolveBaseBranchCmd() tea.Cmd {
	repoDir := m.repoPath()
	configured := m.settings().GetBaseBranch()
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

// cancelPromptOverlay cancels the prompt overlay, discarding the draft
// of a creation flow (none when the overlay was prompting a running
// session).
func (m *home) cancelPromptOverlay() tea.Cmd {
	m.discardDraft()
	m.dismissOverlay()
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)
	return tea.RequestWindowSize
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

// runTask runs task's sync step and returns its async one, like
// task.Run, then rereads the view stores (syncViews): a sync step makes a
// request whose pre-step changes the model's instances at once (a kill's
// Deleting, a create's new row, a resume's Loading), and what follows it
// in the same Update (instanceChanged) reads the change back.
func (m *home) runTask(task overlay.ConfirmationTask) tea.Cmd {
	cmd := task.Run()
	m.syncViews()
	return cmd
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
	if len(m.slots) > 0 && m.info.RepoPath != "" {
		return m.info.RepoPath
	}
	cwd, _ := os.Getwd()
	return cwd
}

// configDir returns the config directory for the focused slot — in global
// mode the global dir, which the global slot's context carries like any
// other slot's (so sessions created there get subagent hooks). Returns
// empty string only for a nil context (bare test homes), which callers
// resolve to config.GetConfigDir.
func (m *home) configDir() string { return m.info.ConfigDir }

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
	if banner := m.bannerView(); banner != "" {
		sections = append(sections, banner)
	}
	if m.accountStrip != nil {
		if strip := m.accountStrip.String(); strip != "" {
			// Padded to the full width: the sections are joined centered,
			// which would otherwise float the shorter strip mid-row.
			sections = append(sections, lipgloss.PlaceHorizontal(m.lastWidth, lipgloss.Left, strip))
		}
	}
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
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings, stateMergePicker, stateLaunchOptions, stateIssuePicker:
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
		m.panes.For(selected).ForwardFocus(in)
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
	if t, ok := m.panes.For(sel).PaneTitle(); ok {
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
	// HitTest(mouse.X - m.listWidth, mouse.Y - m.topChromeHeight()).
	c := tea.NewCursor(xOff+lx, m.topChromeHeight()+ly)
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
