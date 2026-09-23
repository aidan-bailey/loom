package app

import (
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// workspaceSlot bundles per-workspace state so multiple workspaces can be
// loaded in memory simultaneously. It is the single owner of that state:
// home embeds the focused slot (*workspaceSlot), so m.list, m.storage and
// the rest resolve to the focused slot's own fields, and every other open
// slot is reached through m.slots. Slots are always handled by pointer.
type workspaceSlot struct {
	// wsCtx is the workspace's context (name, repo path, config dir).
	// Nil only for the global slot enterGlobalMode builds; the classic
	// slot newHome builds carries the startup context.
	wsCtx *config.WorkspaceContext
	// storage saves/loads this workspace's instances.
	storage *session.Storage
	// appConfig is this workspace's persistent configuration.
	appConfig *config.Config
	// appState is this workspace's persistent app state: help screens
	// seen and the UI prefs block.
	appState config.AppState
	// list is this workspace's session rail (its instances).
	list *ui.List
	// splitPane displays the agent and terminal panes with diff overlay.
	splitPane *ui.SplitPane
	// workbench renders the right content panel when viewMode is
	// viewWorkbench; the left half is splitPane with its terminal hidden.
	// It pairs with this slot's splitPane (its terminal tab shows the
	// slot's shared TerminalPane). Non-nil for every slot.
	workbench *ui.Workbench
	// recovery holds the orphan-reconcile summary from this slot's last
	// activation, surfaced once the slot becomes focused.
	recovery recoverySummary
}

// activateWorkspace loads a workspace's state, config, instances and UI
// components, appending a new slot to m.slots. The first tab opened from
// classic/global mode takes focus at once (see the invariant on
// home.workspaceSlot); later ones open in the background, and callers
// that want to show one focus it with loadSlot.
//
// Focusing the first tab drops the classic slot; the returned Cmd
// releases its instances' preview attach clients (releaseSlotCmd) and is
// nil otherwise. Callers must return it (or, before the program runs,
// run it).
func (m *home) activateWorkspace(ws config.Workspace) (tea.Cmd, error) {
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
		return nil, fmt.Errorf("failed to create storage for workspace %s: %w", ws.Name, err)
	}

	cmdExec := m.executor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		// Fail closed: do NOT proceed to build an empty slot. Continuing
		// here would append a slot with zero instances, and the next
		// SaveInstances for it would overwrite a possibly-recoverable
		// (e.g. transiently unreadable or corrupt) state.json with only
		// the survivors — silent per-workspace data loss. The classic
		// startup path already fails closed this way; mirror it. The slot
		// is simply not opened, leaving state.json on disk untouched.
		return nil, fmt.Errorf("load instances for workspace %s: %w", ws.Name, err)
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
		if !inst.CrashRecovered() {
			continue
		}
		if err := inst.CrashRestart(); err != nil {
			log.For("app").Error("crash_recovery.restart_failed", "instance", inst.Title, "err", err)
			if tErr := inst.TransitionTo(session.Paused); tErr != nil {
				log.For("app").Warn("crash_recovery.transition_failed", "instance", inst.Title, "err", tErr)
			}
		}
		inst.SetCrashRecovered(false)
	}

	// Auto-create workspace terminal if none exists. A record storage
	// preserves but could not load (after a downgrade every record is
	// undecodable, the terminal included) may already own the title: then
	// the terminal exists, just not in this binary's list, and killing its
	// session plus creating a second same-titled record would clobber it.
	wtTitle := ws.Name
	if wtTitle == "" {
		wtTitle = "Workspace Terminal"
	}
	if !hasWorkspaceTerminal && wsCtx.RepoPath != "" && !slices.Contains(storage.PreservedTitles(), wtTitle) {
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
	m.slots = append(m.slots, &workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: appConfig,
		appState:  state,
		list:      list,
		splitPane: splitPane,
		workbench: ui.NewWorkbench(ui.NewDiffPane(), splitPane.Terminal()),
		recovery:  recovery,
	})
	var release tea.Cmd
	if len(m.slots) == 1 {
		// Leaving classic mode: the classic slot is not in m.slots, so the
		// invariant needs the new tab focused before we return. loadSlot
		// runs the classic slot's workbench cleanup and ratio flush first;
		// the slot is then dropped, so its attach clients go too.
		classic := m.workspaceSlot
		m.loadSlot(0)
		release = releaseSlotCmd(classic)
	}
	// Force the next health tick to poll: a newly opened workspace's repo
	// wasn't in openRepoPaths() until just now, and without this the
	// poller stays silent on it until the ambient ghInterval next elapses.
	m.gate(gateGH).expedite()
	return release, nil
}

