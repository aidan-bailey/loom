package app

import (
	"fmt"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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
	// workbench pairs with this slot's splitPane (its terminal tab
	// shows the slot's shared TerminalPane), swapped onto home by
	// loadSlot exactly like splitPane.
	workbench *ui.Workbench
	// recovery holds the orphan-reconcile summary from this slot's last
	// activation, surfaced once the slot becomes focused.
	recovery recoverySummary
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
	session.SetSubagentTrackingEnabled(appConfig.SubagentTrackingEnabled())
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
	// Force the next health tick to poll: a newly opened workspace's repo
	// wasn't in openRepoPaths() until just now, and without this the
	// poller stays silent on it until the ambient ghInterval next elapses.
	m.lastGHQuery = time.Time{}
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
// removeInstanceEverywhere removes inst (by identity) from the focused list
// and every workspace slot's list. Async op completions land on whatever
// m.list is focused at delivery time, which may not be the list that owns
// the instance — slot lists are in-memory until restart, so a missed removal
// would orphan the row (e.g. stuck in Deleting) with its backing resources
// already gone. Removal is idempotent, so hitting both m.list and its
// backing slot entry is harmless.
func (m *home) removeInstanceEverywhere(inst *session.Instance) {
	if inst == nil {
		return
	}
	m.list.RemoveInstance(inst)
	for i := range m.slots {
		m.slots[i].list.RemoveInstance(inst)
	}
}

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
	// Picker escape hatch (W → deselect-all) reaches here without the
	// saveCurrentSlot/loadSlot choke points — clean up workbench residue
	// (wbRatio flush, split-terminal restore) while the workspace slot's
	// appState is still current, or handleQuit later flushes it into the
	// wrong (global) state.json.
	m.cleanupWorkbench()
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