// deactivateWorkspace saves and closes a workspace tab by name.
// Returns an error if SaveInstances fails; in that case the slot is
// kept in memory so the user can retry rather than losing access to
// unpersisted session state. This matches handleQuit's policy that
// silent data loss on slot teardown is worse than a sticky tab.
//
// The invariant on home.workspaceSlot holds on return. Closing the
// focused tab refocuses, via loadSlot, the tab that slides into its
// index (or the new last tab). The last open tab is never closed here:
// leaving no tab means leaving workspace mode, and only enterGlobalMode
// builds the slot that must take focus then.
//
// The returned Cmd releases the closed slot's preview attach clients
// (releaseSlotCmd; nil when none is attached) and must be returned to
// the runtime.
func (m *home) deactivateWorkspace(name string) (tea.Cmd, error) {
	idx := slices.IndexFunc(m.slots, func(s *workspaceSlot) bool { return s.wsCtx.Name == name })
	if idx == -1 {
		return nil, nil
	}
	if len(m.slots) == 1 {
		return nil, fmt.Errorf("cannot close %s, the last open workspace: return to global mode instead", name)
	}

	slot := m.slots[idx]
	if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
		log.For("app").Error("workspace.save_failed", "name", name, "err", err)
		return nil, fmt.Errorf("failed to save workspace %s: %w", name, err)
	}

	wasFocused := idx == m.focusedSlot
	m.slots = slices.Delete(m.slots, idx, idx+1)
	switch {
	case wasFocused:
		// m.workspaceSlot still points at the closed slot, so loadSlot's
		// departing-slot cleanup (workbench, ratio flush) lands on it.
		m.loadSlot(min(idx, len(m.slots)-1))
	case idx < m.focusedSlot:
		m.focusedSlot--
	}
	return releaseSlotCmd(slot), nil
}

// releaseSlotCmd returns a Cmd that releases the attach clients a dropped
// slot holds: its instances' preview clients (releaseInstancesCmd) and the
// ones its terminal pane keeps on each loom_term_* shell it has shown (the
// shells keep running). Every site that drops a slot from the model
// returns it: activateWorkspace (the classic slot), deactivateWorkspace
// (the closed tab) and enterGlobalMode (every tab, or the previous global
// slot — except for the panes it carries into the new global slot, whose
// terminals stay in use). nil when nothing is attached.
func releaseSlotCmd(slot *workspaceSlot) tea.Cmd {
	if slot == nil {
		return nil
	}
	var cmds []tea.Cmd
	if slot.list != nil {
		cmds = append(cmds, releaseInstancesCmd(slot.list.GetInstances()))
	}
	if slot.splitPane != nil {
		var terms []attachedClient
		for _, ts := range slot.splitPane.Terminal().DetachAll() {
			if ts.PtmxAlive() {
				terms = append(terms, attachedClient{name: ts.SessionName(), ts: ts})
			}
		}
		cmds = append(cmds, releaseClientsCmd(terms))
	}
	return tea.Batch(cmds...)
}

// releaseInstancesCmd returns a Cmd that closes loom's preview attach
// client — PTY, output pump and emulator — for each started, non-paused
// instance in insts that has one, leaving the tmux sessions running. The
// instances are being dropped from the model, and must not keep theirs:
// reloading the same workspace attaches a second client to each live
// session (LoadAndReconcile → EnsureRunning), and the stale one keeps
// pumping output, emitting pane events and fighting over the window
// size. nil when nothing is attached.
//
// The instances are snapshotted here, on the Update goroutine. By the
// time the Cmd runs they must be unreachable from the model — the drop
// sites' callers repoint the panes and menu with instanceChanged before
// returning — and a health-probe result still in flight for one is
// dropped by applyLiveness, so nothing re-attaches them (RepairPtmx)
// afterwards. The release runs in the Cmd, off Update:
// PausePreview waits — up to the pump-exit timeout, per session — for the
// output pump to exit, and the pump delivers pane events through
// tea.Program.Send, which blocks until Update returns. PausePreview is
// serialized by the session's stateMu against a concurrent kill/pause.
// The terminal pane's own loom_term_* sessions belong to the pane, not
// the instance, and are left alone.
func releaseInstancesCmd(insts []*session.Instance) tea.Cmd {
	var release []attachedClient
	for _, inst := range insts {
		if !inst.Started() || inst.Paused() {
			continue
		}
		if ts := inst.TmuxSession(); ts != nil && ts.PtmxAlive() {
			release = append(release, attachedClient{name: inst.Title, ts: ts})
		}
	}
	return releaseClientsCmd(release)
}

// attachedClient is a tmux session whose attach client a release closes;
// name labels it in logs.
type attachedClient struct {
	name string
	ts   *tmux.TmuxSession
}

// releaseClientsCmd returns a Cmd that closes each client's attach PTY
// (PausePreview) off the Update goroutine — see releaseInstancesCmd for
// why it must — or nil when there are none.
func releaseClientsCmd(clients []attachedClient) tea.Cmd {
	if len(clients) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, c := range clients {
			if err := c.ts.PausePreview(); err != nil {
				log.For("app").Warn("slot_release.preview_close_failed", "session", c.name, "err", err)
			}
		}
		return nil
	}
}

// removeInstanceEverywhere removes inst (by identity) from every loaded
// slot's list. Async op completions land on whatever slot is focused at
// delivery time, which may not be the slot that owns the instance — slot
// lists are in-memory until restart, so a missed removal would orphan the
// row (e.g. stuck in Deleting) with its backing resources already gone.
func (m *home) removeInstanceEverywhere(inst *session.Instance) {
	if inst == nil {
		return
	}
	for _, slot := range m.openSlots() {
		slot.list.RemoveInstance(inst)
	}
}

// openSlots returns every loaded workspace slot: the open tabs, or in
// classic/global mode the classic slot alone. The focused slot is always
// among them.
func (m *home) openSlots() []*workspaceSlot {
	if len(m.slots) == 0 {
		return []*workspaceSlot{m.workspaceSlot}
	}
	return m.slots
}

// checkSlotInvariant reports a violation of the embedded-focused-slot
// invariant (see home.workspaceSlot): the focused slot is never nil, no
// slot appears in m.slots twice, and with tabs open the focused slot is
// m.slots[m.focusedSlot]. In classic/global mode (no tabs) the focused
// slot is the classic slot, outside m.slots by construction.
// Tests call it after every slot transition.
func (m *home) checkSlotInvariant() error {
	if m.workspaceSlot == nil {
		return errors.New("slot invariant: focused slot is nil")
	}
	seen := make(map[*workspaceSlot]int, len(m.slots))
	for i, s := range m.slots {
		if s == nil {
			return fmt.Errorf("slot invariant: m.slots[%d] is nil", i)
		}
		if j, dup := seen[s]; dup {
			return fmt.Errorf("slot invariant: m.slots[%d] and m.slots[%d] are the same slot", j, i)
		}
		seen[s] = i
	}
	if len(m.slots) == 0 {
		return nil
	}
	if m.focusedSlot < 0 || m.focusedSlot >= len(m.slots) {
		return fmt.Errorf("slot invariant: focusedSlot %d out of range [0,%d)", m.focusedSlot, len(m.slots))
	}
	if m.workspaceSlot != m.slots[m.focusedSlot] {
		if i, ok := seen[m.workspaceSlot]; ok {
			return fmt.Errorf("slot invariant: focused slot is m.slots[%d], but focusedSlot is %d", i, m.focusedSlot)
		}
		return fmt.Errorf("slot invariant: focused slot is not in m.slots (focusedSlot %d)", m.focusedSlot)
	}
	return nil
}

// leaveFocusedSlot runs the departing-slot teardown every focus change
// needs, while the focused slot's splitPane/appState are still the ones
// m.* resolves to. It copies nothing: the slot owns its state. loadSlot
// runs it too, so a separate call is only needed where focus leaves by
// another route (enterGlobalMode) or the teardown must come before other
// work (applyWorkspaceToggle, handleQuit).
func (m *home) leaveFocusedSlot() {
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
}

// loadSlot focuses slot idx: it becomes the embedded m.workspaceSlot, and
// the tab bar, layout and UI prefs follow it. It is the focus-change
// choke point, and runs leaveFocusedSlot's teardown on the departing
// slot first so that no path can skip it. Loading the slot that is
// already focused only refreshes the tab bar and peer sections.
func (m *home) loadSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) {
		return
	}
	if slot := m.slots[idx]; slot == m.workspaceSlot {
		// Nothing departs, only the tab set around the slot may have
		// changed: activateWorkspace focused the first tab and the caller
		// now focuses it again, a toggle whose deactivation already
		// refocused ends by re-focusing, or tabs came and went beside it.
		// Skip the teardown and the layout pass below (it loops tmux
		// SetDetachedSize); the tab bar keeps its height with tabs open.
		m.focusedSlot = idx
		m.tabBar.SetWorkspaces(m.slotNames(), idx)
		m.refreshPeerSections()
		return
	}
	// Departing-slot teardown (idempotent where leaveFocusedSlot already
	// ran): the workbench cleanup and the pending split-ratio flush must
	// land on the slot being left, and m.* resolves to it until the swap
	// below. Focus-changing callers rely on this rather than running the
	// teardown themselves.
	m.leaveFocusedSlot()
	slot := m.slots[idx]
	m.focusedSlot = idx
	m.workspaceSlot = slot
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
// == 0, the focused slot is the global one (~/.loom state). The first tab
// to open drops it, and with it any in-flight changes the user hadn't
// quit-flushed yet. Persist it before the transition so the reverse
// direction (enterGlobalMode) reads back what the user was just looking
// at.
func (m *home) applyWorkspaceToggle(desired []config.Workspace) tea.Cmd {
	if len(m.slots) == 0 {
		err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances()))
		switch {
		case errors.Is(err, session.ErrStorageLoadFailed):
			// The global payload is unreadable (typically the fail-closed
			// case of loadStartupStorageFallback), so the storage refuses
			// every write and no save here could ever succeed: blocking
			// would strand the user in an unwritable global mode. Leave
			// the file untouched and switch.
			log.For("app").Warn("global_save_skipped", "reason", "storage_load_failed", "err", err)
		case err != nil:
			return m.handleError(fmt.Errorf("failed to save global state before workspace transition: %w", err))
		}
	} else {
		m.leaveFocusedSlot()
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
	var releases []tea.Cmd

	// 1. Activate new workspaces first (safe — adds to slots without removing).
	currentNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		currentNames[slot.wsCtx.Name] = true
	}
	for _, ws := range desired {
		if !currentNames[ws.Name] {
			release, err := m.activateWorkspace(ws)
			if err != nil {
				activationErrors = append(activationErrors,
					fmt.Sprintf("%s: %v", ws.Name, err))
			}
			releases = append(releases, release)
		}
	}

	// 2. Deactivate slots not in desired (reverse order to keep indices stable).
	// Slots whose save fails stay in m.slots; the user is told via handleError below.
	// Skipped when no desired workspace is open (every activation failed):
	// closing the rest would leave no tab at all, which is enterGlobalMode's
	// transition, not this one — keep the open tabs and report the failures.
	if slices.ContainsFunc(m.slots, func(s *workspaceSlot) bool { return desiredNames[s.wsCtx.Name] }) {
		for i := len(m.slots) - 1; i >= 0; i-- {
			if !desiredNames[m.slots[i].wsCtx.Name] {
				release, err := m.deactivateWorkspace(m.slots[i].wsCtx.Name)
				if err != nil {
					deactivationErrors = append(deactivationErrors,
						fmt.Sprintf("%s: %v", m.slots[i].wsCtx.Name, err))
				}
				releases = append(releases, release)
			}
		}
	}

	// 3. Re-focus the focused slot so the tab bar and peer sections
	// reflect the new set. Activation and deactivation have already
	// focused the right slot (running any layout pass), so this is
	// loadSlot's cheap already-focused path.
	m.loadSlot(m.focusedSlot)

	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	m.saveOpenWorkspaces()
	if len(m.slots) > 0 {
		m.showRecoverySummary(m.slots[m.focusedSlot].recovery)
	}

	// Point the panes and menu at the (possibly new) selection, so none
	// of them keeps a dropped instance, then release the dropped slots.
	cmds := append([]tea.Cmd{tea.RequestWindowSize, m.instanceChanged()}, releases...)

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
		cmds = append(cmds, m.handleError(fmt.Errorf("%s", strings.Join(msgs, "; "))))
	}
	return tea.Batch(cmds...)
}

// enterGlobalMode transitions from workspace-tab mode back to global
// (no-workspace) mode. Builds a fresh global slot — storage, state and
// list reloaded from scratch via the same path as newHome — rather than
// keeping the classic slot around for the round trip.
//
// Tmux note: closing the tabs doesn't kill their tmux sessions. Session
// names are loom_<title>, keyed by title alone, so a global instance whose
// title matches one in a closing tab shares its tmux session, and
// LoadAndReconcile attaches a second client to it while the tab's is
// still attached. The overlap is brief: the release Cmds returned below
// detach every dropped instance's client.
//
// Fails closed, with nothing switched: no tab closed, storage and list
// unswapped, and the registry unchanged (workbench mode may already have
// been exited — applyWorkspaceToggle runs leaveFocusedSlot first). That
// covers two failures:
//   - The global load, which runs before anything is torn down. Carrying
//     on with an empty list would let its next save overwrite a
//     possibly-recoverable global state.json — the same rule
//     activateWorkspace and the classic startup path follow.
//   - Saving any open tab. Every tab is saved before any is closed, so a
//     failure can't leave a half-switched home (an open tab that is no
//     longer the focused slot).
func (m *home) enterGlobalMode() tea.Cmd {
	// Reconstruct global storage. cfgDir="" is interpreted as ~/.loom
	// by config.LoadStateFrom / session.NewStorage — same as newHome.
	appState := config.LoadStateFrom("")
	appConfig := config.LoadConfigFrom("")
	storage, err := session.NewStorage(appState, "")
	if err != nil {
		return m.handleError(fmt.Errorf("failed to construct global storage: %w", err))
	}

	cmdExec := m.executor()
	instances, err := storage.LoadAndReconcile(cmdExec)
	if err != nil {
		return m.handleError(fmt.Errorf("failed to load global sessions (staying in workspace mode): %w", err))
	}

	// Persist every workspace tab before closing any of them. A tab whose
	// save fails keeps its unpersisted state reachable only while open, so
	// abort; the global instances just loaded are dropped, releasing the
	// preview PTYs LoadAndReconcile attached so a retry does not stack a
	// second attach client on each live session.
	for _, slot := range m.slots {
		if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
			log.For("app").Error("workspace.save_failed", "name", slot.wsCtx.Name, "err", err)
			return tea.Batch(
				m.handleError(fmt.Errorf("failed to save workspace %s (staying in workspace mode): %w", slot.wsCtx.Name, err)),
				releaseInstancesCmd(instances))
		}
	}

	// Picker escape hatch (W → Global row) from global mode reaches here
	// with no leaveFocusedSlot of its own — clean up workbench residue
	// (wbRatio flush, split-terminal restore) and flush pending ratios
	// while the departing slot's appState is still current, or handleQuit
	// later flushes them into the new global state.json.
	m.leaveFocusedSlot()

	// The global slot is built fresh, but keeps the departing slot's
	// splitPane and workbench: the panes are sized and wired already, and
	// the closed tab no longer uses them.
	global := &workspaceSlot{
		storage:   storage,
		appConfig: appConfig,
		appState:  appState,
		list:      ui.NewList(&m.spinner),
		splitPane: m.splitPane,
		workbench: m.workbench,
	}
	for _, inst := range instances {
		global.list.AddInstance(inst)
	}
	// Everything loaded so far is dropped: every tab, or — global mode
	// re-entered from global mode — the previous global slot. The focused
	// one's panes live on in the global slot.
	dropped, carried := m.openSlots(), m.workspaceSlot
	m.slots = nil
	m.focusedSlot = 0
	m.workspaceSlot = global

	// Clear registry's open-tab list so the next launch lands in
	// global mode rather than auto-restoring tabs the user just closed.
	// An explicit return to global mode also ends restore-fallback mode.
	m.restoreFellBack = false
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

	// Point the carried-over panes and the menu at the global selection,
	// so none of them keeps a dropped instance, then release the drops.
	cmds := []tea.Cmd{tea.RequestWindowSize, m.instanceChanged()}
	for _, slot := range dropped {
		if slot == carried {
			cmds = append(cmds, releaseInstancesCmd(slot.list.GetInstances()))
			continue
		}
		cmds = append(cmds, releaseSlotCmd(slot))
	}
	return tea.Batch(cmds...)
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
		for _, inst := range slot.list.GetInstances() {
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
// the non-focused slots' lists (the focused slot is skipped: it is the
// rail's own workspace, not a peer). Main-goroutine only (reads slot
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
